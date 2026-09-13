package lower

import (
	"go/types"
	"os"
	"path/filepath"
	"testing"

	"mvdan.cc/sh/v3/gosource"
)

// Sprint 165 S165.2: the module importer offers gosource.IdentityImporter —
// the policy-free resolution the explicit package map asks for after it has
// decided internal visibility on the importer's declared identity. The
// directory rule of ImportFrom is unchanged for every other caller.
func TestModuleImporterIdentityImporter(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module scratch\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	imp := NewModuleImporter(dir)
	by, ok := imp.(gosource.IdentityImporter)
	if !ok {
		t.Fatal("the module importer does not offer gosource.IdentityImporter")
	}
	// The directory rule still refuses from a scratch directory outside GOROOT.
	if _, err := imp.(types.ImporterFrom).ImportFrom("internal/buildcfg", dir, 0); err == nil || err.Error() != "use of internal package internal/buildcfg not allowed" {
		t.Fatalf("ImportFrom = %v, want the directory rule's refusal", err)
	}
	// The identity path resolves policy-free: the map decided.
	for _, path := range []string{"internal/buildcfg", "cmd/internal/src", "testing/internal/testdeps", "internal/runtime/sys"} {
		pkg, err := by.ImportFromPackage(path, "cmd/compile/internal/base.test", dir)
		if err != nil || pkg == nil || pkg.Path() != path {
			t.Fatalf("ImportFromPackage(%q) = %v, %v", path, pkg, err)
		}
	}
	// And is the same package object the plain path yields for a reviewed import.
	a, err := by.ImportFromPackage("strings", "example.com/app", dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := imp.Import("strings")
	if err != nil || a != b {
		t.Fatalf("Import(strings) = %v, %v; identity path gave %v", b, err, a)
	}
}
