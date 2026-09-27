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

func TestGoSourceS281NativeInterfaceNilComparisonGuards(t *testing.T) {
	const source = `package main

import (
	"bytes"
	"fmt"
	"reflect"
)

type T struct{ X int }
type Alias = any
type Box struct{ V Alias }
type Nested struct{ Box Box }

var calls int

func once(v any) any {
	calls++
	return v
}

func main() {
	typedPointer := reflect.New(reflect.TypeOf((*T)(nil))).Elem().Interface()
	box := Box{V: typedPointer}
	nested := Nested{Box: box}
	var nilInterface any
	typedSlice := reflect.ValueOf([]int(nil)).Interface()
	typedMap := reflect.ValueOf(map[string]int(nil)).Interface()
	byteBuffer := reflect.New(reflect.TypeOf((*bytes.Buffer)(nil))).Elem().Interface()

	fmt.Println(box.V == nil, nil == box.V, box.V != nil, nil != box.V)
	fmt.Println(nilInterface == nil, nil == nilInterface)
	fmt.Println(typedSlice == nil, typedMap == nil, byteBuffer == nil)
	fmt.Println(nested.Box.V == nil, nil != nested.Box.V)
	fmt.Println(once(box.V) == box.V, calls)
	p := box.V.(*T)
	fmt.Println(p == nil, nil == p)
}
`
	differGoSource(t, source, nil, "")
}
