package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestNewRunnerIgnoresInheritedOLDPWD(t *testing.T) {
	t.Setenv("OLDPWD", "/tmp")
	r, err := newRunner()
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Env.Get("OLDPWD"); got.IsSet() {
		t.Fatalf("OLDPWD inherited into runner as %q", got.String())
	}
}

func TestCommandSubstOpenBefore(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		pos  syntax.Pos
		want bool
	}{
		{
			name: "inside command substitution",
			src:  "echo $( if x; then echo foo )\n",
			pos:  syntax.NewPos(0, 1, 9),
			want: true,
		},
		{
			name: "after command substitution",
			src:  "echo $(echo ok) if x; then echo foo\n",
			pos:  syntax.NewPos(0, 1, 17),
			want: false,
		},
		{
			name: "plain compound command",
			src:  "if x; then echo foo )\n",
			pos:  syntax.NewPos(0, 1, 1),
			want: false,
		},
		{
			name: "arithmetic expansion",
			src:  "echo $((1 + ))\n",
			pos:  syntax.NewPos(0, 1, 11),
			want: false,
		},
		{
			name: "multiline command substitution",
			src:  "echo $(\nif x; then echo foo\n)\n",
			pos:  syntax.NewPos(0, 2, 1),
			want: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := commandSubstOpenBefore([]byte(test.src), test.pos); got != test.want {
				t.Fatalf("commandSubstOpenBefore() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestPrintBashParseErrorCompoundEOF(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		text string
		want string
	}{
		{
			name: "if non-empty eof line",
			src:  "if\n\ttrue; true\nthen\n\techo foo bar",
			text: "`if` statement must end with `fi`",
			want: "bash: -c: line 5: syntax error: unexpected end of file from `if' command on line 1\n",
		},
		{
			name: "until whitespace eof line",
			src:  "until false\ndo\n\techo false\n\t",
			text: "`until` statement must end with `done`",
			want: "bash: -c: line 4: syntax error: unexpected end of file from `until' command on line 1\n",
		},
		{
			name: "case whitespace eof line",
			src:  "case foo in\nbar)\tif false\n\tthen\n\t\ttrue\n\tfi\n\t;;\n\t",
			text: "`case` statement must end with `esac`",
			want: "bash: -c: line 7: syntax error: unexpected end of file from `case' command on line 1\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			printBashParseError(&buf, []byte(test.src), "bash: -c", syntax.ParseError{
				Pos:  syntax.NewPos(0, 1, 1),
				Text: test.text,
			})
			if got := buf.String(); got != test.want {
				t.Fatalf("printBashParseError mismatch\nwant:\n%q\ngot:\n%q", test.want, got)
			}
		})
	}
}

func TestPrintBashParseErrorArrayCompatibility(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		src  string
		pos  syntax.Pos
		text string
		want string
	}{
		{
			name: "array assignment after command word",
			src:  `printf "%s\n" -a a=(a 'b  c')`,
			pos:  syntax.NewPos(0, 1, 20),
			text: "a command can only contain words and redirects; encountered `(`",
			want: "bash: -c: line 1: syntax error near unexpected token `('\n" +
				"bash: -c: line 1: `printf \"%s\\n\" -a a=(a 'b  c')'\n",
		},
		{
			name: "empty declare name",
			src:  `declare -r []=asdf`,
			pos:  syntax.NewPos(0, 1, 13),
			text: "invalid var name",
			want: "bash: -c: line 1: declare: `[]=asdf': not a valid identifier\n",
		},
		{
			name: "zsh process substitution in declare",
			src:  `declare -a ''=(a 'b c')`,
			pos:  syntax.NewPos(0, 1, 14),
			text: "`=(` process substitutions are a zsh feature",
			want: "bash: -c: line 1: syntax error near unexpected token `('\n" +
				"bash: -c: line 1: `declare -a ''=(a 'b c')'\n",
		},
		{
			name: "array metacharacter pair",
			src:  `metas=( <> < > ! )`,
			pos:  syntax.NewPos(0, 1, 9),
			text: "array element values must be words",
			want: "bash: -c: line 1: syntax error near unexpected token `<>'\n" +
				"bash: -c: line 1: `metas=( <> < > ! )'\n",
		},
		{
			name: "arithmetic bare tilde",
			src:  `echo $(( ~ ))`,
			pos:  syntax.NewPos(0, 1, 10),
			text: "`~` must be followed by an expression",
			want: "bash: -c: line 1: ~ : arithmetic syntax error: operand expected (error token is \"~ \")\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			printBashParseError(&buf, []byte(test.src), "bash: -c", syntax.ParseError{
				Pos:  test.pos,
				Text: test.text,
			})
			if got := buf.String(); got != test.want {
				t.Fatalf("printBashParseError mismatch\nwant:\n%q\ngot:\n%q", test.want, got)
			}
		})
	}
}

