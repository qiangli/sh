// Copyright (c) 2018, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// Each test has an even number of strings, which form input-output pairs for
// the interactive shell. The input string is fed to the interactive shell, and
// bytes are read from its output until the expected output string is matched or
// an error is encountered.
//
// In other words, each first string is what the user types, and each following
// string is what the shell will print back. Note that the first "$ " output is
// implicit.

var interactiveTests = []struct {
	pairs   []string
	wantErr string
}{
	{},
	{
		pairs: []string{
			"\n",
			"$ ",
			"\n",
			"$ ",
		},
	},
	{
		pairs: []string{
			"echo foo\n",
			"foo\n",
		},
	},
	{
		pairs: []string{
			"echo foo\n",
			"foo\n$ ",
			"echo bar\n",
			"bar\n",
		},
	},
	{
		pairs: []string{
			"if true\n",
			"> ",
			"then echo bar; fi\n",
			"bar\n",
		},
	},
	{
		pairs: []string{
			"echo 'foo\n",
			"> ",
			"bar'\n",
			"foo\nbar\n",
		},
	},
	{
		pairs: []string{
			"echo foo; echo bar\n",
			"foo\nbar\n",
		},
	},
	{
		pairs: []string{
			"echo foo; echo 'bar\n",
			"> ",
			"baz'\n",
			"foo\nbar\nbaz\n",
		},
	},
	{
		pairs: []string{
			"(\n",
			"> ",
			"echo foo)\n",
			"foo\n",
		},
	},
	{
		pairs: []string{
			"[[\n",
			"> ",
			"true ]]\n",
			"$ ",
		},
	},
	{
		pairs: []string{
			"echo foo ||\n",
			"> ",
			"echo bar\n",
			"foo\n",
		},
	},
	{
		// `echo foo | read var; echo $var`: without `shopt -s
		// lastpipe`, bash runs the last command of a pipeline in
		// a subshell, so the assignment to `var` is lost. The
		// `echo $var` after the semicolon therefore prints
		// nothing — just a bare newline.
		pairs: []string{
			"echo foo |\n",
			"> ",
			"read var; echo $var\n",
			"\n",
		},
	},
	{
		pairs: []string{
			"echo foo",
			"",
			" bar\n",
			"foo bar\n",
		},
	},
	{
		pairs: []string{
			"echo\\\n",
			"> ",
			" foo\n",
			"foo\n",
		},
	},
	{
		pairs: []string{
			"echo foo\\\n",
			"> ",
			"bar\n",
			"foobar\n",
		},
	},
	{
		pairs: []string{
			"echo 你好\n",
			"你好\n$ ",
		},
	},
	{
		pairs: []string{
			"echo *; :\n",
			"dbracket_test.go main.go main_test.go\n$ ",
			"echo *\n",
			"dbracket_test.go main.go main_test.go\n$ ",
			"shopt -s globstar; echo **\n",
			"dbracket_test.go main.go main_test.go\n$ ",
		},
	},
	{
		pairs: []string{
			"echo foo; exit 0; echo bar\n",
			"foo\n",
			"echo baz\n",
			"",
		},
	},
	{
		pairs: []string{
			"echo foo; exit 1; echo bar\n",
			"foo\n",
			"echo baz\n",
			"",
		},
		wantErr: "exit status 1",
	},
	{
		pairs: []string{
			"(x\n",
			"> ",
		},
		wantErr: "1:1: unexpected EOF while looking for matching `)'",
	},
	{
		pairs: []string{
			"gosh_alias arg || true\n",
			"\"gosh_alias\": executable file not found in $PATH\n$ ",
			"alias gosh_alias=echo\n",
			"$ ",
			"gosh_alias arg || true\n",
			"arg\n$ ",
			"unalias gosh_alias\n",
			"$ ",
			"gosh_alias arg || true\n",
			"\"gosh_alias\": executable file not found in $PATH\n$ ",
		},
	},
}

