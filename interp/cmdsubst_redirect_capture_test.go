package interp

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

// TestCommandSubstitutionRedirectCapture exercises temporary integer assignments
// and substitution capture without external commands or platform-specific FDs.
// The target is expanded from the pre-command value, before the prefix applies.
func TestCommandSubstitutionRedirectCapture(t *testing.T) {
	for _, tc := range []struct{ name, script, stdout, stderr string }{
		{"integer-prefix", `declare -i target=2; target=9 printf 'marker\n' >&$(echo $target)`, "", "marker\n"},
		{"integer-prefix-eval", `declare -i target=2; target=9 eval printf "'marker\n'" >&$(echo $target)`, "", "marker\n"},
		{"integer-reset", `target=42; unset target; typeset -i target; target=4 eval echo $(echo target+=3); target=2; target=9 echo marker >&$(echo $target)`, "target+=3\n", "marker\n"},
		{"capture-bytes", `declare -i target=2; target=9 printf '<%s>\n' "$(echo $target)"`, "<2>\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := 0; i < 20; i++ {
				f, err := syntax.NewParser().Parse(strings.NewReader(tc.script), "capture-test")
				if err != nil {
					t.Fatal(err)
				}
				var stdout, stderr bytes.Buffer
				r, err := New(StdIO(nil, &stdout, &stderr))
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err = r.Run(ctx, f)
				cancel()
				if err != nil || stdout.String() != tc.stdout || stderr.String() != tc.stderr {
					t.Fatalf("iteration %d: error=%v stdout=%q stderr=%q; want stdout=%q stderr=%q", i, err, stdout.String(), stderr.String(), tc.stdout, tc.stderr)
				}
			}
		})
	}
}
