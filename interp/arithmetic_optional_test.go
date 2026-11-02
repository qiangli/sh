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

func TestArithmeticOptionalSuffix(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"command", `tail='+3'; (( (4)${tail} ==7 )); printf '%s\n' "$?"`, "0\n"},
		{"empty", `unset tail; printf '%s\n' "$(((4+2)${tail+9}))"`, "6\n"},
		{"operator", `tail='+5'; printf '%s\n' "$(((4+2)${tail}))"`, "11\n"},
		{"comparison", `different() { return "$(( ($1)!=($2)${3+${oops:?too many}} ))"; }; different 7 7; printf '%s:' "$?"; different 7 8; printf '%s\n' "$?"`, "0:1\n"},
		{"nounset", `set -u; unset tail; printf '%s\n' "$(((7)${tail+${oops:?unexpected}}))"`, "7\n"},
		{"short-circuit-arithmetic", `unset tail; x=0; printf '%s:' "$((0 && (x++)${tail+9}))"; printf '%s\n' "$x"`, "0:0\n"},
		{"once", `tail=1; x=0; printf '%s:' "$(((++x)${tail:+$(printf +2; printf x >>calls)}))"; printf '%s:%s\n' "$x" "$(<calls)"`, "3:1:x\n"},
		{"lazy-command", `unset tail; printf '%s\n' "$(((3)${tail:+$(printf +2; printf WRONG >&2)}))"`, "3\n"},
		{"indexed", `unset tail; a=(4 9); i=1; printf '%s\n' "$(((a[i])${tail+9}))"`, "9\n"},
		{"unary-spacing", `unset tail; a=4; printf '%s\n' "$(((+ +a)${tail+9}))"`, "4\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "case.sh")
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
			if err := r.Run(ctx, f); err != nil || out.String() != tc.want || errs.Len() != 0 {
				t.Fatalf("error=%v stdout=%q want=%q stderr=%q", err, out.String(), tc.want, errs.String())
			}
			if reference := os.Getenv("SH_TEST_BASH_REFERENCE"); reference != "" {
				refDir := t.TempDir()
				path = filepath.Join(refDir, "case.sh")
				if err := os.WriteFile(path, []byte(tc.src), 0600); err != nil {
					t.Fatal(err)
				}
				cmd := exec.CommandContext(ctx, reference, path)
				cmd.Dir = refDir
				got, err := cmd.CombinedOutput()
				if err != nil || string(got) != tc.want {
					t.Fatalf("Bash error=%v output=%q want=%q", err, got, tc.want)
				}
			}
		})
	}
}

func TestArithmeticOptionalSuffixErrors(t *testing.T) {
	for _, tc := range []struct{ name, src, diagnostic string }{
		{"nonempty-invalid", `tail=1; printf '%s\n' "$(((4)${tail+9}))"; printf UNREACHED`, "arithmetic"},
		{"quoted-numeric", `unset tail; printf '%s\n' "$((('4')${tail+9}))"; printf UNREACHED`, "arithmetic"},
		{"alternate-error", `different() { return "$(( ($1)!=($2)${3+${oops:?too many}} ))"; }; different 7 7 extra; printf UNREACHED`, "too many"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := syntax.NewParser().Parse(strings.NewReader(tc.src), "case.sh")
			if err != nil {
				t.Fatal(err)
			}
			var out, errs bytes.Buffer
			r, err := New(Dir(t.TempDir()), StdIO(nil, &out, &errs), WithBashCompatErrors(true), WithBashSource([]byte(tc.src)))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := r.Run(ctx, f); err == nil || out.Len() != 0 || !strings.Contains(errs.String(), tc.diagnostic) {
				t.Fatalf("error=%v stdout=%q stderr=%q", err, out.String(), errs.String())
			}
			if reference := os.Getenv("SH_TEST_BASH_REFERENCE"); reference != "" {
				cmd := exec.CommandContext(ctx, reference, "-c", tc.src)
				data, err := cmd.CombinedOutput()
				if err == nil || !strings.Contains(string(data), tc.diagnostic) || strings.Contains(string(data), "UNREACHED") {
					t.Fatalf("Bash error=%v output=%q", err, data)
				}
			}
		})
	}
}
