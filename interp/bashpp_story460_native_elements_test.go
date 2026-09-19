//go:build full

// Sprint: #209; Story: #460; Story-ID: d8e7d58f362b
package interp_test

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"mvdan.cc/sh/v3/gosource"
)

func TestStory460NativeCollectionElements(t *testing.T) {
	src := `package main
import (
	"fmt"
	"math"
	"math/big"
	"runtime"
)
func main() {
	framesIter := runtime.CallersFrames(nil)
	frame, _ := framesIter.Next()
	frames := []runtime.Frame{frame}
	floats := []float64{math.NaN()}
	ints := []*big.Int{big.NewInt(7)}
	fmt.Println(frames[0].Function == "", math.IsNaN(floats[0]), ints[0].Int64())
}`
	out, stderr, err := runGoSource(t, "story460nativeelements", src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(out, "true true 7\n"))
}

func TestStory460NativeCollectionElementTypeMismatch(t *testing.T) {
	src := `package main
import (
	"math/big"
	"runtime"
)
func main() {
	_ = []runtime.Frame{big.NewInt(7)}
}`
	_, err := gosource.Parse(strings.NewReader(src), "story460_native_mismatch.go", gosource.Options{RunMain: true})
	qt.Assert(t, qt.ErrorMatches(err, `(?s).*cannot use .*big\.NewInt.* as runtime\.Frame.*`))
}
