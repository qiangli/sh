package main

import "fmt"

// A typed float declaration keeps its value as a float: unary minus, binary
// arithmetic, comparison, assignment, and passing to a typed parameter all see
// a floating-point operand, whatever text the interpreter stores.

func equal(a, b float32) bool { return a == b }

func half(x float64) float64 { return x / 2 }

func main() {
	var f00 float32 = 3.14159
	var f01 float32 = -3.14159
	var f09 float32 = 1e-10
	var f10 float32 = 1e+10
	fmt.Println(f01 == -f00, -f01 == f00)
	fmt.Println(equal(f09, 1/f10), equal(f09, f10))
	var g float64 = 2.5
	fmt.Println(-g, g*2, half(g))
	var k float64
	k = 2.5
	k = -k
	fmt.Println(k, k < g)
	h := g
	fmt.Println(-h)
	var i int = 3
	fmt.Println(-i)
	x := 0.75
	fmt.Println(-x, x+g)
	var f32 float32 = 0.1
	var f64 float64 = 0.1
	fmt.Println(float64(f32) == f64, f32)
}
