//go:build full

// Sprint: #209; Story: #461; Story-ID: 4ed649697945
package interp_test

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
)

func TestStory461TupleAssignFromGoSourceFuncValues(t *testing.T) {
	cases := map[string]string{
		"func_typed_field": `package main
import "fmt"
type C struct {
	a int
	x func(*C) int
}
func g(c *C) int { return c.a }
func main() {
	c := &C{a: 6}
	c.x = g
	var v int
	v = c.x(c)
	fmt.Println(v)
}`,
		"two_result_local_function": `package main
import "fmt"
func pair(n int) (int, int) { return n, n + 1 }
func main() {
	var a, b int
	a, b = pair(4)
	fmt.Println(a, b)
}`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			out, stderr, err := runGoSource(t, "story461tuple"+name, src)
			qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
			qt.Assert(t, qt.Equals(stderr, ""))
			if name == "func_typed_field" {
				qt.Assert(t, qt.Equals(out, "6\n"))
			} else {
				qt.Assert(t, qt.Equals(out, "4 5\n"))
			}
		})
	}
}

func TestStory461ClassicTupleAssignStillRequiresDeclaredFunction(t *testing.T) {
	src := "func main() {\n a := 0\n b := 0\n a, b = missing()\n}\nmain()\n"
	_, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.IsNotNil(err))
	qt.Assert(t, qt.IsTrue(strings.Contains(stderr, "BASHPP-EASSIGN-CALL")), qt.Commentf("stderr: %s", stderr))
}
