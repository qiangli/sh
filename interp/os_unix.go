// Copyright (c) 2017, Andrey Nering <andrey.nering@gmail.com>
// See LICENSE for licensing information

//go:build unix

package interp

import (
	"context"
	"io"
	"os"
	"os/exec"
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

// setProcessUmask sets the process-wide umask. Used only when MirrorUmask is
// enabled (a standalone shell binary, one Runner per process) so external
// commands honour the shell's umask.
func setProcessUmask(mask int) {
	umaskMu.Lock()
	defer umaskMu.Unlock()
	syscall.Umask(mask)
}

func mkfifo(path string, mode uint32) error {
	return unix.Mkfifo(path, mode)
}

// canExec reports whether the current effective user may execute path.
// Bash's PATH search uses sh_eaccess(file, X_OK) (findcmd.c), so a file
// with the execute bit set only for other users is correctly skipped.
func canExec(path string) bool {
	return unix.Faccessat(unix.AT_FDCWD, path, unix.X_OK, unix.AT_EACCESS) == nil
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
	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFL, 0)
	if err != nil {
		delete(r.fdTable, fd)
		f.Close()
		return nil, false
	}
	switch flags & unix.O_ACCMODE {
	case unix.O_RDONLY:
		if r.fdReadTable == nil {
			r.fdReadTable = make(map[int]bool)
		}
		r.fdReadTable[fd] = true
	case unix.O_WRONLY:
		if r.fdWriteTable == nil {
			r.fdWriteTable = make(map[int]io.Writer)
		}
		r.fdWriteTable[fd] = f
	case unix.O_RDWR:
		if r.fdReadTable == nil {
			r.fdReadTable = make(map[int]bool)
		}
		if r.fdWriteTable == nil {
			r.fdWriteTable = make(map[int]io.Writer)
		}
		r.fdReadTable[fd] = true
		r.fdWriteTable[fd] = f
	}
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

func prepareBackgroundJobCmd(ctx context.Context, cmd *exec.Cmd) {
	bg, _ := ctx.Value(bgProcCtxKey{}).(*bgProc)
	if bg == nil || !bg.jobControl {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func waitExecCmd(ctx context.Context, cmd *exec.Cmd) error {
	bg, _ := ctx.Value(bgProcCtxKey{}).(*bgProc)
	if bg == nil || !bg.jobControl {
		return cmd.Wait()
	}
	var status syscall.WaitStatus
	for {
		_, err := syscall.Wait4(cmd.Process.Pid, &status, syscall.WUNTRACED|syscall.WCONTINUED, nil)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		switch {
		case status.Stopped():
			if bg.ignoreNextStop.Swap(0) != 0 {
				continue
			}
			if name, ok := signalName(status.Signal()); ok {
				bg.setStopSignal("SIG" + name)
			}
			bg.setState(jobStopped)
			continue
		case status.Continued():
			if bg.ignoreNextContinue.Swap(0) != 0 {
				continue
			}
			bg.setState(jobRunning)
			continue
		default:
			bg.setState(jobDead)
			if status.Exited() && status.ExitStatus() == 0 {
				return nil
			}
			if status.Signaled() {
				return ExitStatus(128 + status.Signal())
			}
			if status.Exited() {
				return ExitStatus(status.ExitStatus())
			}
			return ExitStatus(1)
		}
	}
}

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

// closeOnExecFd marks fd close-on-exec so it does not leak into spawned
// children. Unix passes the int fd straight to syscall.CloseOnExec; the
// !unix build is a no-op (Windows wants a syscall.Handle) — see os_notunix.go.
func closeOnExecFd(fd int) { syscall.CloseOnExec(fd) }
