//go:build full

package interp_test

import "testing"

// Sprint: #281; Story: #809; Story-ID: fac7e14af4a8
//
// A pointer read from a global slice can be boxed into an interface field in an
// elided pointer-to-struct map literal. This is the small shape go/types'
// typeterm table reaches: map[string]*term{"int": {false, Typ[Int]}}, where
// the second field is an interface and Typ is a global []*Basic.
func TestS281InterfaceFieldFromIndexedPointer(t *testing.T) {
	source := `package main

import "fmt"

type Type interface {
	String() string
}

type Basic struct {
	Name string
}

func (b *Basic) String() string { return b.Name }

var Typ = []*Basic{{Name: "int"}}

type term struct {
	flag bool
	typ  Type
}

var terms = map[string]*term{
	"int": {false, Typ[0]},
}

func main() {
	fmt.Println(terms["int"].typ.String())
}
`
	differGoSource(t, source, nil, "")
}
