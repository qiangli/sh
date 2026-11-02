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

func TestLetIndexedExpandOnce(t *testing.T) {
	cases := []struct{ name, body, want, token string }{
		{"quoted-space", `shopt -s assoc_expand_once; declare -a item; let 'item[" "]=27'; printf unreachable`, "", `" "`},
		{"quoted-empty", `shopt -s assoc_expand_once; declare -a item; let 'item[""]=27'; printf unreachable`, "", `""`},
		{"quoted-number", `shopt -s assoc_expand_once; declare -a item; let 'item["9"]=27'; printf unreachable`, "", `"9"`},
		{"expanded-command", `shopt -s array_expand_once; declare -a item; key='$(printf BAD >&2; printf 0)'; let item["$key"]+=2; printf unreachable`, "", `$(printf BAD >&2; printf 0)`},
		{"read-command", `shopt -s array_expand_once; declare -a item; key='$(printf BAD >&2; printf 0)'; let "n=item[$key]"; printf unreachable`, "", `$(printf BAD >&2; printf 0)`},
		{"parameter-literal", `shopt -s array_expand_once; declare -a item; key=0; let 'item[$key]+=2'; printf unreachable`, "", `$key`},
		{"short-circuit", `shopt -s array_expand_once; declare -a item; let '0 && item[$(printf BAD >&2; printf 0)]++'; printf '%s:%s' "$?" "${#item[@]}"`, "1:0", ""},
		{"resume-without-mutation", "shopt -s array_expand_once; declare -a item=([0]=11); let 'item[\" \"]=27'; printf unreachable\nprintf '%s:%s' \"${item[0]}\" \"$?\"", "11:1", `" "`},
		{"backtick-command", "shopt -s array_expand_once; declare -a item; key='`printf BAD >&2; printf 0`'; let \"item[$key]+=2\"; printf unreachable", "", "`printf BAD >&2; printf 0`"},
		{"associative-then-indexed", `shopt -s assoc_expand_once; declare -A map; declare -a item; let 'map[long raw key]=3,item[" "]=2'; printf unreachable`, "", `" "`},
		{"conditional-unreached", `shopt -s array_expand_once; declare -a item; let 'n=1 ? 7 : item[$(printf BAD >&2; printf 0)]'; printf '%s' "$n"`, "7", ""},
		{"array-enable-assoc-disable", `shopt -s array_expand_once; shopt -u assoc_expand_once; declare -a item; let 'item["9"]=27'; printf '%s' "${item[9]}"`, "27", ""},
		{"assoc-enable-array-disable", `shopt -s assoc_expand_once; shopt -u array_expand_once; declare -a item; let 'item["9"]=27'; printf '%s' "${item[9]}"`, "27", ""},
		{"array-disable-assoc-enable", `shopt -u array_expand_once; shopt -s assoc_expand_once; declare -a item; let 'item["9"]=27'`, "", `"9"`},
		{"assoc-disable-array-enable", `shopt -u assoc_expand_once; shopt -s array_expand_once; declare -a item; let 'item["9"]=27'`, "", `"9"`},
		{"alias-listing", `shopt -s array_expand_once; shopt -p array_expand_once assoc_expand_once; [[ :$BASHOPTS: == *:array_expand_once:* && :$BASHOPTS: == *:assoc_expand_once:* ]] && printf both`, "shopt -s array_expand_once\nshopt -s assoc_expand_once\nboth", ""},
		{"recursive-value", `shopt -s array_expand_once; declare -a item; value='item[" "]'; let 'n=value'; printf unreachable`, "", `" "`},
		{"ordinary-index", `shopt -s array_expand_once; declare -a item; let 'item[2+3]=7'; printf '%s' "${item[5]}"`, "7", ""},
		{"option-off", `shopt -u assoc_expand_once array_expand_once; declare -a item; let 'item["9"]=27'; printf '%s' "${item[9]}"`, "27", ""},
		{"associative-quotes", `shopt -s assoc_expand_once; declare -A item; let 'item[" "]=27' 'item[""]=29'; printf '%s:%s' "${item['" "']}" "${item['""']}"`, "27:29", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "case.sh")
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			f, err := syntax.NewParser().Parse(strings.NewReader(tc.body), path)
			if err != nil {
				t.Fatal(err)
			}
			var out, errs bytes.Buffer
			r, err := New(Dir(dir), StdIO(nil, &out, &errs), WithBashCompatErrors(true), WithBashSource([]byte(tc.body)))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			runErr := r.Run(ctx, f)
			if runErr != nil {
				if status, ok := IsExitStatus(runErr); !ok || status != 1 {
					t.Fatalf("want exit status 1, got %v", runErr)
				}
			}
			wantErr := ""
			if tc.token != "" {
				wantErr = path + ": line 1: " + tc.token + ": arithmetic syntax error: operand expected (error token is \"" + tc.token + "\")\n"
			}
			if (runErr != nil) != (tc.token != "" && tc.want == "") || out.String() != tc.want || errs.String() != wantErr {
				t.Fatalf("error=%v out=%q want=%q stderr=%q want=%q", runErr, out.String(), tc.want, errs.String(), wantErr)
			}
			if ref := os.Getenv("SH_TEST_BASH_REFERENCE"); ref != "" {
				cmd := exec.CommandContext(ctx, ref, path)
				cmd.Dir = dir
				var ro, re bytes.Buffer
				cmd.Stdout = &ro
				cmd.Stderr = &re
				refErr := cmd.Run()
				if (runErr == nil) != (refErr == nil) || out.String() != ro.String() || errs.String() != re.String() {
					t.Fatalf("reference error=%v out=%q stderr=%q", refErr, ro.String(), re.String())
				}
			}
		})
	}
}
