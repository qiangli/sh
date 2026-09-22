// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build windows

package interp

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"mvdan.cc/sh/v3/winmode"
)

// A recorded mode decides what a redirection may open, for reads as well
// as for writes. Windows refuses the write half on its own (chmod clears
// the read-only attribute with the w bit), so before this the two halves
// of redir12.sub disagreed: `> unwritable-file` failed and `<
// unreadable-file` succeeded.
func TestStory687RecordedModeDeniesOpen(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o666); err != nil {
			t.Fatal(err)
		}
		return path
	}
	unrecorded := write("plain")
	for _, flag := range []int{os.O_RDONLY, os.O_WRONLY, os.O_RDWR} {
		if err := recordedModeDenies(unrecorded, flag); err != nil {
			t.Errorf("flag %d on a file nobody chmod'ed: %v", flag, err)
		}
	}
	for _, tc := range []struct {
		mode                       fs.FileMode
		read, writeOnly, readWrite bool // whether the open is denied
	}{
		{0o600, false, false, false},
		{0o400, false, true, true},
		{0o200, true, false, true},
		{0o000, true, true, true},
	} {
		path := write(tc.mode.String())
		if err := winmode.Set(path, tc.mode); err != nil {
			t.Fatalf("Set(%v): %v", tc.mode, err)
		}
		for _, sub := range []struct {
			flag       int
			wantDenied bool
		}{
			{os.O_RDONLY, tc.read},
			{os.O_WRONLY, tc.writeOnly},
			{os.O_RDWR, tc.readWrite},
		} {
			err := recordedModeDenies(path, sub.flag)
			if denied := err != nil; denied != sub.wantDenied {
				t.Errorf("mode %v flag %d: denied=%v, want %v (%v)",
					tc.mode, sub.flag, denied, sub.wantDenied, err)
			}
			if err != nil && !os.IsPermission(err) {
				t.Errorf("mode %v flag %d: %v is not a permission error", tc.mode, sub.flag, err)
			}
		}
	}
}
