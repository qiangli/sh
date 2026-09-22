// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build !windows

package winmode

import (
	"errors"
	"io/fs"
)

// Supported reports whether a mode can be recorded in a file's ACL here.
// Everywhere but Windows the filesystem holds POSIX modes itself, so there
// is nothing to record and nothing to read back: chmod(2) and stat(2) are
// already the writer and the reader this package stands in for.
const Supported = false

// Get reports that no mode is recorded, always: see [Supported].
func Get(string) (fs.FileMode, bool) { return 0, false }

// Set always fails: see [Supported]. A caller on this platform wants
// os.Chmod, and reaching here at all is a bug rather than a fallback.
func Set(string, fs.FileMode) error {
	return errors.New("winmode: modes are recorded in an ACL only on windows")
}
