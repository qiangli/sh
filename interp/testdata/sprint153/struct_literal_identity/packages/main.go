// Unexported field names are qualified by their package: struct{ int } and
// struct{ _ []int } spelled in another package are different types from the
// same spelling here, while struct{ N int } is the same type everywhere.
package main

import (
	"fmt"

	"example.com/anon/a"
	"example.com/anon/b"
)

func F() any { return struct{ int }{0} }

func main() {
	_, ok1 := F().(struct{ int })
	_, ok2 := a.F().(struct{ int })
	fmt.Println(ok1, ok2)
	fmt.Println(a.G() == b.G(), a.X == b.X)
	_, ok3 := a.H().(struct{ N int })
	fmt.Println(ok3, a.H() == any(struct{ N int }{7}))
}
