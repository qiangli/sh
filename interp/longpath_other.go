// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build !linux

package interp

import "io/fs"

func statLongPath(path string, original error) (fs.FileInfo, error) {
	return nil, original
}

func physicalLongPath(path string, original error) (string, error) {
	return "", original
}
