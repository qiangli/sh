// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build linux && !cgo

package interp

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
	"mvdan.cc/sh/v3/interp/ownedexec"
)

// These are the same runtime hooks used by syscall.Exec. In particular the
// before hook serializes exec with creation of runtime threads (Go issue 19546).
// The runtime publishes both linknames; no runtime state is inspected or changed.
//
//go:linkname execRuntimeBefore syscall.runtime_BeforeExec
func execRuntimeBefore()

//go:linkname execRuntimeAfter syscall.runtime_AfterExec
func execRuntimeAfter()

type execFDPlacement struct {
	source, target    int
	temporary, backup int
	flags             int
}

func execReplace(ctx context.Context, path string, args, env []string, stdin, stdout, stderr any, owned *ownedExecHandoff) (bool, error) {
	hc := HandlerCtx(ctx)
	if !execReplaceCwdMatches(hc.Dir) {
		return false, nil
	}
	files := []any{stdin, stdout, stderr}
	for _, v := range files {
		if _, ok := v.(*os.File); !ok {
			return false, nil
		}
	}
	if len(args) == 0 {
		return true, fmt.Errorf("exec: empty argument list")
	}
	placements := make([]execFDPlacement, 0, 3)
	high := 3
	add := func(source, target int) {
		placements = append(placements, execFDPlacement{source: source, target: target, temporary: -1, backup: -1})
		if target >= high {
			high = target + 1
		}
	}
	for fd, v := range files {
		add(int(v.(*os.File).Fd()), fd)
	}
	if hc.runner != nil {
		for fd, f := range hc.runner.fdTable {
			if fd >= 3 && f != nil {
				add(int(f.Fd()), fd)
			}
		}
	}
	if high <= 0 {
		return true, syscall.EINVAL
	}
	if owned != nil {
		// Reserve a logical handoff target, not a live runtime descriptor.
		// The isolated table below installs it together with the redirects.
		env = setExecEnvValue(env, ownedexec.Marker, strconv.Itoa(high))
		add(int(owned.file.Fd()), high)
	}
	if high <= 0 {
		return true, syscall.EINVAL
	}
	pathp, err := syscall.BytePtrFromString(path)
	if err != nil {
		return true, err
	}
	argv, err := syscall.SlicePtrFromStrings(args)
	if err != nil {
		return true, err
	}
	envp, err := syscall.SlicePtrFromStrings(env)
	if err != nil {
		return true, err
	}
	// Initialize the process-wide poller before the descriptor-table snapshot.
	// Later runtime threads must not publish a new poller descriptor which is
	// absent from the isolated table while it is being retired after failure.
	pr, pw, err := os.Pipe()
	if err != nil {
		return true, err
	}
	pr.Close()
	pw.Close()
	result := make(chan syscall.Errno, 1)
	go func() {
		runtime.LockOSThread()
		// Never unlock: on failure the goroutine's exit retires this thread
		// and its private descriptor table. The caller retains its original
		// shared table. On success exec replaces the entire process, same PID.
		execRuntimeBefore()
		errno := execWithPrivateFDs(pathp, &argv[0], &envp[0], placements, high)
		execRuntimeAfter()
		result <- errno
	}()
	errno := <-result
	runtime.KeepAlive(files)
	runtime.KeepAlive(hc)
	runtime.KeepAlive(owned)
	runtime.KeepAlive(argv)
	runtime.KeepAlive(envp)
	return true, errno
}

// execWithPrivateFDs never enters the scheduler while redirects occupy numbers
// used by the runtime. Other threads keep their original CLONE_FILES table;
// this thread cannot be asynchronously preempted inside the nosplit tail.
// All allocations, validation and exec/thread serialization happen beforehand.
// The backup describes the exact post-unshare snapshot, not a racy earlier view.
// Current resource limits are preserved, as required for shell exec semantics.
//
//go:nosplit
//go:norace
func execWithPrivateFDs(path *byte, argv, envp **byte, fds []execFDPlacement, high int) syscall.Errno {
	// No valid Linux descriptor can equal UINT_MAX. This file-table-only API
	// therefore isolates without closing a descriptor, and works with normal
	// container policies which deny the broader namespace unshare syscall.
	_, _, errno := syscall.RawSyscall(unix.SYS_CLOSE_RANGE, 0xffffffff, 0xffffffff, unix.CLOSE_RANGE_UNSHARE)
	if errno == syscall.ENOSYS || errno == syscall.EINVAL {
		_, _, errno = syscall.RawSyscall(syscall.SYS_UNSHARE, syscall.CLONE_FILES, 0, 0)
	}
	if errno != 0 {
		return errno
	}
	for i := range fds {
		p := &fds[i]
		if p.source >= 0 {
			fd, _, e := syscall.RawSyscall(syscall.SYS_FCNTL, uintptr(p.source), unix.F_DUPFD_CLOEXEC, uintptr(high))
			if e != 0 {
				errno = e
				goto cleanup
			}
			p.temporary = int(fd)
		}
		flags, _, e := syscall.RawSyscall(syscall.SYS_FCNTL, uintptr(p.target), unix.F_GETFD, 0)
		if e == syscall.EBADF {
			continue
		}
		if e != 0 {
			errno = e
			goto cleanup
		}
		p.flags = int(flags)
		fd, _, e := syscall.RawSyscall(syscall.SYS_FCNTL, uintptr(p.target), unix.F_DUPFD_CLOEXEC, uintptr(high))
		if e != 0 {
			errno = e
			goto cleanup
		}
		p.backup = int(fd)
	}
	for i := range fds {
		p := &fds[i]
		if p.temporary < 0 {
			_, _, errno = syscall.RawSyscall(syscall.SYS_CLOSE, uintptr(p.target), 0, 0)
			if errno == syscall.EBADF {
				errno = 0
			}
		} else {
			_, _, errno = syscall.RawSyscall(syscall.SYS_DUP3, uintptr(p.temporary), uintptr(p.target), 0)
		}
		if errno != 0 {
			goto restore
		}
	}
	_, _, errno = syscall.RawSyscall(syscall.SYS_EXECVE, uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(argv)), uintptr(unsafe.Pointer(envp)))
restore:
	for i := range fds {
		p := &fds[i]
		var e syscall.Errno
		if p.backup < 0 {
			_, _, e = syscall.RawSyscall(syscall.SYS_CLOSE, uintptr(p.target), 0, 0)
			if e == syscall.EBADF {
				e = 0
			}
		} else {
			flags := uintptr(0)
			if p.flags&unix.FD_CLOEXEC != 0 {
				flags = unix.O_CLOEXEC
			}
			_, _, e = syscall.RawSyscall(syscall.SYS_DUP3, uintptr(p.backup), uintptr(p.target), flags)
		}
		if e != 0 {
			// A corrupt table must never re-enter the Go runtime. This path
			// is unrecoverable, like a failed runtime descriptor restoration.
			for {
				_, _, _ = syscall.RawSyscall(syscall.SYS_EXIT_GROUP, 126, 0, 0)
			}
		}
	}
cleanup:
	for i := range fds {
		if fds[i].temporary >= 0 {
			_, _, _ = syscall.RawSyscall(syscall.SYS_CLOSE, uintptr(fds[i].temporary), 0, 0)
		}
		if fds[i].backup >= 0 {
			_, _, _ = syscall.RawSyscall(syscall.SYS_CLOSE, uintptr(fds[i].backup), 0, 0)
		}
	}
	return errno
}
