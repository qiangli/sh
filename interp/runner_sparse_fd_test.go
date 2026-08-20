//go:build unix

package interp

import (
	"os"
	"testing"
)

func TestExecExtraFilesPreservesSparseNumber(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	r := &Runner{fdTable: map[int]*os.File{8: f}}
	extra, inherited, cleanup := r.execExtraFiles()
	defer cleanup()
	if got, want := len(extra), 6; got != want {
		t.Fatalf("ExtraFiles length = %d, want %d for child fds 3..8", got, want)
	}
	for fd := 3; fd < 8; fd++ {
		placeholder := extra[fd-3]
		if placeholder == nil {
			t.Fatalf("child fd %d placeholder is nil", fd)
		}
		if got := placeholder.Fd(); got != ^uintptr(0) {
			t.Fatalf("child fd %d placeholder is open as parent fd %d", fd, got)
		}
	}
	if extra[5] != f {
		t.Fatalf("child fd 8 = %p, want source file %p", extra[5], f)
	}
	if inherited != "8" {
		t.Fatalf("inherited descriptor list = %q, want %q", inherited, "8")
	}
}
