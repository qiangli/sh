//go:build full

// Sprint: #219; Story: #463; Story-ID: a6f104b906d9
//
// Native dependency bridge: slice writeback identity and the fmt direct-slice
// callback boundary. Outside-corpus reductions of the roots:
//
//   - fixedbugs/issue30606, fixedbugs/issue49110, reflectmethod7
//     a slice of native structs (reflect.StructField, reflect.Value) is
//     handed to a dependency call; the worker hands the elements back as
//     fresh handles nesting this session's own reflect.Type/Value, and the
//     writeback must accept them as it does a pointer writeback's pointee.
//   - typeparam/issue51303
//     a local slice type whose only method (Equal) fmt never looks up is
//     printed directly; the stale-alias refusal is for protocol methods.
//
// The negative space stays: a String or Error method that could write the
// slice while fmt reads its copy is refused before the callback runs.
package interp_test

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
)

// issue49110 / issue30606: reflect.StructOf over an original []reflect.StructField
// whose Type fields are this session's reflect.Type handles. The rebuilt
// elements must still be usable afterwards, and the constructed type must
// reflect them.
func TestS219SliceWritebackNestedHandles(t *testing.T) {
	src := `package main
import "reflect"
func typ(x interface{}) reflect.Type { return reflect.ValueOf(x).Type() }
func main() {
	fields := []reflect.StructField{
		{Name: "A", Type: reflect.TypeOf(int(0))},
		{Name: "B", Type: reflect.ArrayOf(3, reflect.SliceOf(typ(uint64(0))))},
	}
	st := reflect.StructOf(fields)
	println(st.NumField(), st.Field(0).Name, st.Field(1).Type.String())
	println(fields[1].Name, fields[1].Type.Kind() == reflect.Array)
	blank := reflect.StructOf([]reflect.StructField{
		{Name: "_", PkgPath: "main", Type: reflect.TypeOf(int(0))},
		{Name: "_", PkgPath: "main", Type: reflect.TypeOf(int(0))},
	})
	println(blank.NumField(), reflect.New(st).Elem().Field(0).Int())
}`
	_, stderr, err := runGoSource(t, "s219structof", src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(stderr, "2 A [3][]uint64\nB true\n2 0\n"))
}

// reflectmethod7: the []reflect.Value argument to Func.Call nests a
// reflect.Value handle, and the callee is a reflect.Value the transport cannot
// see into; the parked Call request must serve the original method it
// re-enters. The value method promoted onto *S runs on a copy of the
// worker-owned pointee, exactly as Go calls it.
func TestS219SliceWritebackReflectValueCall(t *testing.T) {
	src := `package main
import "reflect"
type S int
var called = 0
func (s S) M() { called++ }
func main() {
	t := reflect.TypeOf(S(0))
	fn, ok := reflect.PointerTo(t).MethodByName("M")
	if !ok { panic("FAIL") }
	args := []reflect.Value{reflect.New(t)}
	fn.Func.Call(args)
	println(called, args[0].Kind() == reflect.Pointer)
}`
	_, stderr, err := runGoSource(t, "s219reflectcall", src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(stderr, "1 true\n"))
}

// The boundary that stays: a pointer-receiver original method invoked on a
// pointee the worker owns (reflect.New, no origin) has no interpreter storage
// to bind its writes to, and is refused rather than run on a detached copy.
func TestS219ReflectValueCallPointerReceiverRefused(t *testing.T) {
	src := `package main
import "reflect"
type S int
func (s *S) M() { *s = 7 }
func main() {
	t := reflect.TypeOf(S(0))
	fn, _ := reflect.PointerTo(t).MethodByName("M")
	fn.Func.Call([]reflect.Value{reflect.New(t)})
	println("after")
}`
	out, stderr, err := runGoSource(t, "s219reflectcallptr", src)
	qt.Assert(t, qt.IsNotNil(err))
	qt.Assert(t, qt.StringContains(err.Error(), "pointer callback requires original receiver identity"))
	qt.Assert(t, qt.IsFalse(strings.Contains(out+stderr, "after")), qt.Commentf("program continued: %q %q", out, stderr))
}

// issue51303: a local generic slice type carrying a method fmt never invokes
// prints directly. The elements are interpreter storage and the emitter only
// reads its copy.
func TestS219FmtDirectSliceWithoutProtocolMethod(t *testing.T) {
	src := `package main
import "fmt"
type ss[E comparable, T []E] []T
func (ss[E, T]) Equal(a, b T) bool { return len(a) == len(b) }
type ints []int
func (s ints) Sum() int { n := 0; for _, v := range s { n += v }; return n }
func main() {
	x := ss[int, []int]{{1}, {2, 3}}
	fmt.Println("x", x, x.Equal(x[0], x[1]))
	s := ints{4, 5}
	fmt.Printf("%v %d\n", s, s.Sum())
}`
	out, stderr, err := runGoSource(t, "s219fmtslice", src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(out, "x [[1] [2 3]] false\n[4 5] 9\n"))
}

// The boundary the relaxation must not cross: a protocol method fmt does look
// up — String, Error — could write the original slice while fmt reads its
// decoded copy, so the request is refused before any callback runs.
func TestS219FmtDirectSliceProtocolMethodRefused(t *testing.T) {
	for name, src := range map[string]string{
		"stringer": `package main
import "fmt"
var b = []byte{1}
type Tag int
func (t Tag) String() string { println("original-read"); b[0] = 7; return "tag" }
func main() { fmt.Println(Tag(1), b); println("after") }`,
		"error_in_slice": `package main
import "fmt"
type E int
func (e E) Error() string { println("original-read"); return "e" }
func main() { es := []E{1}; fmt.Println(es); println("after") }`,
		"embedded_stringer": `package main
import "fmt"
type inner int
func (inner) String() string { println("original-read"); return "in" }
type outer struct{ inner; N int }
func main() { os := []outer{{1, 2}}; fmt.Println(os); println("after") }`,
	} {
		t.Run(name, func(t *testing.T) {
			out, stderr, err := runGoSource(t, "s219refuse-"+name, src)
			qt.Assert(t, qt.IsNotNil(err))
			qt.Assert(t, qt.StringContains(err.Error(), "original callback with copied slice references is unsupported"))
			qt.Assert(t, qt.IsFalse(strings.Contains(out+stderr, "original-read")), qt.Commentf("callback ran: %q %q", out, stderr))
			qt.Assert(t, qt.IsFalse(strings.Contains(out+stderr, "after")), qt.Commentf("program continued: %q %q", out, stderr))
		})
	}
}
