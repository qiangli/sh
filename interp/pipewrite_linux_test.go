// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

//go:build linux

package interp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"mvdan.cc/sh/v3/syntax"
)

const pipelineSIGPIPEHelperEnv = "SH_TEST_PIPELINE_SIGPIPE_HELPER"

func TestPipelineBuiltinSIGPIPEIsolation(t *testing.T) {
	if mode := os.Getenv(pipelineSIGPIPEHelperEnv); mode != "" {
		switch mode {
		case "semantics":
			runPipelineSIGPIPESemanticsHelper()
		case "concurrent":
			runPipelineSIGPIPEConcurrentHelper()
		default:
			os.Exit(2)
		}
		os.Exit(0)
	}

	stdout, stderr := runPipelineSIGPIPEHelper(t, "semantics")
	const wantStdout = "DEFAULT:141,0\nIGNORED:0,0\nCAUGHT:141,0\nSTDERR_PIPE:141,0\nNESTED_STDERR_PIPE:0,141,0\nEXTERNAL:141,0\nSURVIVED\nAFTER_SIGNAL\n"
	if got := stdout; got != wantStdout {
		t.Fatalf("stdout mismatch:\n got: %q\nwant: %q", got, wantStdout)
	}
	if got, want := stderr, "EXPLICIT_PIPE\n"; got != want {
		t.Fatalf("stderr mismatch:\n got: %q\nwant: %q", got, want)
	}

	stdout, stderr = runPipelineSIGPIPEHelper(t, "concurrent")
	if got, want := stdout, "WRITE_EPIPE\nEXPLICIT_DELIVERED\nNO_LEAK\nSELF_KILL_DELIVERED\n"; got != want {
		t.Fatalf("concurrent stdout mismatch:\n got: %q\nwant: %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("concurrent stderr: %q", stderr)
	}
}

func TestLinuxWriteGeneratedSIGPIPEClassification(t *testing.T) {
	var info unix.Siginfo
	info.Code = 0
	*(*int32)(unsafe.Add(unsafe.Pointer(&info), linuxSiginfoPIDOffset)) = 0
	if !linuxWriteGeneratedSIGPIPE(&info, 0, 1, syscall.EPIPE) {
		t.Fatal("pid-zero SEND_SIG_NOINFO was not classified as pipe-write generated")
	}
	*(*int32)(unsafe.Add(unsafe.Pointer(&info), linuxSiginfoPIDOffset)) = int32(os.Getpid())
	if linuxWriteGeneratedSIGPIPE(&info, 1, 1, nil) {
		t.Fatal("same-process SI_USER without write failure was classified as pipe-write generated")
	}
	if !linuxWriteGeneratedSIGPIPE(&info, 0, 1, syscall.EPIPE) {
		t.Fatal("same-process SI_USER accompanying EPIPE did not preserve pipeline semantics")
	}
	info.Code = -6 // SI_TKILL
	if linuxWriteGeneratedSIGPIPE(&info, 0, 1, syscall.EPIPE) {
		t.Fatal("thread-directed SI_TKILL was classified as pipe-write generated")
	}
}

func runPipelineSIGPIPEHelper(t *testing.T, mode string) (string, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPipelineBuiltinSIGPIPEIsolation$")
	cmd.Env = []string{pipelineSIGPIPEHelperEnv + "=" + mode}
	for _, item := range os.Environ() {
		if strings.HasPrefix(item, "GOSH_PROG=") ||
			strings.HasPrefix(item, "GOSH_CMD=") ||
			strings.HasPrefix(item, pipelineSIGPIPEHelperEnv+"=") {
			continue
		}
		cmd.Env = append(cmd.Env, item)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s helper failed: %v\nstdout:\n%s\nstderr:\n%s", mode, err, &stdout, &stderr)
	}
	return stdout.String(), stderr.String()
}

func runPipelineSIGPIPESemanticsHelper() {
	script := `
producer() {
	i=0
	while test "$i" -lt 10000; do
		printf '%080d\n' "$i"
		i=$((i + 1))
	done
}
producer 2>/dev/null | /usr/bin/true
printf 'DEFAULT:%s,%s\n' "${PIPESTATUS[0]}" "${PIPESTATUS[1]}"
trap '' PIPE
producer 2>/dev/null | /usr/bin/true
printf 'IGNORED:%s,%s\n' "${PIPESTATUS[0]}" "${PIPESTATUS[1]}"
trap 'printf "EXPLICIT_PIPE\n" >&2' PIPE
producer 2>/dev/null | /usr/bin/true
printf 'CAUGHT:%s,%s\n' "${PIPESTATUS[0]}" "${PIPESTATUS[1]}"
error_producer() {
	i=0
	while test "$i" -lt 10000; do
		cd /definitely/missing/$i
		i=$((i + 1))
	done
}
error_producer |& /usr/bin/true
printf 'STDERR_PIPE:%s,%s\n' "${PIPESTATUS[0]}" "${PIPESTATUS[1]}"
/usr/bin/true | error_producer |& /usr/bin/true
printf 'NESTED_STDERR_PIPE:%s,%s,%s\n' "${PIPESTATUS[0]}" "${PIPESTATUS[1]}" "${PIPESTATUS[2]}"
/usr/bin/yes | /usr/bin/head -c 1 >/dev/null
printf 'EXTERNAL:%s,%s\n' "${PIPESTATUS[0]}" "${PIPESTATUS[1]}"
printf 'SURVIVED\n'
/bin/kill -PIPE $$
/bin/sleep 0.05
printf 'AFTER_SIGNAL\n'
`
	file, err := syntax.NewParser().Parse(strings.NewReader(script), "sigpipe.sh")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	runner, err := New(StdIO(nil, os.Stdout, os.Stderr))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := runner.Run(context.Background(), file); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

func runPipelineSIGPIPEConcurrentHelper() {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGPIPE)
	defer signal.Stop(ch)

	read, write, err := os.Pipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	defer write.Close()
	if err := read.Close(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	block := make([]byte, 64<<10)
	writeDone := make(chan error, 1)
	writerTID := make(chan int, 1)
	afterSnapshot := make(chan struct{})
	releaseWrite := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		writerTID <- unix.Gettid()
		_, err := writePipelineOutputAfterSnapshot(write, block, func() {
			close(afterSnapshot)
			<-releaseWrite
		})
		writeDone <- err
	}()
	tid := <-writerTID
	<-afterSnapshot
	// Target the exact locked writer thread after its pending snapshot. This
	// forces the explicit signal to compete with the write-generated PIPE in
	// the same pending slot, exercising the siginfo requeue path.
	if _, _, errno := syscall.RawSyscall(syscall.SYS_TGKILL, uintptr(os.Getpid()), uintptr(tid), uintptr(syscall.SIGPIPE)); errno != 0 {
		fmt.Fprintln(os.Stderr, "tgkill:", errno)
		os.Exit(2)
	}
	close(releaseWrite)
	select {
	case err := <-writeDone:
		if !errors.Is(err, syscall.EPIPE) {
			fmt.Fprintln(os.Stderr, "write:", err)
			os.Exit(2)
		}
		fmt.Println("WRITE_EPIPE")
	case <-time.After(2 * time.Second):
		fmt.Fprintln(os.Stderr, "write remained blocked")
		os.Exit(2)
	}
	select {
	case <-ch:
		fmt.Println("EXPLICIT_DELIVERED")
	case <-time.After(2 * time.Second):
		fmt.Fprintln(os.Stderr, "explicit SIGPIPE was lost")
		os.Exit(2)
	}
	select {
	case sig := <-ch:
		fmt.Fprintln(os.Stderr, "extra signal:", sig)
		os.Exit(2)
	case <-time.After(100 * time.Millisecond):
		fmt.Println("NO_LEAK")
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGPIPE); err != nil {
		fmt.Fprintln(os.Stderr, "self kill:", err)
		os.Exit(2)
	}
	select {
	case <-ch:
		fmt.Println("SELF_KILL_DELIVERED")
	case <-time.After(2 * time.Second):
		fmt.Fprintln(os.Stderr, "same-process SIGPIPE was lost")
		os.Exit(2)
	}
}
