// Copyright (c) 2018, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
	"io"
	"io/fs"
	"os"
	"reflect"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func parseWord(t *testing.T, src string) *syntax.Word {
	t.Helper()
	p := syntax.NewParser()
	word, err := p.Document(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	return word
}

func parseCallArg(t *testing.T, src string, index int) *syntax.Word {
	t.Helper()
	p := syntax.NewParser()
	file, err := p.Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatal(err)
	}
	call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok {
		t.Fatalf("wanted call expression, got %T", file.Stmts[0].Cmd)
	}
	return call.Args[index]
}

func TestConfigNils(t *testing.T) {
	os.Setenv("EXPAND_GLOBAL", "value")
	tests := []struct {
		name string
		cfg  *Config
		src  string
		want string
	}{
		{
			"NilConfig",
			nil,
			"$EXPAND_GLOBAL",
			"",
		},
		{
			"ZeroConfig",
			&Config{},
			"$EXPAND_GLOBAL",
			"",
		},
		{
			"EnvConfig",
			&Config{Env: ListEnviron(os.Environ()...)},
			"$EXPAND_GLOBAL",
			"value",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			word := parseWord(t, tc.src)
			got, err := Literal(tc.cfg, word)
			if err != nil {
				t.Fatalf("did not want error, got %v", err)
			}
			if got != tc.want {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
		})
	}
}

func TestFormatLocaleDecimal(t *testing.T) {
	got, _, err := Format(&Config{Env: ListEnviron("LANG=de_DE.UTF-8")}, "%.4f", []string{"1"})
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	if want := "1,0000"; got != want {
		t.Fatalf("wanted %q, got %q", want, got)
	}

	got, _, err = Format(&Config{Env: ListEnviron("LANG=C")}, "%.4f", []string{"1"})
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	if want := "1.0000"; got != want {
		t.Fatalf("wanted %q, got %q", want, got)
	}
}

func TestFormatLocaleUnicodeEscapes(t *testing.T) {
	tests := []struct {
		env  string
		fmt  string
		want string
	}{
		{"LC_CTYPE=fr_FR.ISO8859-1", `\U000000e9`, "\xe9"},
		{"LC_CTYPE=zh_TW.BIG5", `\U000003a8`, "\xa3Z"},
		{"LC_CTYPE=ja_JP.SJIS", `\U0000ff8e`, "\xce"},
		{"LC_CTYPE=C.UTF-8", `\U000000e9`, "é"},
		{"LC_CTYPE=en_US.UTF-8", `\U01000000`, "\xf9\x80\x80\x80\x80"},
		{"LC_CTYPE=en_US.UTF-8", `\U70000000`, "\xfd\xb0\x80\x80\x80\x80"},
		{"LC_CTYPE=en_US.UTF-8", `\Uffffffff`, ""},
	}
	for _, tc := range tests {
		got, _, err := Format(&Config{Env: ListEnviron(tc.env)}, tc.fmt, nil)
		if err != nil {
			t.Fatalf("%s: did not want error, got %v", tc.env, err)
		}
		if got != tc.want {
			t.Fatalf("%s: wanted bytes %v, got %v", tc.env, []byte(tc.want), []byte(got))
		}
	}
}

func TestParamRemovePatternInvalidByte(t *testing.T) {
	cfg := &Config{Env: ListEnviron(
		"euro=\342\202\254",
		"mid=\202",
	)}
	word := parseWord(t, `${euro##*$mid}`)
	got, err := Literal(cfg, word)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	if want := "\254"; got != want {
		t.Fatalf("wanted bytes %v, got %v", []byte(want), []byte(got))
	}
}

func TestDocumentParamExpDefaultQuoteRemoval(t *testing.T) {
	cfg := &Config{Env: ListEnviron("P=A")}
	word := parseWord(t, `${P+\"$P\"}`)
	got, err := Document(cfg, word)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	if want := `"A"`; got != want {
		t.Fatalf("wanted %q, got %q", want, got)
	}

	word = parseWord(t, `${P+$'A\n'}`)
	got, err = Document(cfg, word)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	if want := `$'A\n'`; got != want {
		t.Fatalf("wanted %q, got %q", want, got)
	}
}

