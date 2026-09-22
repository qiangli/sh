//go:build full

package interp_test

import (
	"strings"
	"testing"

	"mvdan.cc/sh/v3/gosource"
)

func TestS219ComplexScalarAndCollections(t *testing.T) {
	source := `package main
import (
	"fmt"
	"math"
	"math/cmplx"
)
type namedComplex complex128
func main() {
	z := complex(5.0, 7.0)
	values := append([]complex128{}, z)
	named := append([]namedComplex{}, namedComplex(z))
	fmt.Printf("%T %v %v %v %v %v\n", values[0], values[0], real(values[0]), imag(values[0]), real(named[0]), imag(named[0]))
	zero := complex(0.0, 0.0)
	q := z / zero
	fmt.Println(math.IsInf(real(q), 0), math.IsInf(imag(q), 0), real(q), imag(q))
	n := cmplx.Sqrt(complex(math.Inf(1), math.NaN()))
	fmt.Println(math.IsInf(real(n), 0), math.IsNaN(imag(n)))
}`
	out, stderr, err := runGoSource(t, "s219complex", source)
	if err != nil {
		t.Fatalf("Runner: %v stdout=%q stderr=%q", err, out, stderr)
	}
	want := "complex128 (5+7i) 5 7 5 7\ntrue true +Inf +Inf\ntrue true\n"
	if out != want || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q; want %q", out, stderr, want)
	}
}

func TestS219ComplexRealRequiresComplex(t *testing.T) {
	source := "package main\nfunc main(){ _ = real(1) }\n"
	_, parseErr := gosource.Parse(strings.NewReader(source), "s219-real-int.go", gosource.Options{RunMain: true})
	if parseErr != nil {
		t.Fatalf("unexpected parse diagnostic: %v", parseErr)
	}
	_, stderr, err := runGoSource(t, "s219-real-int", source)
	if err == nil || !strings.Contains(stderr, "requires complex argument") {
		t.Fatalf("err=%v; want complex operand rejection", err)
	}
}
