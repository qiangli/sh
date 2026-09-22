//go:build full

// Sprint: #219; Story: #463; Story-ID: a6f104b906d9
package interp_test

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
)

// TestS219ReflectedMethodHandle pins the synchronous lifetime and receiver
// identity of a method extracted as reflect.Value -> Interface -> func. The
// same handle remains forbidden when handed to an unreviewed retaining API.
func TestS219ReflectedMethodHandle(t *testing.T) {
	t.Run("value receiver", func(t *testing.T) {
		src := `package main
import "reflect"
type M int
var called int
func (m M) M() { called += int(m) }
func main() {
	local := M(7)
	reflect.ValueOf(local).MethodByName("M").Interface().(func())()
	println(called)
}`
		_, stderr, err := runGoSource(t, "s219-reflected-value", src)
		qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr=%q", stderr))
		qt.Assert(t, qt.Equals(stderr, "7\n"))
	})

	t.Run("pointer receiver identity", func(t *testing.T) {
		src := `package main
import "reflect"
type M struct { n int }
func (m *M) M() { m.n++ }
func main() {
	local := M{n: 4}
	reflect.ValueOf(&local).MethodByName("M").Interface().(func())()
	println(local.n)
}`
		_, stderr, err := runGoSource(t, "s219-reflected-pointer", src)
		qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr=%q", stderr))
		qt.Assert(t, qt.Equals(stderr, "5\n"))
	})

	t.Run("retained dependency refused", func(t *testing.T) {
		src := `package main
import (
	"reflect"
	"time"
)
type M int
func (M) M() {}
func main() {
	local := M(1)
	f := reflect.ValueOf(local).MethodByName("M").Interface().(func())
	time.AfterFunc(time.Second, f)
	println("after")
}`
		out, stderr, err := runGoSource(t, "s219-reflected-retained", src)
		qt.Assert(t, qt.IsNotNil(err))
		qt.Assert(t, qt.StringContains(err.Error()+stderr, "dependency mutation of interpreter-owned references is unsupported for time.AfterFunc"))
		qt.Assert(t, qt.IsFalse(strings.Contains(out+stderr, "after")))
	})
}
