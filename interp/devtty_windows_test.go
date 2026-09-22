//go:build windows

package interp

import (
	"io/fs"
	"os"
	"testing"
)

// /dev/tty on Windows: always a character device to stat, the console to open.
func TestDevTTYWindowsMapping(t *testing.T) {
	info, ok := devTTYStat("/dev/tty")
	if !ok || info.Mode()&fs.ModeCharDevice == 0 {
		t.Fatalf("stat /dev/tty = %v, %v", info, ok)
	}
	if _, ok := devTTYStat("/dev/ttyS0"); ok {
		t.Fatal("only /dev/tty itself is synthesized")
	}
	if p, ok := devTTYConsolePath("/dev/tty", os.O_RDONLY); !ok || p != `CONIN$` {
		t.Fatalf("read open = %q, %v", p, ok)
	}
	if p, _ := devTTYConsolePath("/dev/tty", os.O_WRONLY|os.O_APPEND); p != `CONOUT$` {
		t.Fatalf("write open = %q", p)
	}
	if _, ok := devTTYConsolePath("/dev/null", os.O_RDONLY); ok {
		t.Fatal("/dev/null is not the console")
	}
	if got := userGroups(); len(got) == 0 || got[0] == "-1" {
		t.Fatalf("GROUPS on Windows = %v; want at least one non-negative group", got)
	}
}
