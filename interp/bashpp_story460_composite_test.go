//go:build full

package interp_test

// Sprint: #209; Story: #460; Story-ID: d8e7d58f362b

import (
	"testing"

	"github.com/go-quicktest/qt"
)

func TestStory460CompositeValueCarriers(t *testing.T) {
	src := `package main
import (
	"fmt"
	"reflect"
)
type item struct { n int }
type key struct { a, b int32; c float64 }
type holder struct { values []int }
func main() {
	i := item{n: 1}
	a := [...]*item{&i, &i, &i}
	nested := [][len(a)]*item{a, a}
	pointers := []*item{{n: 2}, {n: 3}}

	m := map[key]bool{{a: 1, b: 2, c: 3}: true}
	t := reflect.ArrayOf(8, reflect.TypeOf(""))
	reflected := reflect.New(t).Elem().Interface().([8]string)
	nativeKey := map[[8]string]int{reflected: 7}

	original := holder{values: []int{9}}
	copied := []holder{original}
	var inferred = []int{1}
	inferred = nil

	fmt.Println(len(nested[1]), pointers[1].n, m[key{a: 1, b: 2, c: 3}], nativeKey[reflected])
	fmt.Println(&original.values[0] == &copied[0].values[0], inferred == nil)
}`
	out, stderr, err := runGoSource(t, "story460composite", src)
	qt.Assert(t, qt.IsNil(err), qt.Commentf("stderr: %s", stderr))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.Equals(out, "3 3 true 7\ntrue true\n"))
}
