package interp

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBashPPImportScratchOverlay(t *testing.T) {
	root := t.TempDir()
	module := filepath.Join(root, "module")
	scratch := filepath.Join(root, "private")
	for _, dir := range []string{filepath.Join(module, "internal", "dep"), scratch} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{"go.mod": "module example.test/overlay\n\ngo 1.26.5\n", "internal/dep/dep.go": "package dep\nconst Value = \"private dependency\"\n"}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(module, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// A read-only original module must remain usable; only scratch is writable.
	if err := os.Chmod(module, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(module, 0700) })
	alias := filepath.Join(root, "module-alias")
	if err := os.Symlink(module, alias); err != nil {
		t.Fatal(err)
	}
	env := setEnvString(os.Environ(), "TMPDIR", scratch)
	env = setEnvString(env, "GOWORK", "off")
	f, err := bashPPImportTempSource(alias, "helper-*.go", env)
	if err != nil {
		t.Fatal(err)
	}
	defer f.cleanup()
	if _, err = f.WriteString("package main\nimport(\"fmt\";\"example.test/overlay/internal/dep\")\nfunc main(){fmt.Print(dep.Value)}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err = os.Stat(filepath.Dir(f.buildPath)); !os.IsNotExist(err) {
		t.Fatalf("virtual directory exists: %v", err)
	}
	info, err := os.Stat(f.Name())
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatalf("private input mode: %v %v", info, err)
	}
	binary := f.Name() + ".bin"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	cmd := exec.Command(filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-p=2", "-overlay="+f.overlay, "-o", binary, f.buildPath)
	cmd.Dir = alias
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("real overlay build: %v\n%s", err, out)
	}
	if out, err := exec.Command(binary).CombinedOutput(); err != nil || string(out) != "private dependency" {
		t.Fatalf("real artifact: %v %q", err, out)
	}
	for name, body := range files {
		if got, err := os.ReadFile(filepath.Join(module, name)); err != nil || string(got) != body {
			t.Fatalf("original mutated: %s", name)
		}
	}
	entries, err := os.ReadDir(module)
	if err != nil || len(entries) != 2 {
		t.Fatalf("source tree entries: %v %v", entries, err)
	}
	f.cleanup()
	entries, err = os.ReadDir(scratch)
	if err != nil || len(entries) != 0 {
		t.Fatalf("scratch not cleaned: %v %v", entries, err)
	}
}

func TestBashPPImportScratchRejectsSourceTMPDIR(t *testing.T) {
	root := t.TempDir()
	for _, tmp := range []string{root, filepath.Join(root, "missing")} {
		f, err := bashPPImportTempSource(root, "helper-*.go", setEnvString(os.Environ(), "TMPDIR", tmp))
		if err == nil {
			f.Close()
			f.cleanup()
			t.Fatal("source TMPDIR accepted")
		}
		if tmp == root && !strings.Contains(err.Error(), "inside the source") {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed request altered source: %v %v", entries, err)
	}
}
