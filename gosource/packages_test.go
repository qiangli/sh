package gosource

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
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

// mappedProgram is one link set: the program's sources plus the explicit
// package map, in the Sprint 149 convention (import base "test", mapped
// paths "test/<name>", relative imports "./<name>").
type mappedProgram struct {
	program  []Source
	packages []PackageSpec
}

func (m mappedProgram) options(runMain bool) Options {
	return Options{RunMain: runMain, ImportBase: "test", ImportPath: "test/main", Packages: m.packages}
}

// runMapped loads the link set with RunMain and runs the lowered file through
// the interpreter exactly as a CLI would, returning stdout.
func runMapped(t *testing.T, m mappedProgram) string {
	t.Helper()
	prog, err := Load(m.program, m.options(true))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var stdout, stderr bytes.Buffer
	runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.Dir(t.TempDir()), interp.StdIO(nil, &stdout, &stderr))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := runner.Run(ctx, prog.File); err != nil {
		t.Fatalf("Runner: %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if stderr.Len() > 0 {
		t.Fatalf("Runner stderr: %q (stdout %q)", stderr.String(), stdout.String())
	}
	return stdout.String()
}

// goRunMapped runs the same link set with the Go toolchain: the sources are
// laid out as module "test" — the import base — with every mapped package at
// its path, and the relative imports the map convention uses are spelled as
// the module paths they resolve to, which module mode requires.
func goRunMapped(t *testing.T, m mappedProgram) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel string, data []byte) {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		text := regexp.MustCompile(`"\./([^"]+)"`).ReplaceAllString(string(data), `"test/$1"`)
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", []byte("module test\n\ngo 1.25\n"))
	for _, s := range m.program {
		write(s.Name, s.Data)
	}
	for _, spec := range m.packages {
		for _, s := range spec.Sources {
			write(strings.TrimPrefix(spec.Path, "test/")+"/"+s.Name, s.Data)
		}
	}
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "run", ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go run: %v\n%s", err, stderr.String())
	}
	return stdout.String()
}

func readFixture(t *testing.T, rel string) Source {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "sprint151", "mapped", filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return Source{Name: filepath.Base(rel), Data: data}
}

// TestMappedFixtureRunsThroughInterp runs the Sprint 151 M1 fixture end to
// end: the explicit package map is linked at lowering time, the interpreter
// never sees an import for the mapped path, and the output matches go run.
func TestMappedFixtureRunsThroughInterp(t *testing.T) {
	m := mappedProgram{
		program:  []Source{readFixture(t, "main.go")},
		packages: []PackageSpec{{Path: "test/a", Sources: []Source{readFixture(t, "a/a.go")}}},
	}
	prog, err := Load(m.program, m.options(true))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if want := []LinkedPackage{{Path: "test/a", Name: "a", Files: []string{"a.go"}}}; !reflect.DeepEqual(prog.Packages, want) {
		t.Fatalf("Packages = %#v, want %#v", prog.Packages, want)
	}
	for _, stmt := range prog.File.Stmts {
		if imp, ok := stmt.Cmd.(*syntax.BashPPImport); ok {
			if path := imp.Path.Parts[0].(*syntax.Lit).Value; path == "./a" || path == "test/a" {
				t.Fatalf("mapped import %q reached the lowered file", path)
			}
		}
	}
	if len(prog.Sources) != 2 || prog.Sources[1].Name != "a.go" {
		t.Fatalf("Sources = %#v, want main.go and a.go", prog.Sources)
	}
	want := goRunMapped(t, m)
	if got := runMapped(t, m); got != want {
		t.Fatalf("interp %q, go run %q", got, want)
	}
	if want != "hello, mapped\n" {
		t.Fatalf("go run = %q", want)
	}
}

