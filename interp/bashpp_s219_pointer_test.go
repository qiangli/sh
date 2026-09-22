//go:build full

package interp_test

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
)

// Pointer-to-array indexing has the same implicit dereference in an address
// expression as it does in a read or write: &p[i] is &(*p)[i]. This is the
// ordinary typed-pointer portion shared by nilptr2.go and issue49295.go.
func TestS219PointerArrayIndexAddress(t *testing.T) {
	src := `package main
func main() {
	a := [3]int{2, 4, 6}
	p := &a
	q := &p[1]
	*q = 9
	println(a[1], *q)
}`
	out, stderr, err := runGoSource(t, "s219-pointer-array-index", src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(out, ""))
	qt.Assert(t, qt.Equals(stderr, "9 9\n"))
}

// A nil pointer-to-array still faults when the implicit dereference is
// evaluated. Merely supporting p[i] must not turn nil storage into an array.
func TestS219PointerArrayIndexAddressNil(t *testing.T) {
	src := `package main
func main() {
	var p *[3]int
	_ = &p[0]
}`
	_, stderr, err := runGoSource(t, "s219-pointer-array-index-nil", src)
	qt.Assert(t, qt.IsNotNil(err))
	qt.Assert(t, qt.IsTrue(strings.Contains(stderr, "nil pointer dereference")),
		qt.Commentf("stderr: %s", stderr))
}

// A generic conversion from a slice to a defined pointer-to-array type is
// ordinary Go pointer construction and aliases the slice's backing storage.
func TestS219PointerGenericSliceToDefinedArrayPointer(t *testing.T) {
	src := `package main
type Token *[4]byte
func take[T interface{ ~*[4]byte }](s []byte) T { return T(s[:4]) }
func main() {
	b := []byte("abcd")
	p := take[Token](b)
	p[2] = 'X'
	println(string(b))
}`
	out, stderr, err := runGoSource(t, "s219-pointer-generic-slice-array", src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(out, ""))
	qt.Assert(t, qt.Equals(stderr, "abXd\n"))
}

// unsafe.Pointer reinterpretation is intentionally outside this mechanism.
// Keep the boundary loud instead of accidentally treating its integer-like
// carrier as interpreter-owned typed storage.
func TestS219PointerUnsafeReinterpretationRejected(t *testing.T) {
	src := `package main
import "unsafe"
func main() {
	x := 1
	_ = (*byte)(unsafe.Pointer(&x))
}`
	_, stderr, err := runGoSource(t, "s219-pointer-unsafe-boundary", src)
	qt.Assert(t, qt.IsNotNil(err))
	qt.Assert(t, qt.IsTrue(strings.Contains(stderr, "BASHPP-")),
		qt.Commentf("stderr: %s", stderr))
}
