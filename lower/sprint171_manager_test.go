package lower_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/lower"
)

// TestGoSourceDotImportsKeepEveryPath is the reproducer behind the Sprint
// 171 gosource-units request: a dot or blank import binds no identifier, so
// the emitter keyed two `import . "..."` lines under one alias and the second
// replaced the first; the corpus canary bug191 (two dot-imported mapped
// packages) regressed once a directory phase lowered to a native unit.
func TestGoSourceDotImportsKeepEveryPath(t *testing.T) {
	path := filepath.Join("testdata", "sprint171", "manager", "dot_imports.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	program, err := gosource.Parse(bytes.NewReader(source), path, gosource.Options{RunMain: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := lower.Compile(program.File, lower.Options{Origin: path})
	if err != nil {
		t.Fatal(err)
	}
	generated := string(result.Source)
	for _, want := range []string{`import . "fmt"`, `import . "strings"`, `import _ "embed"`, `import _ "unsafe"`} {
		if !strings.Contains(generated, want) {
			t.Errorf("generated Go lost %s:\n%s", want, generated)
		}
	}
}
