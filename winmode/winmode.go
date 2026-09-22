// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

// Package winmode keeps POSIX file permission bits on a platform that has
// none, by writing them into the file's discretionary ACL and reading them
// back out of it — the scheme Cygwin has used since it stopped pretending
// that the read-only attribute is a mode.
//
// # The representation
//
// A mode lives in the DACL as this ACE sequence, in this order:
//
//	deny  NULL SID (S-1-0-0)   marker + setuid/setgid/sticky
//	deny  owner SID            rights the group or other class has and the owner does not
//	allow owner SID            the owner class
//	deny  group SID            rights the other class has and the group does not
//	allow group SID            the group class
//	allow Everyone (S-1-1-0)   the other class
//
// The order is the whole point and it is why the ACL is assembled by hand
// rather than through SetEntriesInAcl, which canonicalizes every deny ahead
// of every allow. Windows walks a DACL in order and a grant is final, so
// putting the owner's allow before the group's deny is what lets a user who
// is both the owner and a member of the file's group keep the owner class —
// exactly the POSIX rule that the first matching class wins. Canonical
// ordering would instead let the group's deny take rights away from the
// owner, so `chmod 0607` would lock the owner out of their own file.
//
// The NULL SID matches no account, so its ACE grants and denies nothing; it
// is pure storage. Its access mask carries Cygwin's bits, and the marker bit
// is also the answer to "has anyone recorded a mode here at all" — a file
// nobody has chmod'ed has no marker, [Get] reports that, and the caller
// keeps whatever the platform's own attribute-derived mode said.
//
// # What cannot be represented, and is not pretended
//
//   - setuid, setgid and sticky are recorded and read back, so `chmod g+s f`
//     followed by `test -g f` agrees. They are not ENFORCED: Windows has no
//     notion of executing a program under the file owner's identity, and
//     nothing here makes one up.
//   - The group class is the file's Windows primary group, which is not a
//     POSIX gid. It is a real SID with real members, so denying it has a
//     real effect, but it does not answer "what is this file's gid".
//   - A file with no owner SID (some network filesystems) cannot hold a mode
//     at all; [Set] fails loudly rather than appearing to succeed.
package winmode

import (
	"io/fs"
	"os"
)

// The access mask bits of the NULL SID marker ACE. These are Cygwin's, with
// Cygwin's values, so that a mode written here reads back under Cygwin and
// the other way round.
const (
	cygACEISVTX    = 0x001 // fs.ModeSticky
	cygACEISGID    = 0x002 // fs.ModeSetgid
	cygACEISUID    = 0x004 // fs.ModeSetuid
	cygACENewStyle = 0x008 // "a mode was recorded here"
)

// modeBits are the bits of an fs.FileMode that a recorded mode owns; every
// other bit of a FileInfo's mode (the type bits) still comes from the
// platform.
const modeBits = fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky

// The access mask each POSIX permission maps to. The values are Win32's
// (winnt.h) and are spelled out here rather than taken from x/sys/windows
// so that the mapping is one table, compiled and tested on every host,
// instead of a Windows-only one nobody can run.
//
// The three file rights and the three directory rights share their numeric
// values — FILE_READ_DATA is FILE_LIST_DIRECTORY, FILE_WRITE_DATA is
// FILE_ADD_FILE, FILE_EXECUTE is FILE_TRAVERSE — so one table serves both.
// The single difference is that write on a directory also means removing
// entries from it.
const (
	fileReadData        = 0x00000001
	fileWriteData       = 0x00000002
	fileAppendData      = 0x00000004
	fileReadEA          = 0x00000008
	fileWriteEA         = 0x00000010
	fileExecute         = 0x00000020
	fileDeleteChild     = 0x00000040
	fileReadAttributes  = 0x00000080
	fileWriteAttributes = 0x00000100

	deleteAccess = 0x00010000
	readControl  = 0x00020000
	writeDAC     = 0x00040000
	writeOwner   = 0x00080000
	synchronize  = 0x00100000

	maskRead     = fileReadData | fileReadEA
	maskWrite    = fileWriteData | fileAppendData | fileWriteEA
	maskDirWrite = maskWrite | fileDeleteChild
	maskExec     = fileExecute

	// maskAlways is granted to every class whatever the mode says. POSIX
	// governs stat(2) by search permission on the parent directory, not by
	// the file's own mode, and these are the rights Windows wants for it;
	// READ_CONTROL additionally keeps the recorded mode readable, which a
	// mode of 0000 would otherwise put out of reach of this package.
	maskAlways = fileReadAttributes | readControl | synchronize

	// maskOwnerAlways is granted to the owner on top of maskAlways. These
	// are the rights chmod itself needs to put a mode back — a file left
	// at 0000 must still be chmod-able by its owner, as on Unix — plus the
	// timestamp write POSIX lets an owner make on a file they cannot
	// write, and the delete POSIX governs from the parent directory.
	maskOwnerAlways = fileWriteAttributes | writeDAC | writeOwner | deleteAccess
)

