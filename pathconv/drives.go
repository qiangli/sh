// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package pathconv

// DriveSet is a set of logical drive letters, one bit per letter with bit
// 0 standing for A:, the layout GetLogicalDrives reports.
type DriveSet uint32

// AllDrives is the set holding every letter A: through Z:.
const AllDrives DriveSet = 1<<26 - 1

// DrivesOf returns the set of the letters in s ("CD"), either case; other
// characters are ignored.
func DrivesOf(s string) DriveSet {
	var set DriveSet
	for i := 0; i < len(s); i++ {
		if isDriveLetter(s[i]) {
			set |= 1 << (s[i]&^0x20 - 'A')
		}
	}
	return set
}

// Has reports whether the drive letter, in either case, is in the set.
func (s DriveSet) Has(drive byte) bool {
	if !isDriveLetter(drive) {
		return false
	}
	return s&(1<<(drive&^0x20-'A')) != 0
}

// LogicalDrives is the seam through which the drive-form conversions learn
// which drives are present. MSYS converts /x/… to X:\… only when X: is a
// logical drive; otherwise /x is a directory under the POSIX root, so
// `HOME=/a/b/c /bin/echo $HOME` hands the child /a/b/c rather than A:\b\c
// (varenv.tests). On Windows it is the GetLogicalDrives mask, queried once
// and cached; on every other host it is [AllDrives], so windows-mode
// conversions there apply the plain drive rule. Tests pin it.
var LogicalDrives = hostLogicalDrives
