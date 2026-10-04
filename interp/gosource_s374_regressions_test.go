//go:build full

package interp_test

import "testing"

func TestGoSourceS374Bug441BlankArgumentEvaluatedOnce(t *testing.T) {
	_, stderr, err := runGoSource(t, "s374-bug441", `package main
var did int
func main() { foo(side()); foo2(side(), side()); foo3(side(), side()); T.m1(T(side())); T(1).m2(side()); if did != 7 { println("BUG: missing", 7-did, "calls") } }
func foo(_ int) {}
func foo2(_, _ int) {}
func foo3(int, int) {}
type T int
func (_ T) m1() {}
func (t T) m2(_ int) {}
func side() int { did++; return 1 }
`)
	if err != nil || stderr != "" {
		t.Fatalf("run error=%v stderr=%q", err, stderr)
	}
}
