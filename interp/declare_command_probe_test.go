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

func TestDeclareCommandProbe(t *testing.T) {
	cases := []struct{ name, src, want, diag string }{
		{"keyword-long", "probe=before; export probe; typeset --global --unexport probe=ok; printf \"status:%s\\n\" \"$?\"; declare -p probe; unset probe; printf \"unset:%s\\n\" \"$?\"", "status:2\ndeclare -x probe=\"before\"\nunset:0\n", "$FILE: line 1: typeset: --: invalid option\ntypeset: usage: typeset [-aAfFgiIlnrtux] name[=value] ... or typeset -p [-aAfFilnrtux] [name ...]\n"},
		{"command-long", "probe=before; export probe; command typeset --global --unexport probe=ok; printf \"status:%s\\n\" \"$?\"; declare -p probe; unset probe; printf \"unset:%s\\n\" \"$?\"", "status:2\ndeclare -x probe=\"before\"\nunset:0\n", "$FILE: line 1: typeset: --: invalid option\ntypeset: usage: typeset [-aAfFgiIlnrtux] name[=value] ... or typeset -p [-aAfFilnrtux] [name ...]\n"},
		{"command-unexport", "probe=before; export probe; command typeset -g +x probe=ok; printf \"status:%s\\n\" \"$?\"; declare -p probe; unset probe; printf \"unset:%s\\n\" \"$?\"", "status:0\ndeclare -- probe=\"ok\"\nunset:0\n", ""},
		{"separator", "probe=before; export probe; command typeset -- probe=ok; printf \"status:%s\\n\" \"$?\"; declare -p probe; unset probe; printf \"unset:%s\\n\" \"$?\"", "status:0\ndeclare -x probe=\"ok\"\nunset:0\n", ""},
		{"invalid-short", "probe=before; export probe; command typeset -Z probe=ok; printf \"status:%s\\n\" \"$?\"; declare -p probe; unset probe; printf \"unset:%s\\n\" \"$?\"", "status:2\ndeclare -x probe=\"before\"\nunset:0\n", "$FILE: line 1: typeset: -Z: invalid option\ntypeset: usage: typeset [-aAfFgiIlnrtux] name[=value] ... or typeset -p [-aAfFilnrtux] [name ...]\n"},
		{"global-scope", "item=outer; f() { local item=inner; command typeset -g +x item=updated; printf 'local:%s\\n' \"$item\"; }; f; printf 'global:%s\\n' \"$item\"", "local:inner\nglobal:updated\n", ""},
		{"local-scope", "item=outer; f() { command typeset item=inner; printf 'local:%s\\n' \"$item\"; }; f; printf 'global:%s\\n' \"$item\"", "local:inner\nglobal:outer\n", ""},
		{"readonly-clear", "readonly item=kept; export item; command typeset +x item; printf 'clear:%s\\n' \"$?\"; declare -p item; command typeset item=changed; printf 'assign:%s:%s\\n' \"$?\" \"$item\"", "clear:0\ndeclare -r item=\"kept\"\nassign:1:kept\n", "$FILE: line 1: typeset: item: readonly variable\n"},
		{"literal-value", "command typeset -g +x 'item=$HOME * ~ $(printf BAD)'; printf '%s\\n' \"$item\"", "$HOME * ~ $(printf BAD)\n", ""},
		{"fallback-probe", "probe=no; if command typeset --global --unexport probe=ok 2>/dev/null; then printf wrong; elif command typeset -g +x probe=ok; then printf fallback; fi; unset probe; printf ':%s\\n' \"$?\"", "fallback:0\n", ""},
		{"attribute-clear-order", "command typeset -x +x item=1; declare -p item; command typeset +x -x item=2; declare -p item", "declare -- item=\"1\"\ndeclare -- item=\"2\"\n", ""},
		{"separator-invalid-name", "command typeset -- -r; printf 'status:%s\\n' \"$?\"", "status:1\n", "$FILE: line 1: typeset: `-r': not a valid identifier\n"},
	}
	cases = append(cases, []struct{ name, src, want, diag string }{
		{"option-after-operand", "command typeset item=first -x other=second; printf 'status:%s\\n' \"$?\"; declare -p item other", "status:1\ndeclare -- item=\"first\"\ndeclare -- other=\"second\"\n", "$FILE: line 1: typeset: `-x': not a valid identifier\n"},
		{"equals-value", "command typeset -g +x 'item=a=b=c'; printf '%s\\n' \"$item\"", "a=b=c\n", ""},
		{"global-readonly-shadow", "readonly item=global; f() { local other=local; command typeset -g item=changed; printf 'status:%s\\n' \"$?\"; }; f; printf '%s\\n' \"$item\"", "status:1\nglobal\n", "$FILE: line 1: typeset: item: readonly variable\n"},
		{"local-readonly-shadow", "item=global; f() { local -r item=local; command typeset -g item=changed; printf 'local:%s:%s\\n' \"$?\" \"$item\"; }; f; printf 'global:%s\\n' \"$item\"", "local:0:local\nglobal:changed\n", ""},
		{"nameref-existing-target", "target=before; link=target; command typeset -n link; link=after; printf '%s:%s\\n' \"$target\" \"$link\"", "after:after\n", ""},
		{"inline-export", "temporary=value command typeset -x item=ready; printf '%s:%s\\n' \"$item\" \"${temporary-unset}\"; declare -p item", "ready:unset\ndeclare -x item=\"ready\"\n", ""},
	}...)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "case.sh")
			if err := os.WriteFile(path, []byte(tc.src), 0600); err != nil {
				t.Fatal(err)
			}
			f, err := syntax.NewParser().Parse(strings.NewReader(tc.src), path)
			if err != nil {
				t.Fatal(err)
			}
			var out, errs bytes.Buffer
			r, err := New(Dir(dir), StdIO(nil, &out, &errs), WithBashCompatErrors(true), WithBashSource([]byte(tc.src)))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			runErr := r.Run(ctx, f)
			wantErr := strings.ReplaceAll(tc.diag, "$FILE", path)
			if runErr != nil || out.String() != tc.want || errs.String() != wantErr {
				t.Fatalf("error=%v out=%q want=%q stderr=%q want=%q", runErr, out.String(), tc.want, errs.String(), wantErr)
			}
			if ref := os.Getenv("SH_TEST_BASH_REFERENCE"); ref != "" {
				cmd := exec.CommandContext(ctx, ref, path)
				cmd.Dir = dir
				var ro, re bytes.Buffer
				cmd.Stdout = &ro
				cmd.Stderr = &re
				refErr := cmd.Run()
				if refErr != nil || out.String() != ro.String() || errs.String() != re.String() {
					t.Fatalf("reference err=%v out=%q stderr=%q", refErr, ro.String(), re.String())
				}
			}
		})
	}
}
