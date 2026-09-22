//go:build full

package interp_test

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
)

func TestS243UnsafeBlankStructView(t *testing.T) {
	const prefix = `package main
import "unsafe"
type Source struct { x, y, z int }
type Blank struct { _, _, _ int }
type Nested struct { _ struct { a, b, c int } }
`
	t.Run("layout identity lifetime and comparison", func(t *testing.T) {
		src := prefix + `
func blank(x, y, z int) Blank { return *(*Blank)(unsafe.Pointer(&Source{x, y, z})) }
func nested(x, y, z int) any { return *(*Nested)(unsafe.Pointer(&Source{x, y, z})) }
func main() {
	if blank(1, 2, 3) != blank(4, 5, 6) { panic("blank bytes compared") }
	if nested(1, 2, 3) != nested(4, 5, 6) { panic("nested blank bytes compared") }
}
`
		out, stderr, err := runGoSource(t, "s243-unsafe-blank", src)
		qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
		qt.Assert(t, qt.Equals(out, ""))
		qt.Assert(t, qt.Equals(stderr, ""))
	})

	for _, tc := range []struct{ name, target, want string }{
		{"observable target", `type Bad struct { x, _, _ int }; var _ = *(*Bad)(unsafe.Pointer(&Source{}))`, "BASHPP-EUNSAFE-VIEW"},
		{"layout mismatch", `type Bad struct { _, _ int }; var _ = *(*Bad)(unsafe.Pointer(&Source{}))`, "BASHPP-EUNSAFE-LAYOUT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, err := runGoSource(t, "s243-unsafe-negative", prefix+tc.target+"\nfunc main() {}\n")
			qt.Assert(t, qt.IsNotNil(err))
			qt.Assert(t, qt.IsTrue(strings.Contains(stderr, tc.want)), qt.Commentf("stderr: %s", stderr))
		})
	}
}
