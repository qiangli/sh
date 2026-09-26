//go:build full

package interp_test

// Sprint: #281; Story: #809; Story-ID: fac7e14af4a8
//
// A value-receiver fmt protocol method (String/Format) that runs while fmt
// formats a slice it copied is no longer refused up front. This is the shape
// cmd/compile/internal/ir's TestHTMLWriter reaches: an IR node value carrying a
// slice field is handed to a Printf-style helper that forwards its named
// variadic slice into fmt, so the slice crosses the bridge nested inside a
// value the coherence path now admits, and fmt then looks up the value's
// String/Format method.
//
// Safety is general, not a shape special case: a value receiver owns only a
// decoded copy of itself and cannot reach the interpreter's backing storage
// through it (a write through that copy's reference storage is caught by the
// copied-receiver digest), and the post-callback coherence re-read walks every
// copied source — including a slice nested in the receiver's own fields — and
// fails the callback if the body wrote one, so a read-only method prints as Go
// does while a stale write fails loudly.

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
)

// A read-only value String and value Format over a slice the caller copies must
// print exactly what native Go prints. differGoSource runs each source through
// both the interpreter and the real Go oracle and requires them to agree.
func TestS809ValueProtocolCallbackReadOnly(t *testing.T) {
	cases := map[string]string{
		// A value String reading its own slice field (len), forwarded through a
		// variadic helper exactly as ir.HTMLWriter.Printf forwards into fmt.
		"value_string_reads_own_slice": `package main

import "fmt"

type node struct {
	name string
	kids []int
}

func (n node) String() string { return fmt.Sprintf("%s(%d)", n.name, len(n.kids)) }

func printf(format string, v ...any) string { return fmt.Sprintf(format, v...) }

func main() {
	n := node{name: "root", kids: []int{1, 2, 3}}
	fmt.Println(printf("<%v>", n))
}
`,
		// A value Format method (fmt.Formatter), which ir dumps reach through the
		// %+v verb, over the same copied-slice boundary and a variadic forward.
		"value_format_over_writer": `package main

import "fmt"

type node struct {
	tag  string
	data []byte
}

func (n node) Format(f fmt.State, verb rune) { fmt.Fprintf(f, "%s:%d", n.tag, len(n.data)) }

func fprintf(format string, v ...any) { fmt.Printf(format, v...) }

func main() {
	n := node{tag: "loc", data: []byte{7, 8}}
	fprintf("%+v\n", n)
}
`,
		// A slice of struct values whose element String reads a slice field: the
		// copied source is nested one level below the direct slice fmt walks.
		"slice_of_values_string": `package main

import "fmt"

type node struct {
	name string
	kids []int
}

func (n node) String() string { return fmt.Sprintf("%s#%d", n.name, len(n.kids)) }

func main() {
	fmt.Println([]node{{"a", []int{1}}, {"b", []int{2, 3}}})
}
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) { differGoSource(t, src, nil, "") })
	}
}

// A value protocol method that writes a copied source fmt cannot re-read live
// must not print stale. Here the written slice (global g) is reachable only
// nested inside the value fmt formats, so there is no direct slice argument to
// synchronise; the post-callback coherence re-read catches the write and fails
// the callback loudly, printing nothing rather than a stale value.
func TestS809ValueProtocolCallbackStaleWriteRefused(t *testing.T) {
	src := `package main
import "fmt"
var g = []int{1, 2, 3}
type node struct{ kids []int }
func (n node) String() string { g[0] = 99; return fmt.Sprintf("n%d", len(n.kids)) }
func printf(format string, v ...any) string { return fmt.Sprintf(format, v...) }
func main() {
	n := node{kids: g}
	fmt.Println(printf("<%v>", n))
	println("after")
}`
	out, stderr, err := runGoSource(t, "s809stale", src)
	qt.Assert(t, qt.IsNotNil(err))
	qt.Assert(t, qt.StringContains(err.Error(), "the copy would be stale"))
	qt.Assert(t, qt.IsFalse(strings.Contains(out+stderr, "after")), qt.Commentf("program continued: %q %q", out, stderr))
}
