//go:build full

package interp_test

import (
	"testing"

	"github.com/go-quicktest/qt"
)

// A scalar bound by `:=` from an untyped constant carries the constant's
// default type as its dynamic type when it is stored in an interface, both
// on declaration and as an interface-keyed map index. This is the eface
// portion of maplinear.go.
func TestS243PointerShortScalarDynamicType(t *testing.T) {
	src := `package main
func main() {
	x := 0
	var i interface{} = x
	m := map[interface{}]int{}
	for j := 0; j < 3; j++ {
		m[j] = 1
	}
	f := 1.5
	var g any = f
	_, isFloat := g.(float64)
	println(i.(int), len(m), isFloat)
}`
	out, stderr, err := runGoSource(t, "s243-pointer-short-scalar-dynamic", src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(out, ""))
	qt.Assert(t, qt.Equals(stderr, "0 3 true\n"))
}

// A declared function named as a value stores in an interface with its
// declared signature as the dynamic type; a result name in the declaration
// does not change that signature (bug269.go).
func TestS243PointerDeclaredFuncInterfaceValue(t *testing.T) {
	src := `package main
func f() (ok bool) { return false }
func main() {
	var i interface{}
	i = f
	_ = i.(func() bool)
	g := i.(func() (bool))
	h := f
	var j any = h
	_, isFunc := j.(func() bool)
	_, isOther := j.(func() int)
	println(g(), isFunc, isOther)
}`
	out, stderr, err := runGoSource(t, "s243-pointer-declared-func-iface", src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(out, ""))
	qt.Assert(t, qt.Equals(stderr, "false true false\n"))
}

// `type P = *T` is the very type *T: a P receiver selects *T's pointer
// methods (issue23489.go), including through a nil P.
func TestS243PointerAliasMethodSet(t *testing.T) {
	src := `package main
type T struct{ n int }
func (t *T) Foo() int { if t == nil { return -1 }; return t.n }
func (t T) Bar() int { return t.n + 10 }
type P = *T
type Q = P
func main() {
	var p P
	println(p.Foo())
	p = &T{n: 3}
	var q Q = p
	println(p.Foo(), q.Foo(), q.Bar())
}`
	out, stderr, err := runGoSource(t, "s243-pointer-alias-method-set", src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(out, ""))
	qt.Assert(t, qt.Equals(stderr, "-1\n3 3 13\n"))
}

// A conversion to a defined pointer type — or to a type parameter bound to
// one — is a value of that type, so it assigns to a variable declared with
// the same name while still aliasing the operand's storage (issue49295.go).
func TestS243PointerDefinedPointerConversionAssign(t *testing.T) {
	src := `package main
type Token *[4]byte
func Read[T interface{ ~*[4]byte }](buf []byte) (t T, err error) {
	if n := len(t); len(buf) >= n {
		t = T(buf[:n])
		return
	}
	return
}
func direct(buf []byte) (t Token) {
	t = Token(buf[:4])
	return
}
func main() {
	b := []byte("abcdef")
	tok, err := Read[Token](b)
	tok[1] = 'X'
	var u Token
	u = direct(b)
	u[2] = 'Y'
	var v Token
	v = Token(u)
	println(string(b), err == nil, v[1] == 'X')
}`
	out, stderr, err := runGoSource(t, "s243-pointer-defined-conversion-assign", src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(out, ""))
	qt.Assert(t, qt.Equals(stderr, "aXYdef true true\n"))
}