func TestInteractive(t *testing.T) {
	t.Parallel()
	for _, tc := range interactiveTests {
		t.Run("", func(t *testing.T) {
			inReader, inWriter, err := os.Pipe()
			qt.Assert(t, qt.IsNil(err))
			outReader, outWriter, err := os.Pipe()
			qt.Assert(t, qt.IsNil(err))
			runner, _ := interp.New(interp.Interactive(true), interp.StdIO(inReader, outWriter, outWriter))
			errc := make(chan error, 1)
			go func() {
				errc <- runInteractive(runner, inReader, outWriter, outWriter)
				// Discard the rest of the input.
				io.Copy(io.Discard, inReader)
				inReader.Close()
				outWriter.Close()
			}()

			if err := readString(outReader, "$ "); err != nil {
				t.Fatal(err)
			}

			line := 1
			for len(tc.pairs) > 0 {
				t.Logf("write %q", tc.pairs[0])
				if _, err := io.WriteString(inWriter, tc.pairs[0]); err != nil {
					t.Fatal(err)
				}
				t.Logf("read %q", tc.pairs[1])
				if err := readString(outReader, tc.pairs[1]); err != nil {
					t.Fatal(err)
				}

				line++
				tc.pairs = tc.pairs[2:]
			}

			// Close the input pipe, so that the parser can stop.
			inWriter.Close()

			// Once the input pipe is closed, close the output pipe
			// so that any remaining prompt writes get discarded.
			outReader.Close()

			err = <-errc
			if err != nil && tc.wantErr == "" {
				t.Fatalf("unexpected error: %v", err)
			} else if tc.wantErr != "" && fmt.Sprint(err) != tc.wantErr {
				t.Fatalf("want error %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestBashScriptParseError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		src  string
		want string
	}{
		{
			src:  "case\nin esac\n",
			want: "./s: line 1: syntax error near unexpected token `newline'\n./s: line 1: `case'",
		},
		{
			src:  "$(echo f)$(echo or) i in a b c; do echo $i; done\n",
			want: "./s: line 1: syntax error near unexpected token `do'\n./s: line 1: `$(echo f)$(echo or) i in a b c; do echo $i; done'",
		},
		{
			src:  "echo -n $'\\c'' | show_bytes\n",
			want: "./s: line 1: unexpected EOF while looking for matching `''",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run("", func(t *testing.T) {
			t.Parallel()
			_, err := syntax.NewParser().Parse(strings.NewReader(tt.src), "./s")
			if err == nil {
				t.Fatal("expected parse error")
			}
			got, ok := bashScriptParseError(err, []byte(tt.src), "./s")
			if !ok {
				t.Fatalf("error was not reformatted: %v", err)
			}
			if got != tt.want {
				t.Fatalf("want %q, got %q", tt.want, got)
			}
		})
	}
}

func TestBashScriptParseErrorWithHeredocWarning(t *testing.T) {
	t.Parallel()

	src := "fun() {\n  cat << \"$@\"\nhi\n1\n}\nfun\n"
	var warnings []string
	parser := syntax.NewParser(syntax.HeredocEOFWarning(func(startLine, eofLine int, stop string) {
		warnings = append(warnings, fmt.Sprintf(
			"./s: line %d: warning: here-document at line %d delimited by end-of-file (wanted `%s')",
			eofLine, startLine, stop))
	}))
	_, err := parser.Parse(strings.NewReader(src), "./s")
	if err == nil {
		t.Fatal("expected parse error")
	}
	got, ok := bashScriptParseError(err, []byte(src), "./s")
	if !ok {
		t.Fatalf("error was not reformatted: %v", err)
	}
	got = strings.Join(append(warnings, got), "\n")
	want := "./s: line 6: warning: here-document at line 2 delimited by end-of-file (wanted `$@')\n" +
		"./s: line 7: syntax error: unexpected end of file from `{' command on line 1"
	if got != want {
		t.Fatalf("want %q, got %q", want, got)
	}
}

func TestInteractiveExit(t *testing.T) {
	inReader, inWriter, err := os.Pipe()
	qt.Assert(t, qt.IsNil(err))
	defer inReader.Close()
	go func() {
		io.WriteString(inWriter, "exit\n")
		inWriter.Close()
	}()
	w := io.Discard
	runner, _ := interp.New(interp.Interactive(true), interp.StdIO(inReader, w, w))
	if err := runInteractive(runner, inReader, w, w); err != nil {
		t.Fatal("expected a nil error")
	}
}

// readString will keep reading from a reader until all bytes from the supplied
// string are read.
func readString(r io.Reader, want string) error {
	p := make([]byte, len(want))
	_, err := io.ReadFull(r, p)
	if err != nil {
		return err
	}
	got := string(p)
	if got != want {
		return fmt.Errorf("ReadString: read %q, wanted %q", got, want)
	}
	return nil
}
