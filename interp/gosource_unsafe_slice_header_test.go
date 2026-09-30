//go:build full

package interp_test

import "testing"

// A slice header with a pointer-bearing Data field keeps the backing storage
// alive. Reading it as a slice must preserve the named element type and alias.
// This is a success oracle: unsupported interpreted views must leave it RED.
func TestGoSourceUnsafeSliceHeaderNamedElement(t *testing.T) {
	src := `package main
import "unsafe"

type Element int32
type Header struct {
	Data unsafe.Pointer
	Len int
	Cap int
}

func view(h *Header) []Element {
	return *(*[]Element)(unsafe.Pointer(h))
}

func main() {
	backing := []Element{7, 11, 13}
	h := Header{Data: unsafe.Pointer(&backing[0]), Len: 2, Cap: 3}
	s := view(&h)
	if len(s) != 2 || cap(s) != 3 || s[1] != Element(11) {
		panic("slice header or named element lost")
	}
	s[0] = Element(17)
	if backing[0] != Element(17) {
		panic("slice view lost backing storage")
	}
}`
	out, stderr, err := runGoSource(t, "unsafe-slice-header", src)
	if err != nil || out != "" || stderr != "" {
		t.Fatalf("err=%v out=%q stderr=%q", err, out, stderr)
	}
}