func TestFieldsIdempotency(t *testing.T) {
	tests := []struct {
		src  string
		want []string
	}{
		{
			"{1..4}",
			[]string{"1", "2", "3", "4"},
		},
		{
			"a{1..4}",
			[]string{"a1", "a2", "a3", "a4"},
		},
	}
	for _, tc := range tests {
		word := parseWord(t, tc.src)
		for range 2 {
			got, err := Fields(nil, word)
			if err != nil {
				t.Fatalf("did not want error, got %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
		}
	}
}

func TestFieldsParamExpSubstWordQuotes(t *testing.T) {
	cfg := &Config{Env: ListEnviron("A=aaa bbb ccc")}
	tests := []struct {
		src  string
		want []string
	}{
		{`${B:-"$A"}`, []string{"aaa bbb ccc"}},
		{`${B-"$A"}`, []string{"aaa bbb ccc"}},
		{`${B:-""}`, []string{""}},
	}
	for _, tc := range tests {
		word := parseWord(t, tc.src)
		got, err := Fields(cfg, word)
		if err != nil {
			t.Fatalf("%s: did not want error, got %v", tc.src, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: wanted %q, got %q", tc.src, tc.want, got)
		}
	}
}

func TestFieldsParamExpSubstWordQuotedAt(t *testing.T) {
	cfg := &Config{Env: testEnv{
		"1": {Set: true, Kind: String, Str: "a b"},
		"@": {Set: true, Kind: Indexed, List: []string{"a b", "c", "d"}},
	}}
	for _, src := range []string{`${1+"$@"}`} {
		word := parseWord(t, src)
		got, err := Fields(cfg, word)
		if err != nil {
			t.Fatalf("%s: did not want error, got %v", src, err)
		}
		want := []string{"a b", "c", "d"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: wanted %q, got %q", src, want, got)
		}
	}
}

func TestFieldsParamExpUnsetPositionalDefault(t *testing.T) {
	cfg := &Config{Env: testEnv{
		"*": {Kind: Indexed},
		"@": {Kind: Indexed},
	}}
	for _, src := range []string{`${*-x}`, `${@-x}`} {
		word := parseWord(t, src)
		got, err := Fields(cfg, word)
		if err != nil {
			t.Fatalf("%s: did not want error, got %v", src, err)
		}
		want := []string{"x"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: wanted %q, got %q", src, want, got)
		}
	}
	for _, src := range []string{`echo "${*-x}"`, `echo "${@-x}"`} {
		word := parseCallArg(t, src, 1)
		got, err := Fields(cfg, word)
		if err != nil {
			t.Fatalf("%s: did not want error, got %v", src, err)
		}
		want := []string{"x"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: wanted %q, got %q", src, want, got)
		}
	}
}

func TestFieldsQuotedAtDefaultPreservesElements(t *testing.T) {
	cfg := &Config{Env: testEnv{
		"@": {Set: true, Kind: Indexed, List: []string{"a", "b"}},
	}}
	word := parseCallArg(t, `echo "${@-}"`, 1)
	got, err := Fields(cfg, word)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wanted %q, got %q", want, got)
	}
}

func TestFieldsParamExpDefaultBackslashInDoubleQuotes(t *testing.T) {
	cfg := &Config{Env: ListEnviron("somevar=", "HOME=/usr/homes/chet")}
	tests := []struct {
		src  string
		want []string
	}{
		{`echo "${somevar:-\$HOME}"`, []string{"$HOME"}},
		{`echo "${somevar:-\ \$HOME}"`, []string{`\ $HOME`}},
		{`echo "${somevar:-\ \ \$HOME}"`, []string{`\ \ $HOME`}},
	}
	for _, tc := range tests {
		index := 1
		if strings.HasPrefix(tc.src, "echo foo ") {
			index = 2
		}
		word := parseCallArg(t, tc.src, index)
		got, err := Fields(cfg, word)
		if err != nil {
			t.Fatalf("%s: did not want error, got %v", tc.src, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: wanted %q, got %q", tc.src, tc.want, got)
		}
	}
}

func TestFieldsParamExpDefaultBackslashFields(t *testing.T) {
	cfg := &Config{Env: ListEnviron("HOME=/usr/homes/chet")}
	tests := []struct {
		src  string
		want []string
	}{
		{`echo ${somevar:-a\ b}`, []string{"a b"}},
		{`echo ${somevar:-a\\b}`, []string{`a\b`}},
		{`echo ${somevar:-\$HOME}`, []string{"$HOME"}},
		{`echo ${somevar:-string \\\}}`, []string{"string", `\}`}},
	}
	for _, tc := range tests {
		index := 1
		if strings.HasPrefix(tc.src, "echo foo ") {
			index = 2
		}
		word := parseCallArg(t, tc.src, index)
		got, err := Fields(cfg, word)
		if err != nil {
			t.Fatalf("%s: did not want error, got %v", tc.src, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: wanted %q, got %q", tc.src, tc.want, got)
		}
	}
}

func TestFieldsParamExpAssignSingleQuotesInDoubleQuotes(t *testing.T) {
	cfg := &Config{Env: testEnv{
		"foo": {Set: true, Kind: String, Str: "bar"},
	}}
	word := parseCallArg(t, `echo "${fox='$foo'}"`, 1)
	got, err := Fields(cfg, word)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	want := []string{"'bar'"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wanted %q, got %q", want, got)
	}
}

func TestFieldsParamExpPosixEscapedBrace(t *testing.T) {
	cfg := &Config{Env: ListEnviron("IFS= \t\n")}
	tests := []struct {
		src  string
		want []string
	}{
		{`echo "${IFS+\}z}"`, []string{"}z"}},
		{`echo "${IFS+\"\}\"z}"`, []string{`"}"z`}},
	}
	for _, tc := range tests {
		word := parseCallArg(t, tc.src, 1)
		got, err := Fields(cfg, word)
		if err != nil {
			t.Fatalf("%s: did not want error, got %v", tc.src, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: wanted %q, got %q", tc.src, tc.want, got)
		}
	}
}

func TestFieldsParamExpAssignQuoteRemoval(t *testing.T) {
	cfg := &Config{Env: testEnv{}}
	word := parseCallArg(t, `echo ${v=a\ b} x ${v=c\ d}`, 1)
	got, err := Fields(cfg, word)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wanted %q, got %q", want, got)
	}
	if v := cfg.Env.Get("v").String(); v != "a b" {
		t.Fatalf("v = %q, want %q", v, "a b")
	}
}

func TestFieldsParamExpAssignLeadingTilde(t *testing.T) {
	cfg := &Config{Env: testEnv{
		"HOME": {Set: true, Kind: String, Str: "/usr/xyz"},
	}}
	word := parseCallArg(t, `echo ${ADDPATH:=~/bin:~/bin2}`, 1)
	got, err := Fields(cfg, word)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	want := []string{"/usr/xyz/bin:~/bin2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wanted %q, got %q", want, got)
	}
	if v := cfg.Env.Get("ADDPATH").String(); v != "/usr/xyz/bin:~/bin2" {
		t.Fatalf("ADDPATH = %q, want %q", v, "/usr/xyz/bin:~/bin2")
	}
}

func TestFieldsParamExpAssignAtNullIFSPosix(t *testing.T) {
	cfg := &Config{
		Env: testEnv{
			"IFS": {Set: true, Kind: String, Str: ""},
			"@":   {Set: true, Kind: Indexed, List: []string{"1", "2"}},
		},
		Posix: true,
	}
	word := parseCallArg(t, `echo ${v=$@}`, 1)
	got, err := Fields(cfg, word)
	if err != nil {
		t.Fatalf("did not want error, got %v", err)
	}
	want := []string{"1 2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wanted %q, got %q", want, got)
	}
	if v := cfg.Env.Get("v").String(); v != "1 2" {
		t.Fatalf("v = %q, want %q", v, "1 2")
	}
}

func TestFieldsParamExpArrayStarSubstWord(t *testing.T) {
	tests := []struct {
		name      string
		ifs       string
		src       string
		want      []string
		wantVar   string
		wantVarOK bool
	}{
		{
			name: "colon default",
			ifs:  ":",
			src:  `echo ${v-${a[*]}}`,
			want: []string{"abc", "def ghi", "jkl"},
		},
		{
			name:    "colon assign",
			ifs:     ":",
			src:     `echo ${v=${a[*]}}`,
			want:    []string{"abc", "def ghi", "jkl"},
			wantVar: "abc:def ghi:jkl", wantVarOK: true,
		},
		{
			name: "empty default",
			ifs:  "",
			src:  `echo ${v-${a[*]}}`,
			want: []string{"abc", "def ghi", "jkl"},
		},
		{
			name:    "empty assign",
			ifs:     "",
			src:     `echo ${v=${a[*]}}`,
			want:    []string{"abcdef ghijkl"},
			wantVar: "abcdef ghijkl", wantVarOK: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Env: testEnv{
				"IFS": {Set: true, Kind: String, Str: tc.ifs},
				"a":   {Set: true, Kind: Indexed, List: []string{"abc", "def ghi", "jkl"}},
			}}
			word := parseCallArg(t, tc.src, 1)
			got, err := Fields(cfg, word)
			if err != nil {
				t.Fatalf("did not want error, got %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
			if tc.wantVarOK {
				if v := cfg.Env.Get("v").String(); v != tc.wantVar {
					t.Fatalf("v = %q, want %q", v, tc.wantVar)
				}
			}
		})
	}
}

func TestFieldsParamExpAlternatePreservesQuotedFields(t *testing.T) {
	cfg := &Config{Env: ListEnviron("IFS= \t\n", "u=x")}
	tests := []struct {
		src  string
		want []string
	}{
		{`echo ${IFS+foo 'b\
ar' baz}`, []string{"foo", "b\\\nar", "baz"}},
		{`echo ${IFS+a" b" c}`, []string{"a b", "c"}},
	}
	for _, tc := range tests {
		word := parseCallArg(t, tc.src, 1)
		got, err := Fields(cfg, word)
		if err != nil {
			t.Fatalf("%s: did not want error, got %v", tc.src, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: wanted %q, got %q", tc.src, tc.want, got)
		}
	}
}

func TestFieldsParamExpAlternateQuotedAtWithQuotedEmpty(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"no args", nil, []string{""}},
		{"one empty", []string{""}, []string{""}},
		{"two empty", []string{"", ""}, []string{"", ""}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Env: testEnv{
				"x": {Set: true, Kind: String, Str: "x"},
				"@": {Set: true, Kind: Indexed, List: tc.args},
			}}
			for _, src := range []string{
				`echo ${x+"$@"''}`,
				`echo ${x+''"$@"}`,
				`echo ${x+''"$@"''}`,
			} {
				word := parseCallArg(t, src, 1)
				got, err := Fields(cfg, word)
				if err != nil {
					t.Fatalf("%s: did not want error, got %v", src, err)
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Fatalf("%s: wanted %q, got %q", src, tc.want, got)
				}
			}
		})
	}
}

