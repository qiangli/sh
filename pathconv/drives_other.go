// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build !windows

package pathconv

// hostLogicalDrives reports every letter off Windows: there is no drive
// table to consult, and windows-mode conversions there keep the plain
// drive rule unless a test pins [LogicalDrives].
func hostLogicalDrives() DriveSet { return AllDrives }
