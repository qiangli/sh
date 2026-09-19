//go:build full

package interp_test

// Sprint: #209; Story: #461; Story-ID: 4ed649697945
//
// A Go-source program is vetted by go/types at parse time, which rejects
// every genuinely impossible type assertion (see the negative test below).
// The evaluator's own EASSERT-IMPOSSIBLE pre-check could therefore only
// fire in Go-source mode on assertions go/types had allowed: instantiated
// type parameters, which Go resolves dynamically — comma-ok yields false,
// a type-switch case simply mismatches, and the bare form panics. The
// pre-check is now the classic dialect's guard alone.
//
// Story #461 root classification:
//   - fixedbugs/issue65962.go, typeparam/issue50002.go — this cause; pass.
//   - typeparam/mdempsky/16.go — same rejection cause repaired (the assert
//     now panics and recovers), but the root still fails on a distinct
//     cause: the panic message spells the anonymous interface operand as
//     "interface", not "interface { T() main.T }" (goSourceReflectTypeText
//     has no method-set spelling for interface types).
//   - fixedbugs/bug473.go — distinct cause: ranging over a variadic
//     ...interface{} parameter drops the elements' interface carrier, so
//     `s += v.(int)` fails with EASSERT-OPERAND (a plain []interface{}
//     range works).
//   - fixedbugs/bug510.go, typeparam/issue47740b.go — distinct cause: a
//     native call result (reflect's .Interface()) carries no interface
//     value, so asserting it fails with EASSERT-OPERAND.

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"mvdan.cc/sh/v3/gosource"
)

func TestStory461TypeParamAssertions(t *testing.T) {
	src := `package main
import "fmt"
type S struct{}
func (S) M() byte { return 0 }
type I[T any] interface{ M() T }
type J interface{ f(); g() }
func report[T, A any](x I[T]) {
	matched := false
	switch x.(type) {
	case A:
		matched = true
	}
	_, ok := x.(A)
	caught := ""
	func() {
		defer func() {
			if recover() != nil {
				caught = "panic"
			}
		}()
		_ = x.(A)
	}()
	fmt.Println(matched, ok, caught)
}
func nilAssert[T any]() bool {
	var x J
	_, ok := x.(T)
	return ok
}
func main() {
	report[byte, string](S{})
	report[byte, S](S{})
	fmt.Println(nilAssert[bool]())
}`
	out, stderr, err := runGoSource(t, "story461", src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(out, "false false panic\ntrue true \nfalse\n"))
}

// The rejection the evaluator no longer repeats still happens where Go
// performs it: go/types refuses an impossible concrete assertion at parse
// time, so no Go-source program reaches the evaluator with one. The classic
// dialect's runtime guard is covered by TestBashPPInterfaceDiagnostics
// ("assert impossible").
func TestStory461ImpossibleAssertionStillRejected(t *testing.T) {
	src := `package main
type I interface{ f(); g() }
func main() {
	var x I
	_, ok := x.(bool)
	_ = ok
}`
	_, err := gosource.Parse(strings.NewReader(src), "story461neg.go", gosource.Options{RunMain: true})
	qt.Assert(t, qt.IsNotNil(err))
	qt.Assert(t, qt.StringContains(err.Error(), "impossible type assertion"))
}
