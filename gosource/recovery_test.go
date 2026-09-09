package gosource_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// Mirror the pinned SDK's go/types/check_test.go parseFiles -> testFiles flow,
// independently of the product converter and its AST bookkeeping. Test-only
// assert builtins and fixture importers are deliberately not supplied here.
func nativeRecoveryDiagnostics(t *testing.T, sources []gosource.Source) []string {
	t.Helper()
	sources = append([]gosource.Source(nil), sources...)
	sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	fset := token.NewFileSet()
	var files []*ast.File
	var errors []string
	for _, s := range sources {
		file, err := parser.ParseFile(fset, s.Name, s.Data, parser.AllErrors|parser.SkipObjectResolution)
		if file == nil {
			t.Fatal("native parser returned nil AST", err)
		}
		files = append(files, file)
		if list, ok := err.(scanner.ErrorList); ok {
			for _, e := range list {
				errors = append(errors, e.Error())
			}
		} else if err != nil {
			errors = append(errors, err.Error())
		}
	}
	conf := types.Config{Importer: importer.Default(), Error: func(err error) { errors = append(errors, err.Error()) }}
	conf.Check(files[0].Name.Name, fset, files, nil)
	sort.Strings(errors)
	return errors
}

func TestGoSourceDiagnosticRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, sha string
		minTypes  int
	}{
		{"expr3", "09291a9472f94a3001a74d01f46aa25fbb8a0490973c803c3dbbd13f0087aad9", 100},
		{"stmt0", "29472d473c7ed9ad26b3d762837e8c1757b85473637a8ab28f12510a8ad249a0", 100},
		{"issue43190", "def735fe9882adcc1af85ce2d59c3a97e0a3444cc054575719f9cb9cef32c02a", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join("testdata", "recovery", tc.name+".go.txt")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != tc.sha {
				t.Fatalf("original source changed: %s", got)
			}
			source := gosource.Source{Name: tc.name + ".go", Data: data}
			want := nativeRecoveryDiagnostics(t, []gosource.Source{source})
			for _, runMain := range []bool{false, true} {
				p, err := gosource.Load([]gosource.Source{source}, gosource.Options{RunMain: runMain})
				if p != nil || err == nil {
					t.Fatalf("malformed AST exposed: program=%v error=%v", p, err)
				}
				list, ok := err.(gosource.ErrorList)
				if !ok {
					t.Fatalf("diagnostics lost types: %T", err)
				}
				var got []string
				parseCount, typeCount := 0, 0
				for _, e := range list {
					got = append(got, e.Error())
					switch e := e.(type) {
					case *scanner.Error:
						parseCount++
						if e.Pos.Filename != source.Name || e.Pos.Line < 1 {
							t.Fatalf("parser position: %v", e)
						}
					case types.Error:
						typeCount++
						pos := e.Fset.Position(e.Pos)
						if pos.Filename != source.Name || pos.Line < 1 {
							t.Fatalf("type position: %v", e)
						}
					default:
						t.Fatalf("unpositioned diagnostic: %T %v", e, e)
					}
				}
				if parseCount == 0 || typeCount < tc.minTypes {
					t.Fatalf("missing recovery diagnostics: parser=%d types=%d", parseCount, typeCount)
				}
				sort.Strings(got)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("diagnostics differ from native recovery flow\ngot: %q\nwant: %q", got, want)
				}
				if tc.name == "issue43190" && !strings.Contains(err.Error(), "issue43190.go:11:8: invalid import path (empty string)") {
					t.Fatalf("missing later empty import: %v", err)
				}
				if fmt.Sprintf("%x", sha256.Sum256(data)) != tc.sha {
					t.Fatal("load changed source bytes")
				}
				t.Logf("RunMain=%v: %d parser + %d semantic diagnostics; nil program", runMain, parseCount, typeCount)
			}
		})
	}
}

func TestGoSourceDiagnosticsRecoveryAcrossFiles(t *testing.T) {
	sources := []gosource.Source{
		{Name: "d.go", Data: []byte("package other\nvar D = ignoredWithWrongPackage\n")},
		{Name: "c.go", Data: []byte("package p\nvar C = missingC\n")},
		{Name: "a.go", Data: []byte("package p\nimport ;\nvar A = missingA\n")},
		{Name: "b.go", Data: []byte("package p\nvar B int = \"wrong\"\n")},
	}
	want := nativeRecoveryDiagnostics(t, sources)
	p, err := gosource.Load(sources, gosource.Options{RunMain: true})
	if p != nil || err == nil {
		t.Fatalf("malformed package exposed: %v %v", p, err)
	}
	var got []string
	for _, e := range err.(gosource.ErrorList) {
		got = append(got, e.Error())
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lost/duplicated sibling diagnostics\ngot%q\nwant%q", got, want)
	}
	for _, needle := range []string{"a.go:2:", "a.go:3:", "b.go:2:", "c.go:2:", "d.go:1:"} {
		if !strings.Contains(err.Error(), needle) {
			t.Fatalf("missing %s: %v", needle, err)
		}
	}
	// Fatal package-clause recovery has an empty AST: still fail cleanly, never
	// call conversion, and permit a later independent valid load to run normally.
	for _, bad := range []string{"", "! not Go", "package", "package p\nfunc f( {"} {
		p, err := gosource.Parse(strings.NewReader(bad), "bad.go", gosource.Options{RunMain: true})
		if p != nil || err == nil {
			t.Fatalf("bad source accepted: %q %v %v", bad, p, err)
		}
	}
	valid := []gosource.Source{{Name: "b.go", Data: []byte("package main\nfunc answer()int{return 42}\n")}, {Name: "a.go", Data: []byte("package main\nfunc main(){println(answer())}\n")}}
	p, err = gosource.Load(valid, gosource.Options{RunMain: true})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	r, err := interp.New(interp.Lang(syntax.LangBashPP), interp.StdIO(nil, &output, &output))
	if err != nil {
		t.Fatal(err)
	}
	if err = r.Run(context.Background(), p.File); err != nil || output.String() != "42\n" {
		t.Fatalf("valid load after errors: %v %q", err, output.String())
	}
}
