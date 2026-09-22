package gosource_test

import (
	"reflect"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/syntax"
)

func TestS243RecoveredGoDeferUsesExactDeclarations(t *testing.T) {
	src := `package p
import (
	"fmt"
	"math"
)
func f() {
	i := 1
	defer fmt.Sprint
	go math.Sin
	go i
	var unused int
}
`
	want := []string{
		"recover.go:8:18: expression in defer must be function call",
		"recover.go:9:13: expression in go must be function call",
		"recover.go:10:6: expression in go must be function call",
		"recover.go:11:6: declared and not used: unused",
	}
	got := sprint165Diagnostics(t, "recover.go", []byte(src), gosource.Options{})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got: %q\nwant: %q", got, want)
	}
}

// TestS243EmbeddedTypeKeepsPackageIdentity covers the interpreted, flattened
// package shape. The embedded field belongs to package b, but its type object
// belongs to package a; its lowered name must follow the type object rather
// than an identically named declaration in b.
func TestS243EmbeddedTypeKeepsPackageIdentity(t *testing.T) {
	a := gosource.PackageSpec{Path: "example.com/p/a", Sources: []gosource.Source{{
		Name: "a.go", Data: []byte("package a\n\ntype T int\n"),
	}}}
	b := gosource.PackageSpec{Path: "example.com/p/b", Sources: []gosource.Source{{
		Name: "b.go", Data: []byte("package b\n\nimport \"example.com/p/a\"\n\ntype T struct { a.T }\n"),
	}}}
	main := gosource.Source{Name: "main.go", Data: []byte("package main\n\nimport \"example.com/p/b\"\n\nvar _ = b.T{}\n")}
	program, err := gosource.Load([]gosource.Source{main}, gosource.Options{Packages: []gosource.PackageSpec{a, b}})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := syntax.NewPrinter().Print(&out, program.File); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "type __gosource_pkg_1_T struct { __gosource_pkg_0_T }") {
		t.Fatalf("embedded type lost package identity:\n%s", text)
	}
}