func TestDefaultCommandArgv0(t *testing.T) {
	t.Parallel()
	tests := []struct {
		arg0 string
		want string
	}{
		{"/tmp/bin/bash", "bash"},
		{"specialname", "specialname"},
		{"-specialname", "-specialname"},
	}
	for _, test := range tests {
		if got := defaultCommandArgv0(test.arg0); got != test.want {
			t.Fatalf("defaultCommandArgv0(%q) = %q, want %q", test.arg0, got, test.want)
		}
	}
}

func TestStaticAliasExpand(t *testing.T) {
	t.Parallel()
	src := strings.Join([]string{
		"shopt -s expand_aliases",
		"alias switch=case",
		"switch foo in foo) echo ok;; esac",
		"alias echo='echo ordinary'",
		"echo stays-runtime",
		"echo $( switch foo in foo) echo ok;; esac )",
		"alias comsub0='echo $(echo $DATE'",
		"comsub0)",
		"alias math1='echo $( date )'",
		"math1)",
		"alias number='echo 123'",
		"(( $(number) ))",
		"alias DONE='}'",
		"echo ok; DONE)",
		"alias let='let --'",
		"let '1 == 1'",
		"alias al=' '",
		"shopt -s expand_aliases 2>/dev/null",
		"al for x in y",
		"do echo $x",
		"done",
		"${THIS_SH} -c '",
		"shopt -s expand_aliases 2>/dev/null",
		"alias al=\" \"",
		"alias foo=bar",
		"alias for=echo",
		"al for foo in v",
		"do echo foo=$foo bar=$bar",
		"done' bash",
		"${THIS_SH} -o posix -c '",
		"shopt -s expand_aliases 2>/dev/null",
		"alias al=\" \"",
		"alias foo=bar",
		"al for foo in v",
		"do echo foo=$foo bar=$bar",
		"done' bash",
		`alias raw="echo 'Error:"`,
		`raw bar'`,
		"alias comment=#",
		"comment text after",
		`alias pipe='printf "%s\n" \'`,
		"pipe|cat",
		"alias semi=';'",
		"echo a semi echo b",
		"alias in='<'",
		"cat in file",
		"alias 'headplus=cat <<EOF",
		"hello'",
		"headplus",
		"world",
		"EOF",
		"alias head='cat <<END' body='head",
		"here-document",
		"END'",
		"body",
		"unalias -a",
		`raw again'`,
		"",
	}, "\n")
	want := strings.Join([]string{
		"shopt -s expand_aliases",
		"alias switch=case",
		"case foo in foo) echo ok;; esac",
		"alias echo='echo ordinary'",
		"echo stays-runtime",
		"echo $( case foo in foo) echo ok;; esac )",
		"alias comsub0='echo $(echo $DATE'",
		"echo $(echo $DATE)",
		"alias math1='echo $( date )'",
		"math1)",
		"alias number='echo 123'",
		"(( 123 ))",
		"alias DONE='}'",
		"echo ok; })",
		"alias let='let --'",
		"let '1 == 1'",
		"alias al=' '",
		"shopt -s expand_aliases 2>/dev/null",
		"  for x in y",
		"do echo $x",
		"done",
		"${THIS_SH} -c '",
		"shopt -s expand_aliases 2>/dev/null",
		"alias al=\" \"",
		"alias foo=bar",
		"alias for=echo",
		"al for foo in v",
		"do echo foo=$foo bar=$bar",
		"done' bash",
		"${THIS_SH} -o posix -c '",
		"shopt -s expand_aliases 2>/dev/null",
		"alias al=\" \"",
		"alias foo=bar",
		"  for foo in v",
		"do echo foo=$foo bar=$bar",
		"done' bash",
		`alias raw="echo 'Error:"`,
		`echo 'Error: bar'`,
		"alias comment=#",
		"# text after",
		`alias pipe='printf "%s\n" \'`,
		`printf "%s\n" \|cat`,
		"alias semi=';'",
		"echo a ; echo b",
		"alias in='<'",
		"cat < file",
		"alias 'headplus=cat <<EOF",
		"hello'",
		"cat <<EOF",
		"hello",
		"world",
		"EOF",
		"alias head='cat <<END' body='head",
		"here-document",
		"END'",
		"cat <<END",
		"here-document",
		"END",
		"unalias -a",
		`raw again'`,
		"",
	}, "\n")
	if got := string(staticAliasExpand([]byte(src))); got != want {
		t.Fatalf("staticAliasExpand mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestQuoteParamReplBackquotes(t *testing.T) {
	t.Parallel()
	src := []byte("printf '%s\\n' ${qpath//`printf '%s' \"\\\\\\\\\"`/}\n")
	want := "printf '%s\\n' /tmp/foo/bar\n"
	if got := string(quoteParamReplBackquotes(src)); got != want {
		t.Fatalf("quoteParamReplBackquotes mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

func TestRunRetriesPosixAfterParsedPrefix(t *testing.T) {
	t.Parallel()
	src := strings.Join([]string{
		"set -o posix",
		`echo 1 ${IFS+'}'z}`,
		`echo 2 "${IFS+'}'z}"`,
		`echo 3 "foo ${IFS+'bar} baz"`,
		`printf '%s\n' "4 foo ${IFS+"b   c"} baz"`,
		"",
	}, "\n")
	var stdout, stderr bytes.Buffer
	r, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interp.Env(expand.ListEnviron()),
		interp.WithBashCompatErrors(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := run(r, strings.NewReader(src), "posixexp2.sub"); err != nil {
		t.Fatal(err)
	}
	want := "1 }z\n2 ''z}\n3 foo 'bar baz\n4 foo b   c baz\n"
	if got := stdout.String(); got != want {
		t.Fatalf("stdout mismatch\nwant:\n%q\ngot:\n%q\nstderr:\n%s", want, got, stderr.String())
	}
	if stderr.Len() > 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestRunKeepsBashConditionalAfterRuntimePosix(t *testing.T) {
	t.Parallel()
	src := strings.Join([]string{
		"set -o posix",
		"empty=",
		"if [[ $empty -gt 0 ]]; then echo bad; fi",
		"if [[ 1 -gt 0 ]]; then echo ok; fi",
		"",
	}, "\n")
	var stdout, stderr bytes.Buffer
	r, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interp.Env(expand.ListEnviron()),
		interp.WithBashCompatErrors(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := run(r, strings.NewReader(src), "new-exp2.sub"); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "ok\n"; got != want {
		t.Fatalf("stdout mismatch\nwant:\n%q\ngot:\n%q\nstderr:\n%s", want, got, stderr.String())
	}
	if stderr.Len() > 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestRunQuotedEmptyHeredocEOFKeepsBody(t *testing.T) {
	t.Parallel()
	src := "cat <<''\nhi\nthere\n''\n"
	var stdout, stderr bytes.Buffer
	parser := syntax.NewParser(
		syntax.Variant(syntax.LangBash),
		syntax.HeredocEOFWarning(func(startLine, eofLine int, stop string) {
			stderr.WriteString("heredoc.tests: line 4: warning: here-document at line 1 delimited by end-of-file (wanted `')\n")
		}),
	)
	file, err := parser.Parse(strings.NewReader(src), "heredoc.tests")
	if err != nil {
		t.Fatal(err)
	}
	r, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interp.Env(expand.ListEnviron("PATH=/usr/bin:/bin")),
		interp.WithBashCompatErrors(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	runErr := r.Run(context.Background(), file)
	if got, want := stdout.String(), "hi\nthere\n''\n"; got != want {
		t.Fatalf("stdout mismatch\nwant:\n%q\ngot:\n%q\nstderr:\n%s\nrunErr:%v", want, got, stderr.String(), runErr)
	}
	wantErr := "heredoc.tests: line 4: warning: here-document at line 1 delimited by end-of-file (wanted `')\n"
	if got := stderr.String(); got != wantErr {
		t.Fatalf("stderr mismatch\nwant:\n%q\ngot:\n%q\nrunErr:%v", wantErr, got, runErr)
	}
	if runErr != nil {
		t.Fatalf("run error: %v\nstdout:\n%s\nstderr:\n%s", runErr, stdout.String(), stderr.String())
	}
}

func TestRunBadSubstDollarParamRecovery(t *testing.T) {
	src := "set -e\ntrap 'echo $?' EXIT\necho ${$NO_SUCH_VAR}\n"
	var stdout, stderr bytes.Buffer
	r, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interp.Env(expand.ListEnviron()),
		interp.WithBashCompatErrors(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	oldStderr := os.Stderr
	readStderr, writeStderr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writeStderr
	defer func() {
		os.Stderr = oldStderr
		readStderr.Close()
	}()
	err = run(r, strings.NewReader(src), "./errors2.sub")
	writeStderr.Close()
	globalStderr, readErr := io.ReadAll(readStderr)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err == nil {
		t.Fatal("expected recovered parse error")
	}
	if want := "1\n"; stdout.String() != want {
		t.Fatalf("stdout mismatch\nwant:\n%q\ngot:\n%q\nstderr:\n%s", want, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected runner stderr: %s", stderr.String())
	}
	wantErr := "./errors2.sub: line 3: ${$NO_SUCH_VAR}: bad substitution\n"
	if string(globalStderr) != wantErr {
		t.Fatalf("stderr mismatch\nwant:\n%q\ngot:\n%q", wantErr, string(globalStderr))
	}
}

func TestRunArithmeticParseErrorRecoveryContinues(t *testing.T) {
	src := "echo $((a b))\n(( x=9 y=41 ))\necho after\n"
	var stdout, stderr bytes.Buffer
	r, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interp.Env(expand.ListEnviron()),
		interp.WithBashCompatErrors(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	oldStderr := os.Stderr
	readStderr, writeStderr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writeStderr
	defer func() {
		os.Stderr = oldStderr
		readStderr.Close()
	}()
	err = run(r, strings.NewReader(src), "./arith.tests")
	writeStderr.Close()
	globalStderr, readErr := io.ReadAll(readStderr)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err == nil {
		t.Fatal("expected recovered parse error")
	}
	if got, want := stdout.String(), "after\n"; got != want {
		t.Fatalf("stdout mismatch\nwant:\n%q\ngot:\n%q\nstderr:\n%s", want, got, stderr.String())
	}
	wantErr := "./arith.tests: line 1: a b: arithmetic syntax error in expression (error token is \"b\")\n" +
		"./arith.tests: line 2: ((: x=9 y=41 : arithmetic syntax error in expression (error token is \"y=41 \")\n"
	if string(globalStderr) != wantErr {
		t.Fatalf("stderr mismatch\nwant:\n%q\ngot:\n%q", wantErr, string(globalStderr))
	}
}

func TestRunNestedBadSubstRecoveryContinues(t *testing.T) {
	src := "c=\"\"\necho ${c//${$(($#-1))}/x/}\nset -- a b\nprintf '<%s>\\n' \"$@\"\n"
	var stdout, stderr bytes.Buffer
	r, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interp.Env(expand.ListEnviron()),
		interp.WithBashCompatErrors(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	oldStderr := os.Stderr
	readStderr, writeStderr, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writeStderr
	defer func() {
		os.Stderr = oldStderr
		readStderr.Close()
	}()
	err = run(r, strings.NewReader(src), "./new-exp.tests")
	writeStderr.Close()
	globalStderr, readErr := io.ReadAll(readStderr)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err == nil {
		t.Fatal("expected recovered parse error status")
	}
	wantOut := "<a>\n<b>\n"
	if stdout.String() != wantOut {
		t.Fatalf("stdout mismatch\nwant:\n%q\ngot:\n%q\nstderr:\n%s", wantOut, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected runner stderr: %s", stderr.String())
	}
	wantErr := "./new-exp.tests: line 2: ${$(($#-1))}: bad substitution\n"
	if string(globalStderr) != wantErr {
		t.Fatalf("stderr mismatch\nwant:\n%q\ngot:\n%q", wantErr, string(globalStderr))
	}
}

// runForcedInteractiveInput drives runForcedInteractiveExec with raw
// input bytes (including readline control keys) on a pipe replacing
// os.Stdin, returning the runner's stdout.
func runForcedInteractiveInput(t *testing.T, input string) string {
	t.Helper()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		pw.WriteString(input)
		pw.Close()
	}()
	oldStdin := os.Stdin
	os.Stdin = pr
	t.Cleanup(func() { os.Stdin = oldStdin })

	var out bytes.Buffer
	r, err := interp.New(interp.StdIO(nil, &out, io.Discard), interp.Interactive(true))
	if err != nil {
		t.Fatal(err)
	}
	if err := runForcedInteractiveExec(r); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestForcedInteractiveOperateAndGetNext(t *testing.T) {
	// Mirrors the last sections of bash's history4.sub: type a few
	// commands (one multi-line, one HISTIGNOREd), then C-p back three
	// entries and C-o twice. The recalled multi-line entry must rerun
	// whole, and each C-o must execute and fetch the next entry even
	// while HISTSIZE stifling shifts the list.
	t.Setenv("HISTSIZE", "6")
	t.Setenv("HISTFILE", "")
	t.Setenv("HISTIGNORE", "&:history*:fc*")
	out := runForcedInteractiveInput(t,
		"echo 0\necho 1\necho 2\necho \"(left\nmid\nright)\"\necho A\necho B\nhistory -w\n\x10\x10\x10\x0f\x0f\n")
	want := "0\n1\n2\n(left\nmid\nright)\nA\nB\n(left\nmid\nright)\nA\nB\n"
	if out != want {
		t.Errorf("operate-and-get-next:\n got: %q\nwant: %q", out, want)
	}
}

func TestForcedInteractiveReverseSearch(t *testing.T) {
	// Mirrors the first sections of history4.sub: load a HISTFILE whose
	// multi-line entry comes back as separate line entries, C-r search
	// for it, then C-o through the continuation lines and the next
	// entries. Clearing HISTFILE first must prevent the exit-time save
	// from clobbering the file.
	dir := t.TempDir()
	hf := filepath.Join(dir, "histfile")
	content := "echo 0\necho 1\necho 2\necho \"(left\nmid\nright)\"\necho A\necho B\nhistory -w\n"
	if err := os.WriteFile(hf, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HISTSIZE", "8")
	t.Setenv("HISTFILE", hf)
	t.Setenv("HISTIGNORE", "&:history*:fc*")
	out := runForcedInteractiveInput(t, "HISTFILE=\n\x12left\x0f\x0f\x0f\x0f\n")
	want := "(left\nmid\nright)\nA\nB\n"
	if out != want {
		t.Errorf("reverse search:\n got: %q\nwant: %q", out, want)
	}
	data, err := os.ReadFile(hf)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Errorf("history file clobbered at exit:\n got: %q\nwant: %q", string(data), content)
	}
}