// TestMappedInitOrder pins Go's cross-package initialisation order over the
// flat file: a dependency's variables and init functions run completely
// before its importer's initialisers, in map order, and the program's last.
func TestMappedInitOrder(t *testing.T) {
	m := mappedProgram{
		program: []Source{src("main.go", `package main

import (
	"fmt"

	"./b"
)

var mainVar = trace("main var", b.BVar+1)

func trace(what string, v int) int { fmt.Println(what, v); return v }

func init() { fmt.Println("main init", mainVar) }

func main() { fmt.Println("main", b.G()) }
`)},
		packages: []PackageSpec{
			{Path: "test/a", Sources: []Source{src("a.go", `package a

import "fmt"

var AVar = traceA("a var", 1)

func traceA(what string, v int) int { fmt.Println(what, v); return v }

func init() { fmt.Println("a init 1", AVar) }

func init() { fmt.Println("a init 2") }

func F() int { return AVar * 10 }
`)}},
			{Path: "test/b", Sources: []Source{src("b.go", `package b

import (
	"fmt"

	"./a"
)

var BVar = traceB("b var", a.F()+1)

func traceB(what string, v int) int { fmt.Println(what, v); return v }

func init() { fmt.Println("b init", BVar) }

func G() int { return BVar + a.AVar }
`)}},
		},
	}
	want := goRunMapped(t, m)
	if got := runMapped(t, m); got != want {
		t.Fatalf("interp:\n%sgo run:\n%s", got, want)
	}
	const pinned = "a var 1\na init 1 1\na init 2\nb var 11\nb init 11\nmain var 12\nmain init 12\nmain 12\n"
	if want != pinned {
		t.Fatalf("go run = %q, want %q", want, pinned)
	}
	prog, err := Load(m.program, m.options(true))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"__gosource_init_0", "__gosource_init_1", "__gosource_init_2", "__gosource_init_3"}; !reflect.DeepEqual(prog.InitFunctions, want) {
		t.Fatalf("InitFunctions = %q, want %q", prog.InitFunctions, want)
	}
}

// TestMappedNameCollisionIsRefused pins the flat-namespace boundary: a
// package-level name declared by two linked packages, the program included
// and unexported names included, is refused at Load with both paths named.
func TestMappedNameCollisionIsRefused(t *testing.T) {
	a := PackageSpec{Path: "test/a", Sources: []Source{src("a.go", "package a\n\nvar count = 1\n\nfunc F() int { return count }\n")}}
	for _, tc := range []struct {
		name string
		m    mappedProgram
		want string
	}{
		{"program-vs-package", mappedProgram{
			program:  []Source{src("main.go", "package main\n\nimport \"./a\"\n\nvar count = 2\n\nfunc main() { _ = a.F() + count }\n")},
			packages: []PackageSpec{a},
		}, "gosource: package-level name count declared by both test/a and test/main; execution against the explicit package map requires distinct names"},
		{"package-vs-package", mappedProgram{
			program:  []Source{src("main.go", "package main\n\nimport (\n\t\"./a\"\n\t\"./b\"\n)\n\nfunc main() { _ = a.F() + b.G() }\n")},
			packages: []PackageSpec{a, {Path: "test/b", Sources: []Source{src("b.go", "package b\n\ntype F struct{}\n\nfunc G() int { return 0 }\n")}}},
		}, "gosource: package-level name F declared by both test/a and test/b; execution against the explicit package map requires distinct names"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(tc.m.program, tc.m.options(true))
			if err == nil || err.Error() != tc.want {
				t.Fatalf("err = %v\nwant %s", err, tc.want)
			}
		})
	}
}

// TestMappedEmbedIsRefused pins the M1 boundary on go:embed inside a mapped
// package: directives are attached for the program's files only.
func TestMappedEmbedIsRefused(t *testing.T) {
	m := mappedProgram{
		program:  []Source{src("main.go", "package main\n\nimport (\n\t\"fmt\"\n\n\t\"./a\"\n)\n\nfunc main() { fmt.Println(a.Text) }\n")},
		packages: []PackageSpec{{Path: "test/a", Sources: []Source{src("a.go", "package a\n\nimport _ \"embed\"\n\n//go:embed a.go\nvar Text string\n")}}},
	}
	_, err := Load(m.program, m.options(true))
	want := `a.go:5:1: gosource: go:embed in mapped package "test/a" is not supported by the explicit package map`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
}

// TestMappedTypeNameKnownDifference documents the M1 limit the finding names:
// the flat file spells every linked type under the program's package, so a
// mapped a.T prints as main.T where go run prints a.T. Pinned, not hidden;
// name mangling (M2) lifts it.
func TestMappedTypeNameKnownDifference(t *testing.T) {
	m := mappedProgram{
		program: []Source{src("main.go", `package main

import (
	"fmt"

	"./a"
)

func main() {
	var v a.T = a.New(3)
	fmt.Printf("%T %v %d\n", v, v, v.N)
	fmt.Println(a.Describe(v))
}
`)},
		packages: []PackageSpec{{Path: "test/a", Sources: []Source{src("a.go", `package a

import "fmt"

type T struct{ N int }

func New(n int) T { return T{N: n} }

func Describe(t T) string { return fmt.Sprintf("%T", t) }
`)}}},
	}
	if want, got := "a.T {3} 3\na.T\n", goRunMapped(t, m); got != want {
		t.Fatalf("go run = %q, want %q", got, want)
	}
	if want, got := "main.T {3} 3\nmain.T\n", runMapped(t, m); got != want {
		t.Fatalf("interp = %q, want the pinned known difference %q (if a mapped type now keeps its package, update this test and FINDINGS-M1.md)", got, want)
	}
}

