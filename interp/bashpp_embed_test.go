// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp_test

import (
	"testing"

	"github.com/go-quicktest/qt"
	"mvdan.cc/sh/v3/interp"
)

func TestBashPPPromotedFieldsReadWriteAndShadowing(t *testing.T) {
	const src = `type Leaf struct { N int }
type Left struct { Leaf }
type Right struct { N int }
type Outer struct { Left; Label string }
type Shadow struct { Left; N int }
func main() {
 o := Outer{Left: Left{Leaf: Leaf{N: 3}}, Label: "x"}
 printf '%s:%s:' o.N o.Label
 o.N = 4
 s := Shadow{Left: Left{Leaf: Leaf{N: 5}}, N: 6}
 printf '%s:%s' o.N s.N
}
main()
`
	out, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(out, "3:x:4:6"))
}

func TestBashPPPromotedPointerFieldAndMethods(t *testing.T) {
	const src = `type Leaf struct { N int }
func (v Leaf) Value() { printf 'v%s:' v.N }
func (v Leaf) Change() { v.N = 99 }
func (p *Leaf) Set(n int) { p.N = n }
type Outer struct { *Leaf }
func main() {
 p := new(Leaf)
 p.N = 2
 o := Outer{Leaf: p}
 o.Value()
 o.Change()
 printf '%s:' o.N
 o.Set(7)
 printf '%s:' o.N
 f := o.Value
 f()
}
main()
`
	out, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(out, "v2:2:7:v7:"))
}

func TestBashPPPromotedMethodSets(t *testing.T) {
	const src = `type Leaf struct { N int }
func (v Leaf) Value() { }
func (p *Leaf) Set(n int) { p.N = n }
type ValueOuter struct { Leaf }
type PointerOuter struct { *Leaf }
type Valuer interface { Value() }
type Setter interface { Set(int) }
func main() {
 var v ValueOuter
 var vv Valuer = v
 p := &v
 var ps Setter = p
 q := new(Leaf)
 w := PointerOuter{Leaf: q}
 var ws Setter = w
 vv.Value()
 ps.Set(4)
 ws.Set(5)
 printf '%s:%s' v.N w.N
}
main()
`
	out, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(out, "4:5"))
}

func TestBashPPPromotedPointerMethodCallsAndExpressions(t *testing.T) {
	const src = `type Leaf struct { N int }
func (p *Leaf) Set(n int) { p.N = n }
type ValueOuter struct { Leaf }
type PointerOuter struct { *Leaf }
func main() {
 var v ValueOuter
 v.Set(8)
 p := &v
 (*ValueOuter).Set(p, 9)
 q := new(Leaf)
 w := PointerOuter{Leaf: q}
 PointerOuter.Set(w, 10)
 printf '%s:%s' v.N w.N
}
main()
`
	out, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(out, "9:10"))
}

func TestBashPPValueEmbeddingExcludesPointerMethodFromValueSet(t *testing.T) {
	const src = `type Leaf struct { N int }
func (p *Leaf) Set(n int) { p.N = n }
type Outer struct { Leaf }
type Setter interface { Set(int) }
func main() {
 var o Outer
 var s Setter = o
}
main()
`
	_, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.ErrorIs(err, interp.ExitStatus(2)))
	qt.Assert(t, qt.StringContains(stderr, "missing method Set"))
}

func TestBashPPEmbeddedAliasPromotesTargetMethods(t *testing.T) {
	const src = `type Leaf struct { N int }
func (p *Leaf) Set(n int) { p.N = n }
type Alias = Leaf
type Outer struct { Alias }
func main() {
 var o Outer
 o.Set(12)
 printf '%s' o.N
}
main()
`
	out, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(out, "12"))
}

func TestBashPPPromotedSelectorAmbiguity(t *testing.T) {
	for _, tc := range []struct {
		name string
		src  string
	}{
		{"field", `type A struct { X int }
type B struct { X int }
type C struct { A; B }
func main() {
 var c C
 x := c.X
 printf '%s' "$x"
}
main()
`},
		{"method", `type A int
func (v A) M() { }
type B int
func (v B) M() { }
type C struct { A; B }
func main() {
 var c C
 c.M()
}
main()
`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, stderr, err := runBashSharpCall(t, tc.src)
			qt.Assert(t, qt.ErrorIs(err, interp.ExitStatus(2)))
			qt.Assert(t, qt.StringContains(stderr, "BASHPP-ESELECTOR-AMBIGUOUS"))
		})
	}
}

func TestBashPPPromotedNilPointerDiagnostic(t *testing.T) {
	const src = `type Leaf struct { N int }
type Outer struct { *Leaf }
func main() {
 var o Outer
 x := o.N
 printf '%s' "$x"
}
main()
`
	_, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.ErrorIs(err, interp.ExitStatus(2)))
	qt.Assert(t, qt.StringContains(stderr, "BASHPP-ENIL-DEREF"))
}

func TestBashPPEmbeddedInterfaceRejected(t *testing.T) {
	const src = `type Reader interface { Read() }
type Outer struct { Reader }
func main() { var o Outer }
main()
`
	_, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.ErrorIs(err, interp.ExitStatus(2)))
	qt.Assert(t, qt.StringContains(stderr, "BASHPP-ESTRUCT-EMBED: embedded interface fields are not supported"))
}
