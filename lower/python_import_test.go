package lower_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/lower"
	"mvdan.cc/sh/v3/syntax"
)

func TestPythonImportPlansWithoutImporting(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	t.Setenv("BASHPP_PYTHON", python)
	dir := t.TempDir()
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader("import python \"does_not_exist.sprint183\" as missing\necho planned\n"), filepath.Join(dir, "source.bpp"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := lower.Compile(file, lower.Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ForeignImports) != 1 || result.ForeignImports[0].Module != "does_not_exist.sprint183" || result.ForeignImports[0].Alias != "missing" {
		t.Fatalf("foreign plans = %#v", result.ForeignImports)
	}
}

func TestPythonImportAliasCollision(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	t.Setenv("BASHPP_PYTHON", python)
	dir := t.TempDir()
	for _, source := range []string{
		"import python \"one.mod\" as py\nimport python \"two.mod\" as py\n",
		"func py() {}\nimport python \"one.mod\" as py\n",
	} {
		file, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(source), filepath.Join(dir, "source.bpp"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lower.Compile(file, lower.Options{Dir: dir}); err == nil || !strings.Contains(err.Error(), "collides") {
			t.Fatalf("Compile error = %v", err)
		}
	}
}
