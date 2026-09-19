//go:build full

// Sprint: #209; Story: #461; Story-ID: 4ed649697945
//
// The shared repair here is deliberately narrow: typed nils converted to an
// interface type compare as Go interface values, while existing func-field nil
// comparison stays covered by the same comparable-value path. The unsafe.Pointer
// roots in bug328.go and issue44830.go are pinned as unrelated native/unsafe
// materialization gaps rather than folded into this comparison mechanism.
package interp_test

import (
	"testing"

	"github.com/go-quicktest/qt"
)

func TestStory461TypedNilInterfaceComparison(t *testing.T) {
	src := `package main
import "fmt"
type box struct{}
func main() {
	fmt.Println(interface{}(nil) == nil)
	fmt.Println(nil == any(nil))
	fmt.Println(interface{}((*box)(nil)) == nil)
	fmt.Println(any((*box)(nil)) != nil)
}`
	out, stderr, err := runGoSource(t, "story461typedniliface", src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(out, "true\ntrue\nfalse\ntrue\n"))
}

func TestStory461FuncFieldNilComparison(t *testing.T) {
	src := `package main
import "fmt"
type modifier struct {
	name string
	t    func()
}
func (m modifier) valid() error {
	if m.t == nil {
		return fmt.Errorf("%s missing t", m.name)
	}
	return nil
}
func main() {
	fmt.Println(modifier{name: "zero"}.valid())
	fmt.Println(modifier{name: "live", t: func(){}}.valid())
}`
	out, stderr, err := runGoSource(t, "story461funcfield", src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(out, "zero missing t\n<nil>\n"))
}
