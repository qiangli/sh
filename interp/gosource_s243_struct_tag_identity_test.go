//go:build full

package interp_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Sprint: #243; Story: #674; Story-ID: 63073886bfce

func TestS243OriginalGenericStructTagIdentityThreeModes(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "test", "typeparam", "issue46591.go"))
	if err != nil {
		t.Fatal(err)
	}
	typedSendThreeModes(t, string(source))
}

func TestS243GenericStructTagDynamicKeyIdentityThreeModes(t *testing.T) {
	typedSendThreeModes(t, `package main
import "fmt"

type Key[_ any] struct{}

func localA(m map[any]int) { type Local int; m[Key[Local]{}] = 1 }
func localB(m map[any]int) { type Local int; m[Key[Local]{}] = 2 }

func main() {
	m := map[any]int{
		Key[struct{ X int }]{}: 1,
		Key[struct{ X int "plain" }]{}: 2,
		Key[struct{ X int "json:\"x\\\\y\"" }]{}: 3,
		Key[struct{ X int "json:\"x\\\\z\"" }]{}: 4,
	}
	localA(m)
	localB(m)
	if len(m) != 6 { panic(len(m)) }

	// Struct tags are ignored by the explicit conversion rule, but the
	// resulting anonymous types remain distinct dynamic interface keys.
	type TaggedA struct { X int "a" }
	type TaggedB struct { X int "b" }
	a := TaggedA{X: 7}
	b := TaggedB(a)
	if a.X != b.X { panic("named conversion") }
	x := struct { X int "a" }{X: 9}
	y := struct { X int "b" }(x)
	m[x], m[y] = 7, 8
	if len(m) != 8 || m[x] != 7 || m[y] != 8 { panic("anonymous conversion identity") }
	fmt.Println("ok")
}`)
}
