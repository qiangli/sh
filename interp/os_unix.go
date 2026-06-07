// Copyright (c) 2017, Andrey Nering <andrey.nering@gmail.com>
// See LICENSE for licensing information

//go:build unix

package interp

import (
	"context"
	"os"
	"os/user"
	"strconv"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
	"mvdan.cc/sh/v3/syntax"
)

// umaskMu serializes the read-then-restore dance for [syscall.Umask],
// which is process-wide and has no native read-only access.
var umaskMu sync.Mutex

// processUmask returns the current process umask without permanently
// changing it. It briefly sets the umask to 0 to read the prior value,
// then restores it, all under a package-level mutex.
func processUmask() int {
	umaskMu.Lock()
	defer umaskMu.Unlock()
	m := syscall.Umask(0)
	syscall.Umask(m)
	return m
}

func mkfifo(path string, mode uint32) error {
	return unix.Mkfifo(path, mode)
}

// dupPipeFd duplicates a pipe file descriptor, returning a new *os.File
// that refers to the same underlying pipe endpoint. The caller can close
// the original fd while the duplicate remains valid. This is used to
// ensure the parent process does not hold extra pipe fd references during
// pipeline execution, which would prevent EOF/SIGPIPE propagation.
func dupPipeFd(f *os.File) (*os.File, error) {
	newFd, err := syscall.Dup(int(f.Fd()))
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(newFd)
	return os.NewFile(uintptr(newFd), f.Name()+"-dup"), nil
}

func (r *Runner) inheritedFd(fd int) (*os.File, bool) {
	if fd < 3 {
		return nil, false
	}
	if !r.inheritedFds[fd] {
		return nil, false
	}
	f := os.NewFile(uintptr(fd), "/dev/fd/"+strconv.Itoa(fd))
	if f == nil {
		return nil, false
	}
	if r.fdTable == nil {
		r.fdTable = make(map[int]*os.File)
	}
	r.fdTable[fd] = f
	return f, true
}

// access is similar to checking the permission bits from [io/fs.FileInfo],
// but it also takes into account the current user's role.
func (r *Runner) access(ctx context.Context, path string, mode uint32) error {
	// TODO(v4): "access" may need to become part of a handler, like "open" or "stat".
	return unix.Access(path, mode)
}

// unTestOwnOrGrp implements the -O and -G unary tests. If the file does not
// exist, or the current user cannot be retrieved, returns false.
func (r *Runner) unTestOwnOrGrp(ctx context.Context, op syntax.UnTestOperator, x string) bool {
	info, err := r.stat(ctx, x)
	if err != nil {
		return false
	}
	u, err := user.Current()
	if err != nil {
		return false
	}
	if op == syntax.TsUsrOwn {
		uid, _ := strconv.Atoi(u.Uid)
		return uint32(uid) == info.Sys().(*syscall.Stat_t).Uid
	}
	gid, _ := strconv.Atoi(u.Gid)
	return uint32(gid) == info.Sys().(*syscall.Stat_t).Gid
}

type waitStatus = syscall.WaitStatus

// modifiedSinceAccessed reports whether the file's mtime is strictly
// greater than its atime — bash's `-N FILE` test operator. On
// platforms where atime is unavailable, returns false.
func modifiedSinceAccessed(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	mtim := unixStatMtime(stat)
	atim := unixStatAtime(stat)
	if mtim.Equal(atim) {
		return false
	}
	return mtim.After(atim)
}
