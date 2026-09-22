package gosource

import (
	"strings"
	"testing"
)

func TestCgoMetadataRemainsPackageScoped(t *testing.T) {
	program, err := Load([]Source{src("main.go", `package main
import ("test/a"; "test/b")
func main() { a.F(); b.G() }
`)}, Options{RunMain: true, FakeImportC: true, Packages: []PackageSpec{
		{Path: "test/a", Sources: []Source{src("a.go", `package a
/*
#define A_ONLY 1
#include <stdlib.h>
*/
import "C"
func F() { _ = C.malloc(1) }
`)}},
		{Path: "test/b", Sources: []Source{src("b.go", `package b
/*
#define B_ONLY 1
#include <stdlib.h>
*/
import "C"
func G() { _ = C.calloc(1, 2) }
`)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := program.File.CgoPackages
	if len(got) != 2 {
		t.Fatalf("CgoPackages = %#v", got)
	}
	if got[0].Path != "test/a" || got[1].Path != "test/b" || got[0].Alias == got[1].Alias {
		t.Fatalf("package identities = %#v", got)
	}
	if !strings.Contains(got[0].Preamble, "A_ONLY") || strings.Contains(got[0].Preamble, "B_ONLY") ||
		!strings.Contains(got[1].Preamble, "B_ONLY") || strings.Contains(got[1].Preamble, "A_ONLY") {
		t.Fatalf("preambles crossed packages: %#v", got)
	}
	if len(got[0].Symbols) != 1 || got[0].Symbols[0].Name != "malloc" || got[0].Symbols[0].Kind != "func" ||
		len(got[1].Symbols) != 1 || got[1].Symbols[0].Name != "calloc" || got[1].Symbols[0].Kind != "func" {
		t.Fatalf("symbols = %#v", got)
	}
}
