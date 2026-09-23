// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build windows

package winmode

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
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

// TestIsOwnerAndInGroup pins the two questions `test -O` and `test -G` ask.
// A file this process just created is its own and carries its own primary
// group; a path that does not exist answers neither.
func TestIsOwnerAndInGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	owned, ok := IsOwner(path)
	if !ok {
		t.Fatal("a file this process created has no owner to compare")
	}
	if !owned {
		t.Error("a file this process created is not owned by it")
	}
	member, ok := InGroup(path)
	if !ok {
		t.Fatal("a file this process created has no group to compare")
	}
	if !member {
		t.Error("a file this process created is not in any of its groups")
	}
	missing := filepath.Join(t.TempDir(), "gone")
	if _, ok := IsOwner(missing); ok {
		t.Error("IsOwner claims to know the owner of a missing file")
	}
	if _, ok := InGroup(missing); ok {
		t.Error("InGroup claims to know the group of a missing file")
	}
}

// TestBash53FixtureInGroupProbe reproduces the layout and operation behind
// test.tests' `touch /tmp/test.group; chgrp ${GROUPS[0]}` on Windows. The
// Windows chgrp applet succeeds without changing the native primary group, so
// a new file has the same group state as the fixture. Keep the complete SID
// evidence in verbose test output: the
// distinction between token identity and access-enabled membership is exactly
// what this regression needs to expose on a hosted Windows runner.
func TestBash53FixtureInGroupProbe(t *testing.T) {
	tree, err := os.MkdirTemp("", "bash53-group-probe-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tree)
	tmp := filepath.Join(tree, "tmp")
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tmp, "test.group")
	if err := os.WriteFile(path, nil, 0o666); err != nil {
		t.Fatal(err)
	}

	owner, group, ok := ownerGroup(path)
	if !ok || owner == nil || group == nil {
		t.Fatalf("fixture file security descriptor: owner=%s group=%s ok=%t", sidText(owner), sidText(group), ok)
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	primary, err := token.GetTokenPrimaryGroup()
	if err != nil {
		t.Fatal(err)
	}
	groups, err := token.GetTokenGroups()
	if err != nil {
		t.Fatal(err)
	}
	accessMember, accessErr := token.IsMember(group)
	identityMember, identityErr := tokenHasGroup(token, group)
	inGroup, known := InGroup(path)

	var tokenGroups []string
	for _, entry := range groups.AllGroups() {
		tokenGroups = append(tokenGroups, fmt.Sprintf("%s attributes=%#x", sidText(entry.Sid), entry.Attributes))
	}
	t.Logf("fixture path=%s", path)
	t.Logf("file owner=%s group=%s", sidText(owner), sidText(group))
	t.Logf("token user=%s primary-group=%s", sidText(user.User.Sid), sidText(primary.PrimaryGroup))
	t.Logf("token groups:\n  %s", strings.Join(tokenGroups, "\n  "))
	t.Logf("group access-member=%t err=%v identity-member=%t err=%v test-G=%t known=%t",
		accessMember, accessErr, identityMember, identityErr, inGroup, known)

	if identityErr != nil {
		t.Fatalf("read token group identity: %v", identityErr)
	}
	if !identityMember {
		t.Fatalf("file group %s is absent from both the token primary group and token groups", group)
	}
	if !known || !inGroup {
		t.Fatalf("test -G result = %t, %t; want true for token group %s", inGroup, known, group)
	}
}

func sidText(sid *windows.SID) string {
	if sid == nil {
		return "<nil>"
	}
	return sid.String()
}
