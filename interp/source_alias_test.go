package interp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

// sourceAliasBashReference fails closed rather than accepting macOS's Bash
// 3.2 or omitting the differential half of the semantic assertions.
func sourceAliasBashReference() (string, error) {
	candidates := []string{"/opt/homebrew/bin/bash", "/usr/local/bin/bash", "bash"}
	if explicit := os.Getenv("SH_TEST_BASH_REFERENCE"); explicit != "" {
		candidates = []string{explicit}
	}
	for _, candidate := range candidates {
		version, err := exec.Command(candidate, "--version").Output()
		if err == nil && sourceAliasBash53Version(string(version)) {
			path, err := exec.LookPath(candidate)
			if err != nil {
				return "", err
			}
			return filepath.Abs(path)
		}
	}
	return "", fmt.Errorf("GNU Bash 5.3 reference required; run scripts/prepare-bash-reference.sh and set SH_TEST_BASH_REFERENCE")
}

func sourceAliasBash53Version(version string) bool {
	return strings.HasPrefix(version, "GNU bash, version 5.3.") && !strings.Contains(version, "bashy")
}

func TestSourceAliasReferenceIdentity(t *testing.T) {
	for _, tc := range []struct {
		version string
		want    bool
	}{
		{"GNU bash, version 5.3.0(1)-release", true},
		{"GNU bash, version 5.3.15(1)-release", true},
		{"GNU bash, version 3.2.57(1)-release", false},
		{"GNU bash, version 5.2.37(1)-release", false},
		{"GNU bash, version 5.3.0(1)-bashy", false},
		{"other shell 5.3.0", false},
	} {
		if got := sourceAliasBash53Version(tc.version); got != tc.want {
			t.Errorf("version %q accepted=%v; want %v", tc.version, got, tc.want)
		}
	}
	t.Setenv("SH_TEST_BASH_REFERENCE", filepath.Join(t.TempDir(), "missing-reference"))
	if _, err := sourceAliasBashReference(); err == nil {
		t.Fatal("invalid explicit oracle fell back to another binary")
	}
}

func TestSourceGrammarAliases(t *testing.T) {
	bash, err := sourceAliasBashReference()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, setup, body, want string }{
		{"paired-braces", "alias OPEN='{'; alias CLOSE='}'", "if true; then OPEN printf yes; CLOSE; else printf bad; fi", "yes"},
		{"paired-split-braces", "alias OPEN='{ {'; alias NEXT='}; {'; alias CLOSE='}; }'", "if true; then OPEN :; NEXT printf split; CLOSE; else printf bad; fi", "split"},
		{"paired-loop", "alias LOOP='while'; alias DO='do'; alias DONE='done'", "LOOP true; DO printf loop; break; DONE", "loop"},
		{"paired-conditional", "alias IF='if'; alias THEN='then'; alias ELSE='else'; alias FI='fi'", "IF false; THEN printf bad; ELSE printf conditional; FI", "conditional"},
		{"closing-chain", "alias OPEN='{'; alias FINISH=CLOSE; alias CLOSE='}'", "OPEN printf chain; FINISH", "chain"},
		{"closing-alias-literals", "CLOSE() { printf literal; }; alias CLOSE='}'", "'CLOSE'; \"CLOSE\"; \\CLOSE; printf '%s' CLOSE; value=CLOSE; printf '%s' \"$value\"", "literalliteralliteralCLOSECLOSE"},
		{"closing-alias-heredoc", "alias OPEN='{'; alias CLOSE='}'", "cat <<'END'\nCLOSE\nEND\nOPEN printf ok; CLOSE # CLOSE\n", "CLOSE\nok"},
		{"paired-function-body", "alias LOCAL='{ {'; alias BEGIN='}; worker() {'; alias END='}; worker; }'", "if true; then LOCAL :; BEGIN printf worker; END; else printf bad; fi", "worker"},
		{"conditional-elif", "alias IF='if'; alias THEN='then'; alias ELIF='elif'; alias FI='fi'", "IF false; THEN printf bad; ELIF true; THEN printf elif; FI", "elif"},
		{"loop-function", "alias forever='while :;'", "f() { forever do printf loop; break; done; }; f", "loop"},
		{"nested-loop", "alias forever='while :;'", "f() { for x in a; do forever do printf '%s' \"$x\"; break; done; done; }; f", "a"},
		{"negation", "alias not='! '", "not false; printf '%s' \"$?\"; if not true; then printf bad; else printf good; fi", "0good"},
		{"pipeline", "alias not='! '", "not false | cat; printf '%s' \"$?\"", "1"},
		{"self-reference", "f() { printf literal; }; alias f='! f'", "f; printf '%s' \"$?\"", "literal1"},
		{"indirect-self-reference", "a() { printf literal; }; alias a='! b'; alias b='! a'", "a; printf '%s' \"$?\"", "literal0"},
		{"comsub-negation", "alias not='! '", "x=$(not false; printf '%s' \"$?\"); printf '[%s]' \"$x\"", "[0]"},
		{"comsub-command", "alias say='printf %s'", "x=$(say ok); printf '[%s]' \"$x\"", "[ok]"},
		{"many-independent", "alias not='! '", strings.Repeat("not false;\n", 1030) + "printf done", "done"},
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
			{
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
	// Case terminators are not command-word alias positions in GNU Bash.
	r.alias["ENDCASE"] = alias{text: "esac"}
	if _, err := r.parseSourceAliases(syntax.NewParser(), []byte("case x in x) :;; ENDCASE"), "module.sh"); err == nil {
		t.Fatal("accepted aliased case terminator rejected by GNU Bash")
	}
	r.alias["a"], r.alias["b"] = alias{text: "b"}, alias{text: "a"}
	if _, ok := r.sourceGrammarAlias("a", make(map[string]bool)); ok {
		t.Fatal("cyclic aliases resolved as grammar")
	}
}
