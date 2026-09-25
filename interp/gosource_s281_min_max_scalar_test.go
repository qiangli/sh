package interp_test

// Sprint: #281; Story: #808; Story-ID: a5674a7da9a6

import "testing"

// TestGoSourceS281MinMaxScalarOperand pins the general fix for min/max used
// as an operand inside a larger scalar expression, not as the sole
// right-hand side of `:=`. bashPPEvalScalarExpr's call switch recognized
// only the bare `len`/`cap`/`copy` spellings as builtins with a scalar
// result; every other predeclared name, including `min`/`max`, fell through
// to the ordinary function lookup and failed with "BASHPP-EEXPR-UNDEFINED:
// undefined callable max". cmd/compile/internal/abt hits exactly this shape
// computing `t.height_ = 1 + max(l.height(), r.height())`.
func TestGoSourceS281MinMaxScalarOperand(t *testing.T) {
	out, stderr, err := runS270G1Source(t, "s281-min-max-scalar", `package main
import "fmt"
type node struct {
	left, right *node
	height      int8
}
func (n *node) h() int8 {
	if n == nil {
		return 0
	}
	return n.height
}
func rebalance(l, r *node) int8 {
	return 1 + max(l.h(), r.h())
}
func main() {
	left := &node{height: 2}
	right := &node{height: 5}
	n := &node{left: left, right: right}
	n.height = rebalance(n.left, n.right)
	shallow := min(n.left.h(), n.right.h()) - 1
	fmt.Println(n.height, shallow)
}`)
	if err != nil || out != "6 1\n" {
		t.Fatalf("err=%v stdout=%q stderr=%q", err, out, stderr)
	}
}
