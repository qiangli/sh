//go:build full

package interp_test

// Sprint: #281; Story: #809; Story-ID: fac7e14af4a8

import "testing"

func TestGoSourceS281ReflectedTypedNilPointerInterface(t *testing.T) {
	const source = `package main

import (
	"fmt"
	"reflect"
)

type T struct{ X int }

func main() {
	v := reflect.New(reflect.TypeOf((*T)(nil))).Elem().Interface()
	p := v.(*T)
	fmt.Println(v == nil, p == nil)
}
`
	differGoSource(t, source, nil, "")
}

func TestGoSourceS281NativeInterfaceTypedNilPointerFieldTuple(t *testing.T) {
	const source = `package main

import (
	"fmt"
	"reflect"
)

type T struct{ X int }
type Box struct{ V any }

func main() {
	box := Box{V: reflect.New(reflect.TypeOf((*T)(nil))).Elem().Interface()}
	p := box.V.(*T)
	fmt.Println(box.V == nil, p == nil)
}
`
	differGoSource(t, source, nil, "")
}
