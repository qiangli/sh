// Mechanism: type *ast.BasicLit / *ast.BinaryExpr — calling an element of a
// map or slice of functions. The call converter treats every IndexExpr callee
// as a generic instantiation and passes the index to c.typ.
package main

import "fmt"

func main() {
	handlers := map[string]func() int{"one": func() int { return 1 }}
	steps := []func() int{func() int { return 10 }, func() int { return 20 }}
	i := 0
	fmt.Println(handlers["one"]() + steps[i+1]())
}
