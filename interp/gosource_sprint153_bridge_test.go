package interp_test

// Sprint: #153; Story: S153.4; Story-ID: e58cccba74f8
//
// Dependency-bridge value shapes. Every case is an outside-corpus reproducer
// under testdata/sprint153/<mechanism>/: the unchanged original source runs
// through the interpreter and is compared against a real Go build of the same
// file on exact stdout, stderr and exit status. differGoSource lives in
// bashpp_native_structured_test.go.

import (
	"os"
	"path/filepath"
	"testing"
)

// differSprint153 runs every .go program in one mechanism directory through
// the native-Go differ.
func differSprint153(t *testing.T, mechanism string) {
	t.Helper()
	dir := filepath.Join("testdata", "sprint153", mechanism)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	ran := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
			continue
		}
		ran++
		source, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		t.Run(entry.Name(), func(t *testing.T) { differGoSource(t, string(source), nil, "") })
	}
	if ran == 0 {
		t.Fatalf("no reproducers in %s", dir)
	}
}

// TestGoSourceBridgeLocalInterfaceDecl covers locally declared interfaces with
// method specifications: the helper materialises them as real declarations, so
// a struct field of that interface type no longer breaks the worker build.
func TestGoSourceBridgeLocalInterfaceDecl(t *testing.T) {
	differSprint153(t, "local-interface-decl")
}