// aclSpec is one mode expressed as the access masks the DACL will carry:
// what each POSIX class is allowed, what the owner and group classes must
// additionally be denied, and the marker ACE's mask.
type aclSpec struct {
	ownerAllow, groupAllow, otherAllow uint32
	ownerDeny, groupDeny               uint32
	marker                             uint32
}

// specOf translates a mode into the masks [Set] writes. It is pure so the
// mapping — which is the whole of the design that can be got wrong — is
// tested on every host and not only where it runs.
func specOf(mode fs.FileMode, dir bool) aclSpec {
	write := uint32(maskWrite)
	if dir {
		write = maskDirWrite
	}
	class := func(bits uint32) uint32 {
		var m uint32
		if bits&4 != 0 {
			m |= maskRead
		}
		if bits&2 != 0 {
			m |= write
		}
		if bits&1 != 0 {
			m |= maskExec
		}
		return m
	}
	perm := uint32(mode.Perm())
	s := aclSpec{
		ownerAllow: class(perm >> 6 & 7),
		groupAllow: class(perm >> 3 & 7),
		otherAllow: class(perm & 7),
		marker:     cygACENewStyle,
	}
	// A class less permissive than a later one needs an explicit deny,
	// because the later ACE would otherwise reach the same account: the
	// owner is usually a member of the file's group and always a member of
	// Everyone.
	s.ownerDeny = (s.groupAllow | s.otherAllow) &^ s.ownerAllow
	s.groupDeny = s.otherAllow &^ s.groupAllow
	if mode&fs.ModeSticky != 0 {
		s.marker |= cygACEISVTX
	}
	if mode&fs.ModeSetgid != 0 {
		s.marker |= cygACEISGID
	}
	if mode&fs.ModeSetuid != 0 {
		s.marker |= cygACEISUID
	}
	return s
}

// permOf maps one class's access mask back to its rwx bits. It reads the
// three data rights only: the rights in maskAlways and maskOwnerAlways are
// there whatever the mode says, so counting them would report r for every
// class of every file.
func permOf(mask uint32) uint32 {
	var bits uint32
	if mask&fileReadData != 0 {
		bits |= 4
	}
	if mask&fileWriteData != 0 {
		bits |= 2
	}
	if mask&fileExecute != 0 {
		bits |= 1
	}
	return bits
}

// markerMode maps a marker ACE's mask back to the three mode bits it
// carries, and reports whether the mask is a marker at all.
func markerMode(mask uint32) (fs.FileMode, bool) {
	if mask&cygACENewStyle == 0 {
		return 0, false
	}
	var mode fs.FileMode
	if mask&cygACEISVTX != 0 {
		mode |= fs.ModeSticky
	}
	if mask&cygACEISGID != 0 {
		mode |= fs.ModeSetgid
	}
	if mask&cygACEISUID != 0 {
		mode |= fs.ModeSetuid
	}
	return mode, true
}

// Overlay returns m with the mode recorded for path substituted in, or m
// unchanged when no mode is recorded there.
func Overlay(path string, m fs.FileMode) fs.FileMode {
	recorded, ok := Get(path)
	if !ok {
		return m
	}
	return m&^modeBits | recorded&modeBits
}

// Apply returns info with the mode recorded for path substituted into its
// Mode, or info itself when no mode is recorded. It is the one reader a
// stat layer needs: everything a FileInfo reports other than the mode is
// forwarded to the original.
//
// Irregular files are returned untouched. A device, a pipe or a socket has
// no ACL worth consulting, and on Windows a stat of one is frequently a
// synthesized FileInfo with no path behind it at all.
func Apply(path string, info fs.FileInfo) fs.FileInfo {
	if info == nil || !Supported {
		return info
	}
	if m := info.Mode(); !m.IsRegular() && !m.IsDir() {
		return info
	}
	recorded, ok := Get(path)
	if !ok {
		return info
	}
	return modeInfo{FileInfo: info, mode: info.Mode()&^modeBits | recorded&modeBits}
}

// modeInfo is a FileInfo whose Mode is the recorded one.
type modeInfo struct {
	fs.FileInfo
	mode fs.FileMode
}

func (i modeInfo) Mode() fs.FileMode { return i.mode }

// SameFile is [os.SameFile] seen through the wrapper [Apply] may have put
// around either FileInfo. os.SameFile recognizes only the concrete type
// os.Stat returns, so a FileInfo carrying a recorded mode would otherwise
// compare unequal to everything — including to itself, which is what
// `test f -ef f` asks.
func SameFile(a, b fs.FileInfo) bool {
	return os.SameFile(unwrap(a), unwrap(b))
}

// unwrap returns the FileInfo [Apply] wrapped, for the handful of callers
// that need the platform's own type back rather than the mode.
func unwrap(info fs.FileInfo) fs.FileInfo {
	if mi, ok := info.(modeInfo); ok {
		return mi.FileInfo
	}
	return info
}

// Recorded reports whether info came out of [Apply] carrying a mode read
// back from an ACL. It is how a caller tells "this file's mode is 0644
// because someone chmod'ed it" from "this file's mode is 0666 because that
// is what Windows reports for every writable file" — two answers io/fs
// spells identically, and only the first of which is worth believing.
func Recorded(info fs.FileInfo) bool {
	_, ok := info.(modeInfo)
	return ok
}
