package interp

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func TestSourceGrammarAliases(t *testing.T) {
	bash := ""
	for _, candidate := range []string{"/opt/homebrew/bin/bash", "bash"} {
		version, err := exec.Command(candidate, "--version").Output()
		if err == nil && strings.Contains(string(version), "GNU bash, version") && !strings.Contains(string(version), "bashy") {
			bash = candidate
			break
		}
	}
	cases := []struct{ name, setup, body, want string }{
		{"loop-function", "alias forever='while :;'", "f() { forever do printf loop; break; done; }; f", "loop"},
		{"nested-loop", "alias forever='while :;'", "f() { for x in a; do forever do printf '%s' \"$x\"; break; done; done; }; f", "a"},
		{"negation", "alias not='! '", "not false; printf '%s' \"$?\"; if not true; then printf bad; else printf good; fi", "0good"},
		{"pipeline", "alias not='! '", "not false | cat; printf '%s' \"$?\"", "1"},
		{"self-reference", "f() { printf literal; }; alias f='! f'", "f; printf '%s' \"$?\"", "literal1"},
		{"chain", "alias again=forever; alias forever='while :;'", "f() { again do printf chain; break; done; }; f", "chain"},
		{"quoted-escaped", "forever() { printf literal; }; alias forever='while :;'", "'forever'; \\forever; printf '%s' forever", "literalliteralforever"},
		{"comment", "alias forever='while :;'", "# forever do\nprintf '%s' ok # forever do\n", "ok"},
		{"heredoc", "alias forever='while :;'", "cat <<'END'\nforever do\ndone\nEND\nforever do printf ok; break; done", "forever do\ndone\nok"},

		{"same-outer-line", "alias forever='while :;'", "forever do printf fresh; break; done", "fresh"},
		{"argument", "alias not='! '", "printf '%s' not; x=not; printf '%s' \"$x\"", "notnot"},
		{"disabled", "shopt -u expand_aliases; alias not='! '", "not() { printf literal; }; not", "literal"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "module.sh"), []byte(tc.body+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			src := "shopt -s expand_aliases\n" + tc.setup + "; source ./module.sh\n"
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if bash != "" {
				cmd := exec.CommandContext(ctx, bash, "--noprofile", "--norc", "-c", src)
				cmd.Dir = dir
				var oracleOut, oracleErr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &oracleOut, &oracleErr
				if err := cmd.Run(); err != nil || oracleOut.String() != tc.want || oracleErr.Len() != 0 {
					t.Fatalf("GNU Bash: out=%q stderr=%q err=%v; want %q", oracleOut.String(), oracleErr.String(), err, tc.want)
				}
			}
			tree, err := syntax.NewParser().Parse(strings.NewReader(src), "test.sh")
			if err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			r, err := New(Dir(dir), StdIO(nil, &out, &stderr))
			if err != nil {
				t.Fatal(err)
			}
			runErr := r.Run(ctx, tree)
			if runErr != nil || out.String() != tc.want || stderr.Len() != 0 {
				t.Fatalf("got out=%q stderr=%q err=%v; Bash out=%q stderr=%q", out.String(), stderr.String(), runErr, tc.want, "")
			}
		})
	}
}

func TestSourceGrammarAliasParseGuards(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatal(err)
	}
	r.opts[optExpandAliases] = true
	r.alias = map[string]alias{"forever": {text: "while :;"}}
	// GNU Bash rejects this grammar inside a command substitution while finding
	// its closing delimiter. Source alias reparsing must not broaden that grammar.
	if _, err := r.parseSourceAliases(syntax.NewParser(), []byte("x=$(forever do :; done)"), "module.sh"); err == nil {
		t.Fatal("accepted unsupported command-substitution grammar")
	}
	// Source still parses a complete read unit before execution. Never execute
	// alias definitions early merely to recover a later grammar error.
	if _, err := r.parseSourceAliases(syntax.NewParser(), []byte("alias newly='while :;'\nnewly do :; done"), "module.sh"); err == nil {
		t.Fatal("unexpectedly executed an in-source alias definition during parsing")
	}
	if _, exists := r.alias["newly"]; exists {
		t.Fatal("parsing mutated aliases")
	}
	r.alias["a"], r.alias["b"] = alias{text: "b"}, alias{text: "a"}
	if _, ok := r.sourceGrammarAlias("a", make(map[string]bool)); ok {
		t.Fatal("cyclic aliases resolved as grammar")
	}
}
