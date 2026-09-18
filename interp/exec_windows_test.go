//go:build windows

package interp_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// A relative executable (./sub/x.exe) after a `cd` inside the shell must run:
// bashy hands scripts /c/… paths, and CreateProcess resolves a relative
// application path against the parent's cwd, not Cmd.Dir.
func TestWindowsRelativeExecAfterCd(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(`C:\Windows\System32\where.exe`)
	if err != nil {
		t.Skip("no where.exe")
	}
	exe := filepath.Join(sub, "w.exe")
	if err := os.WriteFile(exe, src, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, form := range []string{"./sub/w.exe", "sub/w.exe", `"sub\w.exe"`} {
		t.Run(form, func(t *testing.T) {
			var out, errb strings.Builder
			r, _ := interp.New(interp.StdIO(nil, &out, &errb), interp.Dir(dir))
			script := "cd " + filepath.ToSlash(dir) + " && " + form + " /Q where.exe; echo rc=$?"
			f, perr := syntax.NewParser().Parse(strings.NewReader(script), "")
			if perr != nil {
				t.Fatal(perr)
			}
			_ = r.Run(context.Background(), f)
			if !strings.Contains(out.String(), "rc=0") {
				t.Fatalf("form %q: out=%q err=%q", form, out.String(), errb.String())
			}
		})
	}
}
