//go:build full

package interp_test

// Sprint: #243; Story: #424; Story-ID: ffabc6c1c44a
// Sprint: #248; Story: #424; Story-ID: ffabc6c1c44a

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestS243RetainedFinalizerRefusal pinned two unchanged SDK roots at the
// retained-finalizer refusal while finalizers could only reach a bridge
// mirror. Sprint 248 keeps the finalizer on the interpreter's own allocation
// (gosource_finalizer.go) and releases dead bindings at their last use
// (gosource_liveness.go), so the refusal is obsolete for both roots and each
// becomes the positive control it was standing in for:
//
//   - abi/map.go (early): the *T map-key temporary must stay live across V's
//     three collections; a finalizer that ran early would print FAIL.
//   - fixedbugs/issue46725.go (early and late): the object must survive
//     collections while an interface still reaches it, and must be finalized
//     once neither the array nor the interface is used again.
//   - tinyfin.go and mallocfin.go (late, once, ordered): each finalizer runs
//     once with its own object, and an object reachable from another
//     finalizable object is finalized only after it.
//
// typeparam/orderedmap.go registers its finalizer without refusal now; it
// still stops at the select that mixes a dependency channel with an
// interpreter-owned one, which is pinned so that residual stays explicit.
func TestS243RetainedFinalizerRefusal(t *testing.T) {
	for _, rel := range []string{
		filepath.Join("abi", "map.go"),
		filepath.Join("fixedbugs", "issue46725.go"),
		"tinyfin.go",
		"mallocfin.go",
	} {
		t.Run(filepath.ToSlash(rel), func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "test", rel))
			if err != nil {
				t.Fatal(err)
			}
			out, stderr, err := runGoSource(t, strings.TrimSuffix(filepath.Base(rel), ".go"), string(source))
			if err != nil || out != "" || stderr != "" {
				t.Fatalf("unchanged %s: run=%v stdout=%q stderr=%q", filepath.ToSlash(rel), err, out, stderr)
			}
		})
	}
	t.Run("typeparam/orderedmap.go", func(t *testing.T) {
		source, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "test", "typeparam", "orderedmap.go"))
		if err != nil {
			t.Fatal(err)
		}
		got := runGoSourceRunnerError(t, string(source))
		if strings.Contains(got, "original callback signature requires value-semantics parameters") ||
			!strings.Contains(got, "mixed native/interpreted channel select requires atomic arbitration") {
			t.Fatalf("unchanged orderedmap.go did not reach its select residual: %q", got)
		}
	})
}
