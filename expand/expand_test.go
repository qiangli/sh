// Copyright (c) 2018, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package expand

import (
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

type mockFileInfo struct {
	name        string
	typ         fs.FileMode
	fs.DirEntry // Stub out everything but Name() & Type()
}

var _ fs.DirEntry = (*mockFileInfo)(nil)

func (fi *mockFileInfo) Name() string      { return fi.name }
func (fi *mockFileInfo) Type() fs.FileMode { return fi.typ }
