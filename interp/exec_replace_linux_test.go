// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build linux && !cgo

package interp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestExecPrivateDescriptors(t *testing.T) {
	if mode := os.Getenv("SH_EXEC_FD_HELPER"); mode != "" {
		execPrivateDescriptorHelper(t, mode)
		return
	}
	for _, mode := range []string{"success", "failure", "limit"} {
		t.Run(mode, func(t *testing.T) {
			for range 10 {
				outputPath := filepath.Join(t.TempDir(), "redirect")
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecPrivateDescriptors$")
				cmd.Env = append(os.Environ(), "GOSH_PROG=", "SH_EXEC_FD_HELPER="+mode, "SH_EXEC_FD_OUTPUT="+outputPath, "GOGC=1", "GOMAXPROCS=2")
				out, err := cmd.CombinedOutput()
				cancel()
				if err != nil || !strings.Contains(string(out), "exec-fds-ok") {
					t.Fatalf("helper %s: %v\n%s", mode, err, out)
				}
				if mode != "failure" {
					data, err := os.ReadFile(outputPath)
					if err != nil || string(data) != "redirect-ok" {
						t.Fatalf("redirect: %q, %v", data, err)
					}
				}
			}
		})
	}
}

func execPrivateDescriptorHelper(t *testing.T, mode string) {
	null, err := os.OpenFile("/dev/null", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	// Initialize the poller and retain a live pipe across the failure path.
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	r := &Runner{fdTable: make(map[int]*os.File)}
	for fd := 3; fd < 32; fd++ {
		r.fdTable[fd] = null
	}
	output, err := os.OpenFile(os.Getenv("SH_EXEC_FD_OUTPUT"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	r.fdTable[9] = output
	closed, err := closedExecFile()
	if err != nil {
		t.Fatal(err)
	}
	r.fdTable[17] = closed
	framePath := os.Getenv("SH_EXEC_FD_OUTPUT") + ".frame"
	if err := os.WriteFile(framePath, []byte("handoff-ok"), 0600); err != nil {
		t.Fatal(err)
	}
	frame, err := os.Open(framePath)
	if err != nil {
		t.Fatal(err)
	}
	defer frame.Close()
	owned := &ownedExecHandoff{file: frame}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), handlerCtxKey{}, HandlerContext{Dir: cwd, runner: r})
	env := append(os.Environ(), "EXPECTED_PID="+strconv.Itoa(os.Getpid()))
	var visibleLimit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &visibleLimit); err != nil {
		t.Fatal(err)
	}
	env = append(env, "EXPECTED_LIMIT="+strconv.FormatUint(visibleLimit.Cur, 10))
	args := []string{"sh", "-c", `test "$$" = "$EXPECTED_PID" || exit 41; test "$(ulimit -n)" = "$EXPECTED_LIMIT" || exit 42; test ! -e /proc/self/fd/17 || exit 43; test "$(/bin/cat /proc/self/fd/$BASHY_OWNED_EXEC_FRAME)" = handoff-ok || exit 44; printf redirect-ok >&9; printf exec-fds-ok`, "sh", strings.Repeat("x", 64*1024)}
	if mode == "limit" {
		var limit syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
			t.Fatal(err)
		}
		limit.Cur = min(limit.Max, 1024)
		if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
			t.Fatal(err)
		}
		env = append(env, "EXPECTED_LIMIT="+strconv.FormatUint(limit.Cur, 10))
	}
	if mode != "failure" {
		_, err := execReplace(ctx, "/bin/sh", args, env, os.Stdin, os.Stdout, os.Stderr, owned)
		t.Fatalf("replacement returned: %v", err)
	}
	snapshot := func() map[int]string {
		m := make(map[int]string)
		for fd := 0; fd < 32; fd++ {
			target, err := os.Readlink(fmt.Sprintf("/proc/thread-self/fd/%d", fd))
			flags, ferr := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
			m[fd] = fmt.Sprintf("%s/%v/%d/%v", target, err, flags, ferr)
		}
		return m
	}
	root := t.TempDir()
	denied, invalid := filepath.Join(root, "denied"), filepath.Join(root, "invalid")
	if err := os.WriteFile(denied, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalid, []byte("invalid executable\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path  string
		errno syscall.Errno
	}{{filepath.Join(root, "missing"), syscall.ENOENT}, {denied, syscall.EACCES}, {invalid, syscall.ENOEXEC}} {
		before := snapshot()
		attempted, err := execReplace(ctx, tc.path, args, env, os.Stdin, os.Stdout, os.Stderr, nil)
		if !attempted || !errors.Is(err, tc.errno) {
			t.Fatalf("%s: attempted=%t error=%v", tc.path, attempted, err)
		}
		runtime.GC()
		for fd, got := range snapshot() {
			if got != before[fd] {
				t.Fatalf("fd%d changed: before %q after %q", fd, before[fd], got)
			}
		}
		if _, err := writer.Write([]byte{'x'}); err != nil {
			t.Fatal(err)
		}
		var b [1]byte
		if _, err := reader.Read(b[:]); err != nil || b[0] != 'x' {
			t.Fatalf("poller pipe: %q, %v", b, err)
		}
	}
	// Inspect restoration in the isolated thread itself. Looking only at the
	// unaffected caller's shared table would miss a broken private rollback.
	pathp, _ := syscall.BytePtrFromString(filepath.Join(root, "missing"))
	argv, _ := syscall.SlicePtrFromStrings(args)
	envp, _ := syscall.SlicePtrFromStrings(env)
	placements := make([]execFDPlacement, 32)
	for i := range placements {
		placements[i] = execFDPlacement{source: int(null.Fd()), target: i, temporary: -1, backup: -1}
	}
	// Exercise initially closed targets and closing a live standard target.
	placements = append(placements, execFDPlacement{source: int(null.Fd()), target: 100, temporary: -1, backup: -1})
	placements[1].source = -1
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		before := snapshot()
		execRuntimeBefore()
		errno := execWithPrivateFDs(pathp, &argv[0], &envp[0], placements, 101)
		execRuntimeAfter()
		if errno != syscall.ENOENT {
			done <- fmt.Errorf("raw failure = %v", errno)
			return
		}
		for fd, got := range snapshot() {
			if got != before[fd] {
				done <- fmt.Errorf("private fd%d: before %q after %q", fd, before[fd], got)
				return
			}
		}
		if _, e := unix.FcntlInt(100, unix.F_GETFD, 0); e != syscall.EBADF {
			done <- fmt.Errorf("closed target100: %v", e)
			return
		}
		for _, p := range placements {
			for _, fd := range []int{p.temporary, p.backup} {
				if fd >= 0 {
					if _, e := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); e != syscall.EBADF {
						done <- fmt.Errorf("private temporary%d leaked: %v", fd, e)
						return
					}
				}
			}
		}
		runtime.GC()
		done <- nil
	}()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(pathp)
	fmt.Println("exec-fds-ok")
}
