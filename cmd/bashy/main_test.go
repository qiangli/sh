package main

import (
	"bytes"
	"context"
	"io"
	"os"
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
