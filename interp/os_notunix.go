// Copyright (c) 2017, Andrey Nering <andrey.nering@gmail.com>
// See LICENSE for licensing information

//go:build !unix

package interp

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func mkfifo(path string, mode uint32) error {
	return fmt.Errorf("unsupported")
}

// canExec reports whether path is executable. On non-Unix platforms there
// is no per-user X_OK check, so fall back to the file mode bits already
// vetted by the caller.
func canExec(path string) bool {
	return true
}

// canRead reports whether path is readable. On non-Unix platforms there
// is no portable effective-user R_OK check here, so fall back to the file
// mode bits already vetted by the caller.
func canRead(path string) bool {
	return true
}

// processUmask returns a Unix-style default on non-Unix platforms,
// since Windows has no umask equivalent.
func processUmask() int {
	return 0o022
}

// setProcessUmask is a no-op on non-unix platforms (no umask equivalent).
func setProcessUmask(mask int) {}

// syncUmaskForChild is a no-op on non-unix platforms.
func syncUmaskForChild(mask int) (restore func()) {
	return func() {}
}

func (r *Runner) startExecCmdWithUmask(ctx context.Context, cmd *exec.Cmd, mask int) error {
	return r.startExecCmd(ctx, cmd)
}

func refreshFileTimesNow(file *os.File, path string) error {
	now := time.Now()
	return os.Chtimes(path, now, now)
}

// dupPipeFd is a no-op on non-unix platforms; the original pipe fd is
// returned. Pipelines will still run, but EOF/SIGPIPE propagation is
// best-effort because the parent retains the original fd reference.
func dupPipeFd(f *os.File) (*os.File, bool, error) {
	return f, false, nil
}

// access attempts to emulate [unix.Access] on Windows.
// Windows seems to have a different system of permissions than Unix,
// so for now just rely on what [io/fs.FileInfo] gives us.
func (r *Runner) access(ctx context.Context, path string, mode uint32) error {
	info, err := r.lstat(ctx, path)
	if err != nil {
		return err
	}
	m := info.Mode()
	switch mode {
	case access_R_OK:
		if m&0o400 == 0 {
			return fmt.Errorf("file is not readable")
		}
	case access_W_OK:
		if m&0o200 == 0 {
			return fmt.Errorf("file is not writable")
		}
	case access_X_OK:
		if m&0o100 == 0 {
			return fmt.Errorf("file is not executable")
		}
	}
	return nil
}

// unTestOwnOrGrp panics. Under Unix, it implements the -O and -G unary tests,
// but under Windows, it's unclear how to implement those tests, since Windows
// doesn't have the concept of a file owner, just ACLs, and it's unclear how
// to map the one to the other.
func (r *Runner) unTestOwnOrGrp(ctx context.Context, op syntax.UnTestOperator, x string) bool {
	r.errf("unsupported unary test op: %v\n", op)
	return false
}

func userGroups() []string {
	return []string{strconv.Itoa(os.Getgid())}
}

func openPath(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	return os.OpenFile(path, flag, perm)
}

func openPathAt(ctx context.Context, dir, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	return openPath(ctx, shellPathJoinAbs(dir, path), flag, perm)
}

// modifiedSinceAccessed reports whether the file's mtime is strictly
// greater than its atime — bash's `-N FILE` test operator. atime is not
// portably available off unix; report false.
func modifiedSinceAccessed(info os.FileInfo) bool {
	return false
}

// waitStatus is a no-op on plan9 and windows.
type waitStatus struct{}

func (waitStatus) Signaled() bool { return false }
func (waitStatus) Signal() int    { return 0 }

func prepareBackgroundJobCmd(ctx context.Context, cmd *exec.Cmd) {}

func waitExecCmd(ctx context.Context, cmd *exec.Cmd) error {
	return cmd.Wait()
}

func execReplace(ctx context.Context, path string, args, env []string, stdin any, stdout any, stderr any) (bool, error) {
	return false, nil
}

func (r *Runner) inheritedFd(fd int) (*os.File, bool) {
	return nil, false
}

// closeOnExecFd is a no-op off unix: Windows/plan9 don't expose the int-fd
// close-on-exec primitive (syscall.CloseOnExec there takes a Handle).
func closeOnExecFd(int) {}

// hdocServe delivers a here-document body over a pipe. Non-unix platforms
// cannot unlink an open file, so the temp-file approach used on unix isn't
// available; the pipe+goroutine is retained here.
func hdocServe(body []byte) (*os.File, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	go func() {
		pw.Write(body)
		pw.Close()
	}()
	return pr, nil
}
