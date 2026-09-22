// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package winmode

import (
	"io/fs"
	"testing"
	"time"
)

// TestSpecRoundTrip pins that every one of the 512 permission modes comes
// back out of the access masks it goes in as. This is the mapping the whole
// scheme rests on, and it is pure, so it is checked on every host rather
// than only where the ACL is real.
func TestSpecRoundTrip(t *testing.T) {
	for _, dir := range []bool{false, true} {
		for perm := 0; perm < 0o1000; perm++ {
			mode := fs.FileMode(perm)
			spec := specOf(mode, dir)
			got := fs.FileMode(permOf(spec.ownerAllow)<<6 |
				permOf(spec.groupAllow)<<3 |
				permOf(spec.otherAllow))
			if got != mode {
				t.Fatalf("dir=%v mode %04o round-tripped as %04o", dir, perm, got)
			}
		}
	}
}

// TestSpecMarker pins the setuid/setgid/sticky trio into and out of the
// marker ACE's access mask, including that a mask with none of them still
// says "a mode was recorded here".
func TestSpecMarker(t *testing.T) {
	for _, mode := range []fs.FileMode{
		0, fs.ModeSetuid, fs.ModeSetgid, fs.ModeSticky,
		fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky,
	} {
		spec := specOf(0o644|mode, false)
		got, ok := markerMode(spec.marker)
		if !ok {
			t.Fatalf("mode %v: marker %#x not recognized", mode, spec.marker)
		}
		if got != mode {
			t.Errorf("mode %v round-tripped as %v", mode, got)
		}
	}
	if _, ok := markerMode(0); ok {
		t.Error("an empty mask must not be read as a recorded mode")
	}
}

// TestSpecDeny pins the deny masks, which are what makes the owner class
// win over the group and other classes for an account that is in all three
// — the owner of a file is almost always a member of its group and always a
// member of Everyone. The interesting modes are the ones where a later
// class is more permissive than an earlier one.
func TestSpecDeny(t *testing.T) {
	for _, tc := range []struct {
		mode                 fs.FileMode
		ownerDeny, groupDeny uint32
	}{
		// Classes that only shrink need no deny at all.
		{0o644, 0, 0},
		{0o755, 0, 0},
		{0o000, 0, 0},
		{0o777, 0, 0},
		{0o444, 0, 0},
		// The owner lacks the x that the group and other classes have.
		{0o655, maskExec, 0},
		// The owner lacks x; the group lacks everything other has.
		{0o607, maskExec, maskRead | maskWrite | maskExec},
		// The owner has nothing and the other two have rw.
		{0o066, maskRead | maskWrite, 0},
		// Only the other class can write.
		{0o442, maskWrite, maskWrite},
	} {
		spec := specOf(tc.mode, false)
		if spec.ownerDeny != tc.ownerDeny || spec.groupDeny != tc.groupDeny {
			t.Errorf("mode %04o: deny masks %#x/%#x, want %#x/%#x",
				tc.mode, spec.ownerDeny, spec.groupDeny, tc.ownerDeny, tc.groupDeny)
		}
		// Whatever a class is denied must be absent from what it is
		// allowed; otherwise one ACE would contradict the next.
		if spec.ownerDeny&spec.ownerAllow != 0 {
			t.Errorf("mode %04o: owner is both allowed and denied %#x",
				tc.mode, spec.ownerDeny&spec.ownerAllow)
		}
		if spec.groupDeny&spec.groupAllow != 0 {
			t.Errorf("mode %04o: group is both allowed and denied %#x",
				tc.mode, spec.groupDeny&spec.groupAllow)
		}
	}
}

// TestSpecDirectoryWrite pins that write on a directory carries the right
// to remove entries from it, and that write on a file does not.
func TestSpecDirectoryWrite(t *testing.T) {
	if specOf(0o200, false).ownerAllow&fileDeleteChild != 0 {
		t.Error("a writable file must not carry FILE_DELETE_CHILD")
	}
	if specOf(0o200, true).ownerAllow&fileDeleteChild == 0 {
		t.Error("a writable directory must carry FILE_DELETE_CHILD")
	}
	if specOf(0o500, true).ownerAllow&fileDeleteChild != 0 {
		t.Error("a read-only directory must not carry FILE_DELETE_CHILD")
	}
}

// TestSpecAlwaysMasks pins that the rights every class keeps regardless of
// the mode are never counted as permissions on the way back out — a mode of
// 0000 must read back as 0000 and not as 0444.
func TestSpecAlwaysMasks(t *testing.T) {
	spec := specOf(0, false)
	if got := permOf(spec.otherAllow | maskAlways); got != 0 {
		t.Errorf("other class of mode 0000 reads back as %o, want 0", got)
	}
	if got := permOf(spec.ownerAllow | maskAlways | maskOwnerAlways); got != 0 {
		t.Errorf("owner class of mode 0000 reads back as %o, want 0", got)
	}
}

// TestOverlayAndApply pin the two readers a stat layer uses. Off Windows
// nothing is ever recorded, so both are the identity — which is exactly the
// contract a cross-platform caller relies on.
func TestOverlayAndApply(t *testing.T) {
	const m = fs.ModeDir | 0o755
	if got := Overlay("nonexistent", m); got != m {
		t.Errorf("Overlay of an unrecorded path returned %v, want %v", got, m)
	}
	info := fakeInfo{mode: m}
	if got := Apply("nonexistent", info); got != fs.FileInfo(info) {
		t.Errorf("Apply of an unrecorded path returned %v, want the original", got)
	}
	if Recorded(info) {
		t.Error("a plain FileInfo must not report a recorded mode")
	}
	if Recorded(modeInfo{FileInfo: info, mode: m}) != true {
		t.Error("an overlaid FileInfo must report a recorded mode")
	}
	if Apply("nonexistent", nil) != nil {
		t.Error("Apply of a nil FileInfo must stay nil")
	}
}

type fakeInfo struct{ mode fs.FileMode }

func (f fakeInfo) Name() string       { return "fake" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return nil }
