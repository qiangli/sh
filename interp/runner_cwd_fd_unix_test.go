// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build linux || darwin

package interp_test

import (
	"context"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestRunnerSubshellsDoNotLeakCwdHandles(t *testing.T) {
	fdDir := "/proc/self/fd"
	if runtime.GOOS == "darwin" {
		fdDir = "/dev/fd"
	}
	countFDs := func() int {
		if runtime.GOOS == "darwin" {
			var limit unix.Rlimit
			if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
				t.Fatal(err)
			}
			maxFD := min(int(limit.Cur), 4096)
			count := 0
			for fd := range maxFD {
				if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil {
					count++
				}
			}
			return count
		}
		entries, err := os.ReadDir(fdDir)
		if err != nil {
			t.Fatal(err)
		}
		return len(entries)
	}

	file, err := syntax.NewParser().Parse(strings.NewReader(`
i=0
while test "$i" -lt 256; do
	(:)
	value=$(:)
	i=$((i + 1))
done
`), "cwd-fd-leak")
	if err != nil {
		t.Fatal(err)
	}
	runner, err := interp.New(interp.Dir(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}

	oldGCPercent := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(oldGCPercent)
	before := countFDs()
	if err := runner.Run(context.Background(), file); err != nil {
		t.Fatal(err)
	}
	for range 256 {
		runner.Reset()
	}
	after := countFDs()
	if delta := after - before; delta > 4 {
		t.Fatalf("subshells leaked %d cwd descriptors", delta)
	}
}
