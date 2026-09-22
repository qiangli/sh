// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build windows

package pathconv

import (
	"sync"

	"golang.org/x/sys/windows"
)

var logicalDrives struct {
	once sync.Once
	set  DriveSet
}

// hostLogicalDrives is GetLogicalDrives, cached on first use. Should the
// query fail, every letter counts, which is the pre-existing rule.
func hostLogicalDrives() DriveSet {
	logicalDrives.once.Do(func() {
		mask, err := windows.GetLogicalDrives()
		if err != nil || mask == 0 {
			logicalDrives.set = AllDrives
			return
		}
		logicalDrives.set = DriveSet(mask) & AllDrives
	})
	return logicalDrives.set
}
