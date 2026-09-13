package interp_test

import (
	"testing"

	"github.com/go-quicktest/qt"
	"mvdan.cc/sh/v3/interp"
)

func TestSprint162CollectionElementDestinationTypes(t *testing.T) {
	src := `type T struct { s string; f float64 }
func main() {
	t := T{"hello", 0.2}
	printf '%s:%s\n' t.s t.f
}
main()
`
	out, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(out, "hello:0.2\n"))
}

func TestSprint162CollectionElementRejectsIncompatibleValue(t *testing.T) {
	src := `func main() { _ = []float64{"nope"} }
main()
`
	_, stderr, err := runBashSharpCall(t, src)
	qt.Assert(t, qt.ErrorIs(err, interp.ExitStatus(2)))
	qt.Assert(t, qt.IsNotNil(err), qt.Commentf("stderr: %s", stderr))
}
