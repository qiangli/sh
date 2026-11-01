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

func TestPrefixNamesNounset(t *testing.T) {
	type testCase struct{ name, src, want string }
	var cases []testCase
	for _, op := range []string{"@", "*"} {
		for _, quoted := range []bool{false, true} {
			for _, matches := range []bool{false, true} {
				exp := "${!s557_q_" + op + "}"
				if quoted {
					exp = `"` + exp + `"`
				}
				definitions, want := "", "n=0\n"
				if matches {
					definitions = "s557_q_a=one; s557_q_z=two; "
					want = "n=2<s557_q_a><s557_q_z>\n"
				}
				if op == "*" && quoted {
					want = "n=1<>\n"
					if matches {
						want = "n=1<s557_q_a s557_q_z>\n"
					}
				}
				cases = append(cases, testCase{fmt.Sprintf("%s/quoted=%v/matches=%v", op, quoted, matches), "set -u; " + definitions + "set -- " + exp + `; printf 'n=%s' "$#"; for x; do printf '<%s>' "$x"; done; printf '\n'`, want})
			}
		}
	}
	cases = append(cases,
		testCase{"assignment-star-ifs", `set -u; s557_q_a=one; s557_q_z=two; IFS=:; v=${!s557_q_*}; printf '<%s>\n' "$v"`, "<s557_q_a:s557_q_z>\n"},
		testCase{"assignment-at", `set -u; s557_q_a=one; s557_q_z=two; v=${!s557_q_@}; printf '<%s>\n' "$v"`, "<s557_q_a s557_q_z>\n"},
		testCase{"assignment-zero", `set -u; v=${!s557_q_*}; printf '<%s>\n' "$v"`, "<>\n"},
		testCase{"nameref-prefix", `set -u; declare -n s557_q_=absent; s557_q_a=one; v=${!s557_q_*}; printf '<%s>\n' "$v"`, "<s557_q_ s557_q_a>\n"},
		testCase{"prefix-value-irrelevant", `set -u; s557_q_=other; s557_q_a=one; v=${!s557_q_*}; printf '<%s>\n' "$v"`, "<s557_q_ s557_q_a>\n"},
		testCase{"quoted-at-custom-ifs", `set -u; s557_q_a=one; s557_q_z=two; IFS=:; set -- "${!s557_q_@}"; printf '%s:<%s>:<%s>\n' "$#" "$1" "$2"`, "2:<s557_q_a>:<s557_q_z>\n"},
		testCase{"unquoted-star-custom-ifs", `set -u; s557_q_a=one; s557_q_z=two; IFS=:; set -- ${!s557_q_*}; printf '%s:<%s>:<%s>\n' "$#" "$1" "$2"`, "2:<s557_q_a>:<s557_q_z>\n"},
		testCase{"quoted-star-empty-ifs", `set -u; s557_q_a=one; s557_q_z=two; IFS=; set -- "${!s557_q_*}"; printf '%s:<%s>\n' "$#" "$1"`, "1:<s557_q_as557_q_z>\n"},
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "case.sh")
			f, err := syntax.NewParser().Parse(strings.NewReader(tc.src), path)
			if err != nil {
				t.Fatal(err)
			}
			var out, errs bytes.Buffer
			r, err := New(Dir(dir), StdIO(nil, &out, &errs), WithBashCompatErrors(true))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := r.Run(ctx, f); err != nil || out.String() != tc.want || errs.Len() != 0 {
				t.Fatalf("error=%v stdout=%q want=%q stderr=%q", err, out.String(), tc.want, errs.String())
			}
			if reference := os.Getenv("SH_TEST_BASH_REFERENCE"); reference != "" {
				if err := os.WriteFile(path, []byte(tc.src), 0600); err != nil {
					t.Fatal(err)
				}
				cmd := exec.CommandContext(ctx, reference, path)
				cmd.Dir = dir
				got, err := cmd.CombinedOutput()
				if err != nil || string(got) != tc.want {
					t.Fatalf("Bash error=%v output=%q want=%q", err, got, tc.want)
				}
			}
		})
	}
}

func TestPrefixNamesInvalidQuoted(t *testing.T) {
	for _, op := range []string{"@", "*"} {
		for _, nounset := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/nounset=%v", op, nounset), func(t *testing.T) {
				src := `printf '%s' "${!1` + op + `}"; printf UNREACHED`
				if nounset {
					src = "set -u; " + src
				}
				f, err := syntax.NewParser().Parse(strings.NewReader(src), "case.sh")
				if err != nil {
					t.Fatal(err)
				}
				var out, errs bytes.Buffer
				r, err := New(Dir(t.TempDir()), StdIO(nil, &out, &errs), WithBashCompatErrors(true))
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := r.Run(ctx, f); err == nil || out.Len() != 0 || !strings.Contains(errs.String(), "bad substitution") {
					t.Fatalf("error=%v stdout=%q stderr=%q", err, out.String(), errs.String())
				}
				if reference := os.Getenv("SH_TEST_BASH_REFERENCE"); reference != "" {
					cmd := exec.CommandContext(ctx, reference, "-c", src)
					data, err := cmd.CombinedOutput()
					if err == nil || !strings.Contains(string(data), "bad substitution") || strings.Contains(string(data), "UNREACHED") {
						t.Fatalf("Bash error=%v output=%q", err, data)
					}
				}
			})
		}
	}
}
