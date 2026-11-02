package interp

import (
	"bytes"
	"context"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestEvalDeclarationAliases(t *testing.T) {
	reference, err := sourceAliasBashReference()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, script string }{
		{"export-handler", `handler() { printf 'handled:%s\n' "$1"; }; alias export=handler
eval 'export VALUE="two words"'
printf 'value:%s\n' "${VALUE-unset}"`},
		{"readonly-handler", `handler() { printf 'handled:%s\n' "$1"; }; alias readonly=handler
eval 'readonly VALUE=yes'
printf 'value:%s\n' "${VALUE-unset}"`},
		{"quoted", `handler() { printf bad; }; alias export=handler
eval '"export" VALUE=yes'
printf '%s\n' "$VALUE"`},
		{"escaped", `handler() { printf bad; }; alias export=handler
eval '\export VALUE=yes'
printf '%s\n' "$VALUE"`},
		{"command-bypass", `handler() { printf bad; }; alias export=handler
eval 'command export VALUE=yes'
printf '%s\n' "$VALUE"`},
		{"builtin-bypass", `handler() { printf bad; }; alias export=handler
eval 'builtin export VALUE=yes'
printf '%s\n' "$VALUE"`},
		{"self-recursive", `alias export=export
eval 'export VALUE=yes'
printf '%s\n' "$VALUE"`},
		{"chain", `handler() { printf 'handled:%s\n' "$1"; }; alias export=next; alias next=handler
eval 'export VALUE=yes'`},
		{"declaration-cycle", `alias export=readonly; alias readonly=export
eval 'export VALUE=yes'
printf '%s\n' "$VALUE"`},
		{"local-handler", `handler() { printf 'handled:%s\n' "$1"; }; alias local=handler
f() { eval 'local VALUE=yes'; printf 'value:%s\n' "${VALUE-unset}"; }; f`},
		{"declaration-chain", `handler() { printf 'handled:%s\n' "$1"; }; alias export=readonly; alias readonly=handler
eval 'export VALUE=yes'`},
		{"heredoc-data", `handler() { printf bad; }; alias export=handler
eval 'cat <<"END"
export VALUE=data
END'`},
		{"argument-data", `handler() { printf bad; }; alias export=handler
eval 'printf "%s\n" export'`},
		{"definition-in-read-unit", `handler() { printf bad; }
eval 'alias export=handler; export VALUE=yes'
printf '%s\n' "$VALUE"`},
		{"removal-in-read-unit", `handler() { printf 'handled:%s\n' "$1"; }; alias export=handler
eval 'unalias export; export VALUE=yes'`},
		{"disabled", `handler() { printf bad; }; alias export=handler
shopt -u expand_aliases
eval 'export VALUE=yes'
printf '%s\n' "$VALUE"`},
		{"posix-export-listing", `set -o posix
export S557_VALUE='first
second'
listing=$(command export -p)
handler() { case $1 in S557_VALUE=*) seen=$1;; esac; }
alias export=handler
eval "$listing"
printf '<%s>\n' "$seen"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			script := "shopt -s expand_aliases\n" + tc.script
			env := []string{"PATH=" + os.Getenv("PATH")}
			cmd := exec.CommandContext(ctx, reference, "-c", script)
			cmd.Env = env
			var want, referenceErr bytes.Buffer
			cmd.Stdout = &want
			cmd.Stderr = &referenceErr
			if err = cmd.Run(); err != nil {
				t.Fatalf("GNU: %v %s", err, &referenceErr)
			}
			node, err := syntax.NewParser().Parse(strings.NewReader(script), "eval-alias")
			if err != nil {
				t.Fatal(err)
			}
			var got, stderr bytes.Buffer
			r, err := New(Env(expand.ListEnviron(env...)), StdIO(nil, &got, &stderr))
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Run(ctx, node); err != nil {
				t.Fatalf("run: %v stderr=%s", err, &stderr)
			}
			if got.String() != want.String() {
				t.Fatalf("got %q want %q stderr=%q", got.String(), want.String(), stderr.String())
			}
		})
	}
}
