// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build windows

package winmode

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// TestSetGetFile pins the round trip through a real DACL: what Set writes
// is what Get reads, for the modes a shell script actually sets.
func TestSetGetFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	if _, ok := Get(path); ok {
		t.Fatal("a file nobody has chmod'ed must report no recorded mode")
	}
	for _, mode := range []fs.FileMode{
		0o644, 0o755, 0o600, 0o444, 0o400, 0o222, 0o000, 0o777, 0o700,
		0o644 | fs.ModeSetuid,
		0o755 | fs.ModeSetgid,
		0o644 | fs.ModeSticky,
		0o755 | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky,
	} {
		if err := Set(path, mode); err != nil {
			t.Fatalf("Set(%v): %v", mode, err)
		}
		got, ok := Get(path)
		if !ok {
			t.Fatalf("Set(%v) wrote a mode Get cannot find", mode)
		}
		if got != mode {
			t.Errorf("Set(%v) read back as %v", mode, got)
		}
	}
}

// TestSetGetDir pins the same round trip for a directory, whose write
// permission carries an extra right.
func TestSetGetDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d")
	if err := os.Mkdir(path, 0o777); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []fs.FileMode{0o755, 0o700, 0o555, 0o777 | fs.ModeSticky} {
		if err := Set(path, mode); err != nil {
			t.Fatalf("Set(%v): %v", mode, err)
		}
		got, ok := Get(path)
		if !ok {
			t.Fatalf("Set(%v) wrote a mode Get cannot find", mode)
		}
		if got != mode {
			t.Errorf("Set(%v) read back as %v", mode, got)
		}
	}
}

// TestEnforced is the reason the mode goes into the ACL rather than into a
// note on the side: the operating system has to refuse the open. This is
// the redir12.sub case — `chmod a-r f` then a redirection from f, and
// `chmod a-w f` then a redirection into it.
func TestEnforced(t *testing.T) {
	dir := t.TempDir()
	unreadable := filepath.Join(dir, "unreadable")
	unwritable := filepath.Join(dir, "unwritable")
	for _, p := range []string{unreadable, unwritable} {
		if err := os.WriteFile(p, []byte("x"), 0o666); err != nil {
			t.Fatal(err)
		}
	}
	if err := Set(unreadable, 0o222); err != nil {
		t.Fatal(err)
	}
	if err := Set(unwritable, 0o444); err != nil {
		t.Fatal(err)
	}
	if f, err := os.Open(unreadable); err == nil {
		f.Close()
		t.Error("a file with no read permission opened for reading")
	} else if !os.IsPermission(err) {
		t.Errorf("opening an unreadable file failed with %v, want a permission error", err)
	}
	if f, err := os.OpenFile(unwritable, os.O_WRONLY, 0); err == nil {
		f.Close()
		t.Error("a file with no write permission opened for writing")
	} else if !os.IsPermission(err) {
		t.Errorf("opening an unwritable file failed with %v, want a permission error", err)
	}
	// The owner keeps the rights chmod needs, so a mode is never a
	// one-way door: 0000 must still be chmod-able back to 0644.
	if err := Set(unreadable, 0o000); err != nil {
		t.Fatal(err)
	}
	if err := Set(unreadable, 0o644); err != nil {
		t.Fatalf("a file at mode 0000 could not be chmod'ed back by its owner: %v", err)
	}
	if f, err := os.Open(unreadable); err != nil {
		t.Errorf("opening the restored file failed: %v", err)
	} else {
		f.Close()
	}
}

// TestApplyStat pins the stat-layer reader: the mode a FileInfo reports
// after Apply is the recorded one, and everything else it reports is still
// the platform's.
func TestApplyStat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("abc"), 0o666); err != nil {
		t.Fatal(err)
	}
	plain, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if Apply(path, plain) != plain {
		t.Error("Apply must return the original FileInfo when no mode is recorded")
	}
	if err := Set(path, 0o751|fs.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	info := Apply(path, plain)
	if !Recorded(info) {
		t.Fatal("Apply did not substitute a recorded mode")
	}
	if got, want := info.Mode(), fs.FileMode(0o751|fs.ModeSetgid); got != want {
		t.Errorf("Mode is %v, want %v", got, want)
	}
	if info.Size() != plain.Size() || info.Name() != plain.Name() {
		t.Error("Apply changed something other than the mode")
	}
	if got := Overlay(path, plain.Mode()); got != 0o751|fs.ModeSetgid {
		t.Errorf("Overlay is %v, want %v", got, fs.FileMode(0o751|fs.ModeSetgid))
	}
}
