package gosource

import (
	"reflect"
	"strings"
	"testing"
)

func src(name, data string) Source { return Source{Name: name, Data: []byte(data)} }

const pkgA = "package a\n\nfunc F() int { return 1 }\n"
const pkgB = "package b\n\nimport \"./a\"\n\nfunc G() int { return a.F() + 1 }\n"
const mainC = "package main\n\nimport (\n\t\"fmt\"\n\t\"./b\"\n)\n\nfunc main() { fmt.Println(b.G()) }\n"

func TestPackageMapResolvesRelativeImportsThroughBase(t *testing.T) {
	prog, err := Load([]Source{src("c.go", mainC)}, Options{
		ImportBase: "test",
		Packages: []PackageSpec{
			{Path: "test/a", Sources: []Source{src("a.go", pkgA)}},
			{Path: "test/b", Sources: []Source{src("b.go", pkgB)}},
		},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if prog.Main != "main" {
		t.Fatalf("Main = %q", prog.Main)
	}
	want := []Resolution{
		{From: "test/b", Import: "./a", Path: "test/a", Origin: "package-map", Name: "a", Files: []string{"a.go"}},
		{From: "main", Import: "fmt", Path: "fmt", Origin: "importer", Name: "fmt"},
		{From: "main", Import: "./b", Path: "test/b", Origin: "package-map", Name: "b", Files: []string{"b.go"}},
	}
	if !reflect.DeepEqual(prog.Resolutions, want) {
		t.Fatalf("Resolutions =\n%#v\nwant\n%#v", prog.Resolutions, want)
	}
}

func TestPackageMapResolutionOrderIsDeterministic(t *testing.T) {
	load := func() []Resolution {
		prog, err := Load([]Source{src("c.go", mainC)}, Options{
			ImportBase: "test",
			Packages: []PackageSpec{
				{Path: "test/a", Sources: []Source{src("a.go", pkgA)}},
				{Path: "test/b", Sources: []Source{src("b.go", pkgB)}},
			},
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		return prog.Resolutions
	}
	first := load()
	for i := 0; i < 5; i++ {
		if got := load(); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d differs:\n%#v\nwant\n%#v", i, got, first)
		}
	}
}

func TestRelativeImportWithoutBaseIsRefused(t *testing.T) {
	_, err := Load([]Source{src("b.go", pkgB)}, Options{
		Packages: []PackageSpec{{Path: "test/a", Sources: []Source{src("a.go", pkgA)}}},
	})
	if err == nil || !strings.Contains(err.Error(), "relative import path requires an import base") {
		t.Fatalf("err = %v, want relative-import refusal", err)
	}
}

func TestRelativeImportEscapingBaseIsRefused(t *testing.T) {
	_, err := Load([]Source{src("b.go", "package b\n\nimport \"../a\"\n\nvar _ = a.F\n")}, Options{
		ImportBase: "test",
		Packages:   []PackageSpec{{Path: "a", Sources: []Source{src("a.go", pkgA)}}},
	})
	if err == nil || !strings.Contains(err.Error(), "escapes import base") {
		t.Fatalf("err = %v, want escape refusal", err)
	}
}

func TestRelativeImportNeverFallsThroughToDisk(t *testing.T) {
	_, err := Load([]Source{src("b.go", pkgB)}, Options{ImportBase: "test"})
	if err == nil || !strings.Contains(err.Error(), `package "test/a" is not in the explicit package map`) {
		t.Fatalf("err = %v, want map miss", err)
	}
}

func TestPackageMapTakesPrecedenceOverImporter(t *testing.T) {
	// A map entry named like a standard library package shadows it: the map
	// is consulted first, exactly as -importcfg shadows GOROOT.
	prog, err := Load([]Source{src("m.go", "package main\n\nimport \"strings\"\n\nfunc main() { _ = strings.Shadow }\n")}, Options{
		Packages: []PackageSpec{{Path: "strings", Sources: []Source{src("s.go", "package strings\n\nvar Shadow = 1\n")}}},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(prog.Resolutions) != 1 || prog.Resolutions[0].Origin != "package-map" {
		t.Fatalf("Resolutions = %#v", prog.Resolutions)
	}
}

func TestDependencyDiagnosticsAreAttributedAndStopTheLoad(t *testing.T) {
	_, err := Load([]Source{src("c.go", mainC)}, Options{
		ImportBase: "test",
		Packages: []PackageSpec{
			{Path: "test/a", Sources: []Source{src("a.go", "package a\n\nfunc F() int { return \"x\" }\n")}},
			{Path: "test/b", Sources: []Source{src("b.go", pkgB)}},
		},
	})
	if err == nil || !strings.HasPrefix(err.Error(), "a.go:3:") {
		t.Fatalf("err = %v, want a diagnostic positioned in a.go", err)
	}
}

func TestExplicitPackageRejectsRelativeOrDuplicatePaths(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		{"./a", "must not be relative"},
		{"", "empty import path"},
	} {
		_, err := Load([]Source{src("m.go", "package main\n\nfunc main() {}\n")}, Options{Packages: []PackageSpec{{Path: tc.path, Sources: []Source{src("a.go", pkgA)}}}})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("path %q: err = %v, want %q", tc.path, err, tc.want)
		}
	}
	_, err := Load([]Source{src("m.go", "package main\n\nfunc main() {}\n")}, Options{Packages: []PackageSpec{
		{Path: "test/a", Sources: []Source{src("a.go", pkgA)}},
		{Path: "test/a", Sources: []Source{src("a2.go", pkgA)}},
	}})
	if err == nil || !strings.Contains(err.Error(), "duplicate package path") {
		t.Errorf("duplicate: err = %v", err)
	}
}

func TestImportPathAttributesProgramResolutions(t *testing.T) {
	prog, err := Load([]Source{src("b.go", pkgB)}, Options{
		ImportBase: "test",
		ImportPath: "test/b",
		Packages:   []PackageSpec{{Path: "test/a", Sources: []Source{src("a.go", pkgA)}}},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(prog.Resolutions) != 1 || prog.Resolutions[0].From != "test/b" {
		t.Fatalf("Resolutions = %#v", prog.Resolutions)
	}
}
