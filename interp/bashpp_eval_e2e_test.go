// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

// End-to-end evidence for the P2 capability policy, through a real runner and
// the reviewed Go toolchain. The classification tests elsewhere in this
// package are pure functions over go-list facts; these run the whole path.

func writeBashPPFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o666); err != nil {
		t.Fatal(err)
	}
}

// runBashPPInDir runs src through a default (policy) runner rooted at dir.
func runBashPPInDir(t *testing.T, dir, src string) (string, string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), Dir(dir), Lang(syntax.LangBashPP))
	if err != nil {
		t.Fatal(err)
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "t.bpp")
	if err != nil {
		t.Fatal(err)
	}
	runErr := r.Run(context.Background(), f)
	return stdout.String(), stderr.String(), runErr
}

func bashPPLocalModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeBashPPFile(t, filepath.Join(dir, "go.mod"), "module example.com/m\n\ngo 1.27.0\n")
	writeBashPPFile(t, filepath.Join(dir, "greet", "greet.go"),
		"package greet\n\nimport \"fmt\"\n\nfunc Hello(who string) { fmt.Println(\"hello,\", who) }\n")
	return dir
}

// TestBashPPLocalModuleRunsThroughPolicy proves the capNativeOnly path end to
// end: a local-module package is classified at import time, passes policy, and
// is built and run by the reviewed toolchain.
func TestBashPPLocalModuleRunsThroughPolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a Go program with the reviewed toolchain")
	}
	dir := bashPPLocalModule(t)
	stdout, stderr, err := runBashPPInDir(t, dir, "import \"example.com/m/greet\"\ngreet.Hello(\"p2\")\n")
	if err != nil {
		t.Fatalf("run: %v (stderr %q)", err, stderr)
	}
	if stdout != "hello, p2\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "hello, p2\n")
	}
	// No interpreter is adopted, so the toolchain is the engine the policy
	// selects rather than a downgrade from one. Nothing should be announced;
	// see announce() in bashpp_eval.go.
	if stderr != "" {
		t.Fatalf("unexpected diagnostic: %q", stderr)
	}
}

// TestBashPPRefusedClassesFailAtImport is the diagnostic-timing evidence. Each
// refused class must fail on the line that NAMED the package, not later at an
// unrelated call site, and must leave the session registry untouched.
func TestBashPPRefusedClassesFailAtImport(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the reviewed Go toolchain")
	}
	dir := bashPPLocalModule(t)
	// package main: Go refuses to import it, and so must the policy.
	writeBashPPFile(t, filepath.Join(dir, "cmdmain", "main.go"), "package main\n\nfunc main() {}\n")
	// A platform mismatch: source exists, but nothing builds on this host.
	writeBashPPFile(t, filepath.Join(dir, "plan9only", "p.go"),
		"//go:build plan9 && ignoreme\n\npackage plan9only\n\nfunc F() {}\n")

	for _, test := range []struct {
		name, path, want string
	}{
		{"package main", "example.com/m/cmdmain", "not importable"},
		{"platform mismatch", "example.com/m/plan9only", "not importable"},
		{"missing", "example.com/m/absent", ""},
		{"unreviewed stdlib", "internal/abi", "reviewed Go standard library"},
	} {
		t.Run(test.name, func(t *testing.T) {
			src := "import " + strconv.Quote(test.path) + "\necho REACHED-NEXT-LINE\n"
			stdout, _, err := runBashPPInDir(t, dir, src)
			if err == nil {
				t.Fatalf("import %q was accepted", test.path)
			}
			if test.want != "" && !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %v, want it to mention %q", err, test.want)
			}
			// The failure must stop the script at the import.
			if strings.Contains(stdout, "REACHED-NEXT-LINE") {
				t.Fatalf("execution continued past a refused import: %q", stdout)
			}
		})
	}
}

// TestBashPPRefusedImportLeavesRegistryUntouched pins atomicity: a refused
// import must not half-populate the session namespace.
func TestBashPPRefusedImportLeavesRegistryUntouched(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the reviewed Go toolchain")
	}
	dir := bashPPLocalModule(t)
	writeBashPPFile(t, filepath.Join(dir, "cmdmain", "main.go"), "package main\n\nfunc main() {}\n")
	r, err := New(StdIO(nil, &bytes.Buffer{}, &bytes.Buffer{}), Dir(dir), Lang(syntax.LangBashPP))
	if err != nil {
		t.Fatal(err)
	}
	src := "import (\n\t\"example.com/m/greet\"\n\t\"example.com/m/cmdmain\"\n)\n"
	f, perr := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "t.bpp")
	if perr != nil {
		t.Fatal(perr)
	}
	if err := r.Run(context.Background(), f); err == nil {
		t.Fatal("grouped import containing a refused package was accepted")
	}
	if len(r.bashPPImports) != 0 {
		t.Fatalf("refused group left the registry populated: %#v", r.bashPPImports)
	}
}