func TestFieldsParamExpAlternateQuotedEmptyAfterSplit(t *testing.T) {
	cfg := &Config{Env: testEnv{
		"x": {Set: true, Kind: String, Str: "x"},
		"e": {Set: true, Kind: String, Str: ""},
	}}
	cfg.CmdSubst = func(io.Writer, *syntax.CmdSubst) error { return nil }
	for _, src := range []string{
		`echo ${x:+"$e""$e" ""}`,
		`echo ${x:+"$(:)""$(:)" ""}`,
	} {
		word := parseCallArg(t, src, 1)
		got, err := Fields(cfg, word)
		if err != nil {
			t.Fatalf("%s: did not want error, got %v", src, err)
		}
		want := []string{"", ""}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: wanted %q, got %q", src, want, got)
		}
	}
}

func TestParamSliceNegativeLength(t *testing.T) {
	env := testEnv{
		"#": {Set: true, Kind: String, Str: "1"},
		"@": {Set: true, Kind: Indexed, List: []string{"a"}},
		"a": {Set: true, Kind: Indexed, List: []string{"x", "y", "z"}},
		"v": {Set: true, Kind: String, Str: "hello"},
	}
	cfg := &Config{Env: env}
	tests := []struct {
		src     string
		want    []string
		wantErr string
	}{
		{`${@:1:$(($# - 2))}`, nil, "$(($# - 2)): substring expression < 0"},
		{`${a[@]:0:-2}`, nil, "a: -2: substring expression < 0"},
		{`${v:1:-2}`, []string{"ell"}, ""},
		{`${v: -3:2}`, []string{"ll"}, ""},
		{`${@:2:2}`, []string{"b", "c"}, ""},
	}
	for _, tc := range tests {
		if tc.src == `${@:1:$(($# - 2))}` {
			env["@"] = Variable{Set: true, Kind: Indexed, List: []string{"a"}}
		} else {
			env["@"] = Variable{Set: true, Kind: Indexed, List: []string{"a", "b", "c"}}
		}
		word := parseWord(t, tc.src)
		got, err := Fields(cfg, word)
		if tc.wantErr != "" {
			if err == nil {
				t.Fatalf("%s: wanted error %q, got nil", tc.src, tc.wantErr)
			}
			if err.Error() != tc.wantErr {
				t.Fatalf("%s: wanted error %q, got %q", tc.src, tc.wantErr, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: did not want error, got %v", tc.src, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: wanted %q, got %q", tc.src, tc.want, got)
		}
	}
}

type testEnv map[string]Variable

func (e testEnv) Get(name string) Variable {
	return e[name]
}

func (e testEnv) Each(fn func(string, Variable) bool) {
	for name, vr := range e {
		if !fn(name, vr) {
			return
		}
	}
}

func (e testEnv) Set(name string, vr Variable) error {
	e[name] = vr
	return nil
}

func Test_glob(t *testing.T) {
	cfg := &Config{
		ReadDir2: func(string) ([]fs.DirEntry, error) {
			return []fs.DirEntry{
				// The filenames here are sorted, just like [io/fs.ReadDirFS].
				&mockFileInfo{name: "A"},
				&mockFileInfo{name: "AB"},
				&mockFileInfo{name: "a"},
				&mockFileInfo{name: "ab"},
			}, nil
		},
	}

	tests := []struct {
		noCaseGlob bool
		pat        string
		want       []string
	}{
		{false, "a*", []string{"a", "ab"}},
		{false, "A*", []string{"A", "AB"}},
		{false, "*b", []string{"ab"}},
		{false, "b*", nil},
		{true, "a*", []string{"A", "AB", "a", "ab"}},
		{true, "A*", []string{"A", "AB", "a", "ab"}},
		{true, "*b", []string{"AB", "ab"}},
		{true, "b*", nil},
	}
	for _, tc := range tests {
		t.Run(tc.pat, func(t *testing.T) {
			cfg.NoCaseGlob = tc.noCaseGlob
			got, err := cfg.glob("/", tc.pat)
			if err != nil {
				t.Fatalf("did not want error, got %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
		})
	}
}

func TestFieldsBackslashEscapedGlobMeta(t *testing.T) {
	cfg := &Config{
		ReadDir2: func(string) ([]fs.DirEntry, error) {
			return []fs.DirEntry{
				&mockFileInfo{name: "a"},
				&mockFileInfo{name: "ab"},
			}, nil
		},
	}
	tests := []struct {
		src  string
		want []string
	}{
		{`\*`, []string{"*"}},
		{`*`, []string{"a", "ab"}},
		{`a\*`, []string{"a*"}},
		{`a*`, []string{"a", "ab"}},
	}
	for _, tc := range tests {
		word := parseWord(t, tc.src)
		got, err := Fields(cfg, word)
		if err != nil {
			t.Fatalf("%s: did not want error, got %v", tc.src, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("%s: wanted %q, got %q", tc.src, tc.want, got)
		}
	}
}

type mockFileInfo struct {
	name        string
	typ         fs.FileMode
	fs.DirEntry // Stub out everything but Name() & Type()
}

var _ fs.DirEntry = (*mockFileInfo)(nil)

func (fi *mockFileInfo) Name() string      { return fi.name }
func (fi *mockFileInfo) Type() fs.FileMode { return fi.typ }

func TestAnchoredEmptyPatternSubst(t *testing.T) {
	tests := []struct {
		env  string
		src  string
		want string
	}{
		{"var=blah", `${var/#/--}`, "--blah"},
		{"var=abc", `${var/#/x}`, "xabc"},
		{"var=", `${var/#/x}`, "x"},
		{"var=abc", `${var/%/x}`, "abcx"},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			cfg := &Config{Env: ListEnviron(tc.env)}
			word := parseWord(t, tc.src)
			got, err := Literal(cfg, word)
			if err != nil {
				t.Fatalf("did not want error, got %v", err)
			}
			if got != tc.want {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
		})
	}
}

func TestGlobalAnchoredPatternSubst(t *testing.T) {
	tests := []struct {
		env  testEnv
		src  string
		want string
	}{
		{
			env: testEnv{
				"var": {Set: true, Kind: Indexed, List: []string{"abcde", "abcdf"}},
			},
			src:  `${var[*]//#abc/foo}`,
			want: "abcde abcdf",
		},
		{
			env: testEnv{
				"var": {Set: true, Kind: Indexed, List: []string{"abcde", "abcdf"}},
			},
			src:  `${var[*]/#abc/foo}`,
			want: "foode foodf",
		},
		{
			env: testEnv{
				"v": {Set: true, Kind: String, Str: "x#abcy"},
			},
			src:  `${v//#abc/Z}`,
			want: "xZy",
		},
	}
	for _, tc := range tests {
		t.Run(tc.src, func(t *testing.T) {
			cfg := &Config{Env: tc.env}
			word := parseWord(t, tc.src)
			got, err := Literal(cfg, word)
			if err != nil {
				t.Fatalf("did not want error, got %v", err)
			}
			if got != tc.want {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
		})
	}
}
