//go:build full

package interp_test

import "testing"

// Sprint: #249; Story: #714; Story-ID: 5251dc793b6e
//
// A struct value held by an `error` interface is never equal to nil. The
// dependency worker compares the dynamic value it is handed; the interface
// marker on that value, not its dynamic kind, decides the nil comparison.
func TestGoSourceS249StructErrorInterfaceNilComparison(t *testing.T) {
	typedSendThreeModes(t, `package main

import "go/scanner"

func main() {
	var err error = scanner.Error{Msg: "boom"}
	println(err == nil, err != nil, nil == err)
	got, ok := err.(scanner.Error)
	println(ok, got.Msg)
}
`)
}

// An imported pointer-receiver method on an addressable dot-imported struct
// returns a concrete struct through an `error` result. Re-assigning that
// result into an already declared `error` variable, returning it through an
// interpreted `error` result, comparing it with nil, and asserting it against
// the dot-imported and the package-qualified struct type must all observe the
// interface wrapper.
func TestGoSourceS249DotImportedMethodResultKeepsInterface(t *testing.T) {
	typedSendThreeModes(t, `package main

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	. "go/types"
	"reflect"
)

const example = "package p\n\nvar _ = missing\n"

func check() error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "example.go", example, parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	conf := Config{FakeImportC: true, Importer: importer.Default()}
	_, err = conf.Check("example", fset, []*ast.File{file}, nil)
	return err
}

func main() {
	err := check()
	if err == nil {
		panic("no error")
	}
	dotted, ok := err.(Error)
	if !ok {
		panic("lost interface (dot import)")
	}
	named, ok := err.(types.Error)
	if !ok {
		panic("lost interface (named import)")
	}
	println(dotted.Msg == named.Msg, reflect.ValueOf(err).FieldByName("Soft").Bool())
}
`)
}
