//go:build full

package interp

// Sprint: #319; Story: #1087; Story-ID: 96cd7f0c400d

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/syntax"
)

// TestS319SyntaxParseThenFdump keeps the real compiler-syntax AST between the
// parser and its reflection-based dumper. Small hand-built Expr graphs miss
// the parser-grown []Expr capacity tail whose nil interface used to cross as
// a scalar string in an Expr slot.
func TestS319SyntaxParseThenFdump(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("..", "gosource", "internal", "gcsyntax"))
	if err != nil {
		t.Fatal(err)
	}
	var sources []gosource.Source
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		name := filepath.Join("..", "gosource", "internal", "gcsyntax", entry.Name())
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, gosource.Source{Name: name, Data: data})
	}
	parsedSource := []byte("package p\nfunc f() { h(a(), b.c, d) }\n")
	main := gosource.Source{Name: "main.go", Data: []byte(fmt.Sprintf(`package main
import (
	"io"
	"strings"
	"test/syntax"
)
func main() {
	ast, first := syntax.Parse(syntax.NewFileBase("small.go"), strings.NewReader(%q), nil, nil, syntax.CheckBranches)
	if first != nil { panic(first) }
	if err := syntax.Fdump(io.Discard, ast); err != nil { panic(err) }
}
`, parsedSource))}
	program, err := gosource.Load([]gosource.Source{main}, gosource.Options{
		RunMain:  true,
		Packages: []gosource.PackageSpec{{Path: "test/syntax", Sources: sources}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	runner, err := New(Lang(syntax.LangBashPP), Dir(t.TempDir()), StdIO(nil, &stdout, &stderr))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := runner.Run(ctx, program.File); err != nil {
		t.Fatalf("run: %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
}

func TestS319ReflectedInterfaceSliceCapacityTail(t *testing.T) {
	source := gosource.Source{Name: "main.go", Data: []byte(`package main
import "reflect"
type E interface{ m() }
type A struct{}
type Root struct{ X []E }
func (*A) m() {}
func main() {
	var xs []E
	for i := 0; i < 3; i++ { xs = append(xs, &A{}) }
	_ = reflect.ValueOf(&Root{X: xs})
}
`)}
	program, err := gosource.Load([]gosource.Source{source}, gosource.Options{RunMain: true})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := New(Lang(syntax.LangBashPP), Dir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), program.File); err != nil {
		t.Fatal(err)
	}
}