// TestMappedDiamondImportLinksOnce pins that a package imported by two linked
// packages (b→a, c→a, main→b,c) is lowered once: one linked entry, one
// function definition, one initialised variable, and go run's output.
func TestMappedDiamondImportLinksOnce(t *testing.T) {
	m := mappedProgram{
		program: []Source{src("main.go", `package main

import (
	"fmt"

	"./b"
	"./c"
)

func main() { fmt.Println(b.G(), c.H(), b.G()+c.H()) }
`)},
		packages: []PackageSpec{
			{Path: "test/a", Sources: []Source{src("a.go", "package a\n\nimport \"fmt\"\n\nvar Counter = next()\n\nvar calls int\n\nfunc next() int { calls++; fmt.Println(\"a initialised\", calls); return calls }\n\nfunc F() int { return Counter }\n")}},
			{Path: "test/b", Sources: []Source{src("b.go", "package b\n\nimport \"./a\"\n\nfunc G() int { return a.F() + 1 }\n")}},
			{Path: "test/c", Sources: []Source{src("c.go", "package c\n\nimport \"./a\"\n\nfunc H() int { return a.F() + 2 }\n")}},
		},
	}
	want := goRunMapped(t, m)
	if got := runMapped(t, m); got != want {
		t.Fatalf("interp %q, go run %q", got, want)
	}
	if want != "a initialised 1\n2 3 5\n" {
		t.Fatalf("go run = %q", want)
	}
	prog, err := Load(m.program, m.options(true))
	if err != nil {
		t.Fatal(err)
	}
	if len(prog.Packages) != 3 || prog.Packages[0].Path != "test/a" {
		t.Fatalf("Packages = %#v", prog.Packages)
	}
	defs := map[string]int{}
	for _, stmt := range prog.File.Stmts {
		switch d := stmt.Cmd.(type) {
		case *syntax.BashPPFuncDecl:
			defs[d.Name.Value]++
		case *syntax.BashPPDecl:
			defs[d.Name.Value]++
		}
	}
	for _, name := range []string{"F", "next", "Counter", "calls", "G", "H"} {
		if defs[name] != 1 {
			t.Fatalf("%s defined %d times in the lowered file", name, defs[name])
		}
	}
}

// TestMappedSelectorSites covers every site the converter collapses a mapped
// qualifier at: types in struct fields, pointers, parameters and results
// (typ and text), composite literals, method calls on mapped types, function
// values, constants, variadics, interface satisfaction across packages,
// generics, and a blank import of a mapped package whose init still runs.
func TestMappedSelectorSites(t *testing.T) {
	m := mappedProgram{
		program: []Source{src("main.go", `package main

import (
	"fmt"

	"./a"
	_ "./z"
)

type Holder struct {
	Item  a.T
	Items []*a.T
	M     map[string]a.T
}

func use(t a.T) a.T { return t }

func main() {
	h := Holder{Item: a.T{N: 1}, Items: []*a.T{&a.T{N: 2}}, M: map[string]a.T{"k": a.T{N: 3}}}
	var p *a.T = &h.Item
	fmt.Println(h.Item.Double(), h.Items[0].N, h.M["k"].N, p.N, use(a.T{N: 4}).N, a.Kind, a.K2)
	fn := a.Double
	fmt.Println(fn(a.T{N: 5}), a.Sum(1, 2, 3))
	var s a.Shape = a.Square{Side: 2}
	fmt.Println(s.Area())
	fmt.Println(a.Generic[int](7))
}
`)},
		packages: []PackageSpec{
			{Path: "test/a", Sources: []Source{src("a.go", `package a

type T struct{ N int }

const Kind = "kind"

const K2 = 2

func (t T) Double() int { return t.N * 2 }

func Double(t T) int { return t.Double() }

func Sum(xs ...int) int {
	s := 0
	for _, x := range xs {
		s += x
	}
	return s
}

type Shape interface{ Area() int }

type Square struct{ Side int }

func (s Square) Area() int { return s.Side * s.Side }

func Generic[X any](x X) X { return x }
`)}},
			{Path: "test/z", Sources: []Source{src("z.go", "package z\n\nimport \"fmt\"\n\nfunc init() { fmt.Println(\"z init\") }\n")}},
		},
	}
	want := goRunMapped(t, m)
	if got := runMapped(t, m); got != want {
		t.Fatalf("interp:\n%sgo run:\n%s", got, want)
	}
	if want != "z init\n2 2 3 1 4 kind 2\n10 6\n4\n7\n" {
		t.Fatalf("go run = %q", want)
	}
}
