// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build windows

package winmode

import (
	"encoding/binary"
	"fmt"
	"io/fs"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Supported reports whether a mode can be recorded in a file's ACL here.
const Supported = true

// aclRevision is ACL_REVISION: the revision of a DACL holding only the
// non-object ACE types this package writes.
const aclRevision = 2

// Set records mode in path's discretionary ACL. The DACL it writes is
// protected, so the recorded mode is the whole answer for that file and an
// inherited "Users: full control" from the parent cannot quietly grant back
// what the mode took away — which is the difference between a mode that is
// merely remembered and one the operating system enforces.
func Set(path string, mode fs.FileMode) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if owner == nil {
		return fmt.Errorf("winmode: %s has no owner SID to hold a mode", path)
	}
	// A missing group is not an error: a file on a filesystem that reports
	// no primary group simply has no group class, and the group ACEs are
	// left out rather than aimed at some substitute account.
	group, _, gerr := sd.Group()
	if gerr != nil {
		group = nil
	}
	nullSID, err := windows.CreateWellKnownSid(windows.WinNullSid)
	if err != nil {
		return err
	}
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		return err
	}

	spec := specOf(mode, fi.IsDir())

	var b aclBuilder
	b.add(windows.ACCESS_DENIED_ACE_TYPE, spec.marker, nullSID)
	if spec.ownerDeny != 0 {
		b.add(windows.ACCESS_DENIED_ACE_TYPE, spec.ownerDeny, owner)
	}
	b.add(windows.ACCESS_ALLOWED_ACE_TYPE, spec.ownerAllow|maskAlways|maskOwnerAlways, owner)
	// The group ACEs go in even when the group SID is the owner SID, which
	// is the common case for a file created on a workstation. A second ACE
	// for the same account after the owner's allow cannot take away what
	// that allow already granted, so enforcement is unchanged, and the
	// group class survives the round trip instead of being guessed at on
	// the way back in.
	if group != nil {
		if spec.groupDeny != 0 {
			b.add(windows.ACCESS_DENIED_ACE_TYPE, spec.groupDeny, group)
		}
		b.add(windows.ACCESS_ALLOWED_ACE_TYPE, spec.groupAllow|maskAlways, group)
	}
	b.add(windows.ACCESS_ALLOWED_ACE_TYPE, spec.otherAllow|maskAlways, everyone)

	dacl, err := b.acl()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, dacl, nil)
}

// Get returns the mode recorded in path's discretionary ACL. The second
// result is false when no mode is recorded — an ordinary Windows file, a
// path whose security descriptor cannot be read, or a filesystem with no
// ACLs at all — and the caller should keep whatever mode the platform
// itself reports rather than treat the absence as 0000.
func Get(path string) (fs.FileMode, bool) {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.GROUP_SECURITY_INFORMATION|
			windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return 0, false
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return 0, false
	}
	nullSID, err := windows.CreateWellKnownSid(windows.WinNullSid)
	if err != nil {
		return 0, false
	}
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		return 0, false
	}
	owner, _, _ := sd.Owner()
	group, _, _ := sd.Group()

	var mode fs.FileMode
	var marked, haveOwner, haveGroup, haveOther bool
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return 0, false
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		mask := uint32(ace.Mask)
		if sid.Equals(nullSID) {
			bits, ok := markerMode(mask)
			if !ok {
				continue
			}
			mode, marked = mode|bits, true
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			continue
		}
		// Only the first allow ACE per class counts, so that an owner
		// who is also the group reads back as the owner class and not
		// as its own group, matching how the access check resolves it.
		switch {
		case !haveOwner && owner != nil && sid.Equals(owner):
			mode |= fs.FileMode(permOf(mask) << 6)
			haveOwner = true
		case !haveGroup && group != nil && sid.Equals(group):
			mode |= fs.FileMode(permOf(mask) << 3)
			haveGroup = true
		case !haveOther && sid.Equals(everyone):
			mode |= fs.FileMode(permOf(mask))
			haveOther = true
		}
	}
	if !marked {
		return 0, false
	}
	// A file whose owner or group ACE is gone (an ACL edited by another
	// tool after the mode was written) would read back as a mode nobody
	// asked for. The marker alone is not enough; the owner class must be
	// there, and a missing group class falls back to the other class the
	// way a POSIX ACL's mask would.
	if !haveOwner {
		return 0, false
	}
	if !haveGroup {
		mode |= (mode & 7) << 3
	}
	return mode, true
}

// aclBuilder assembles a DACL one ACE at a time, keeping the order it is
// given. windows.ACL and the ACE structures have unexported or
// variable-length fields, so the bytes are laid out here rather than
// through the Go types; see the package doc for why SetEntriesInAcl, which
// would do this safely, cannot be used.
type aclBuilder struct {
	aces  []byte
	count int
}

// add appends one non-object ACE of the given type, access mask and
// trustee. No inheritance flags are set: a recorded mode describes the one
// file it is on, and nothing about what is created inside a directory
// later.
func (b *aclBuilder) add(aceType uint8, mask uint32, sid *windows.SID) {
	// AceType(1) AceFlags(1) AceSize(2) Mask(4) then the SID.
	const fixed = 8
	size := fixed + sid.Len()
	off := len(b.aces)
	b.aces = append(b.aces, make([]byte, size)...)
	ace := b.aces[off : off+size]
	ace[0] = aceType
	ace[1] = 0
	binary.LittleEndian.PutUint16(ace[2:], uint16(size))
	binary.LittleEndian.PutUint32(ace[4:], mask)
	copy(ace[fixed:], unsafe.Slice((*byte)(unsafe.Pointer(sid)), sid.Len()))
	b.count++
}

// acl returns the assembled DACL.
//
// The backing allocation is a []uint32 rather than a []byte because an ACL
// must be aligned to a DWORD and only the former is guaranteed to be. Every
// ACE is a multiple of four bytes long — a SID is 8+4*subauthorities — so
// the ACEs stay aligned once the 8-byte header is in front of them.
func (b *aclBuilder) acl() (*windows.ACL, error) {
	const header = 8
	size := header + len(b.aces)
	if size > 0xffff {
		return nil, fmt.Errorf("winmode: ACL of %d bytes is too large to encode", size)
	}
	words := make([]uint32, (size+3)/4)
	raw := unsafe.Slice((*byte)(unsafe.Pointer(&words[0])), len(words)*4)
	raw[0] = aclRevision
	raw[1] = 0
	binary.LittleEndian.PutUint16(raw[2:], uint16(size))
	binary.LittleEndian.PutUint16(raw[4:], uint16(b.count))
	binary.LittleEndian.PutUint16(raw[6:], 0)
	copy(raw[header:], b.aces)
	return (*windows.ACL)(unsafe.Pointer(&words[0])), nil
}
