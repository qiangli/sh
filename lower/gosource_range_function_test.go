package lower_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func runRangeFunctionSource(t *testing.T, source string) (string, string, error) {
	t.Helper()
	dir := t.TempDir()
	program, err := gosource.Parse(strings.NewReader(source), filepath.Join(dir, "main.go"), gosource.Options{RunMain: true})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	runner, err := interp.New(interp.Lang(syntax.LangBashPP), interp.Dir(dir), interp.StdIO(nil, &stdout, &stderr))
	if err != nil {
		t.Fatal(err)
	}
	err = runner.Run(context.Background(), program.File)
	return stdout.String(), stderr.String(), err
}

func TestGoSourceRangeFunctionCallbackTransport(t *testing.T) {
	const source = `package main
import "fmt"
func pairs(yield func(int, string) bool) {
	for i := range 3 {
		if !yield(i, "value") { fmt.Println("stopped"); return }
	}
	fmt.Println("exhausted")
}
func main() {
	for i, value := range pairs {
		fmt.Println(i, value)
		if i == 1 { break }
	}
}`
	stdout, stderr, err := runRangeFunctionSource(t, source)
	if err != nil {
		t.Fatalf("Run: %v stdout=%q stderr=%q", err, stdout, stderr)
	}
	if stdout != "0 value\n1 value\nstopped\n" || stderr != "" {
		t.Fatalf("streams: stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestGoSourceRangeFunctionYieldAfterFalsePanics(t *testing.T) {
	const source = `package main
import "fmt"
func bad(yield func(int) bool) {
	if !yield(1) { yield(2) }
}
func main() {
	defer func() { fmt.Println("recovered:", recover()) }()
	for value := range bad {
		fmt.Println(value)
		break
	}
	fmt.Println("unreachable")
}`
	stdout, stderr, err := runRangeFunctionSource(t, source)
	if err != nil {
		t.Fatalf("Run: %v stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = "1\nrecovered: runtime error: range function continued iteration after function for loop body returned false\n"
	if stdout != want || stderr != "" {
		t.Fatalf("streams: stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestGoSourceRangeFunctionCallbackShapeBoundaries(t *testing.T) {
	t.Run("aggregate yield parameter", func(t *testing.T) {
		const source = `package main
func values(yield func([]int) bool) { println("iterator-ran"); yield([]int{1}) }
func main() { for range values { println("body-ran") } }`
		stdout, stderr, err := runRangeFunctionSource(t, source)
		if err == nil || !strings.Contains(err.Error()+stderr, "iterator callback requires scalar yield parameters") {
			t.Fatalf("missing boundary: %v stdout=%q stderr=%q", err, stdout, stderr)
		}
		if strings.Contains(stdout+stderr, "iterator-ran") || strings.Contains(stdout+stderr, "body-ran") {
			t.Fatalf("unsupported callback executed: stdout=%q stderr=%q", stdout, stderr)
		}
	})

	for name, source := range map[string]string{
		"non-bool yield result":  `package main; func bad(yield func(int) int) {}; func main() { for range bad {} }`,
		"three yield parameters": `package main; func bad(yield func(int, int, int) bool) {}; func main() { for range bad {} }`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := gosource.Parse(strings.NewReader(source), "main.go", gosource.Options{RunMain: true}); err == nil {
				t.Fatal("invalid Go range callback shape was accepted")
			}
		})
	}
}

func TestGoSourceGbERangeIteratorsInterpreted(t *testing.T) {
	source, err := os.ReadFile("testdata/gosource-gbe/range-over-iterators.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runRangeFunctionSource(t, string(source))
	if err != nil {
		t.Fatalf("Run: %v stdout=%q stderr=%q", err, stdout, stderr)
	}
	const want = "10\n13\n23\nall: [10 13 23]\npart: go\npart: by\npart: example\n0\n1\n1\n2\n3\n5\n8\n"
	if stdout != want || stderr != "" {
		t.Fatalf("streams: stdout=%q stderr=%q", stdout, stderr)
	}
}
