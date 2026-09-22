// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

//go:build windows

package interp

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// An anonymous pipe is the handle Go's runtime poller refuses, so
// SetReadDeadline cannot call its read off; the peek-based probe is what
// `read -t` relies on there.
func TestWindowsPipeReadinessProbe(t *testing.T) {
	t.Parallel()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()

	if windowsHandleKindOf(pr) != winHandlePipe {
		t.Fatalf("want an os.Pipe end to classify as a pipe")
	}
	if windowsReadReadyNow(pr) {
		t.Fatalf("an empty pipe must not report itself readable")
	}
	if _, err := pw.WriteString("x\n"); err != nil {
		t.Fatal(err)
	}
	if !windowsReadReadyNow(pr) {
		t.Fatalf("a pipe holding bytes must report itself readable")
	}
}

// The deadline must be honoured even though SetReadDeadline is rejected for
// the handle — the read7.sub / `sleep 1 | read -t 0.25` shape.
func TestWindowsTimeoutFileReaderHonoursDeadline(t *testing.T) {
	t.Parallel()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()

	r := &timeoutFileReader{
		ctx:      context.Background(),
		file:     pr,
		deadline: time.Now().Add(50 * time.Millisecond),
	}
	start := time.Now()
	var buf [1]byte
	if _, err := r.Read(buf[:]); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("want os.ErrDeadlineExceeded, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the read took %v, so the deadline was not honoured", elapsed)
	}
}

// Buffered input wins over an already-expired deadline, as in bash.
func TestWindowsTimeoutFileReaderReadsBufferedInput(t *testing.T) {
	t.Parallel()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()

	if _, err := pw.WriteString("hi"); err != nil {
		t.Fatal(err)
	}
	r := &timeoutFileReader{
		ctx:      context.Background(),
		file:     pr,
		deadline: time.Now().Add(-time.Second),
	}
	var buf [2]byte
	n, err := r.Read(buf[:])
	if err != nil {
		t.Fatalf("want the buffered bytes, got %v", err)
	}
	if got := string(buf[:n]); got != "hi" {
		t.Fatalf("want %q, got %q", "hi", got)
	}
}

// A cancelled runner context stops the poll rather than leaving the read
// stranded on a handle no deadline can reach.
func TestWindowsTimeoutFileReaderCancels(t *testing.T) {
	t.Parallel()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	r := &timeoutFileReader{ctx: ctx, file: pr}
	var buf [1]byte
	if _, err := r.Read(buf[:]); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// A pipe and the console are the two handles the runtime poller refuses, so
// they are the two that need the poll to stay cancellable.
func TestWindowsCancellableReaderCoversPipes(t *testing.T) {
	t.Parallel()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if cancellableReader(ctx, pr) == nil {
		t.Fatalf("want a cancellable reader for a pipe")
	}
	// An uncancellable context has nothing to interrupt the read with, so
	// the plain blocking read is kept.
	if cancellableReader(context.Background(), pr) != nil {
		t.Fatalf("want the plain read when the context cannot be cancelled")
	}
}

// A regular file's read completes without waiting for a peer; polling it
// would only add syscalls. Nor is a console read taken over: the wait for a
// user to press enter is not a hang.
func TestWindowsCancellableReaderSkipsRegularFiles(t *testing.T) {
	t.Parallel()
	f, err := os.CreateTemp(t.TempDir(), "readpoll")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if cancellableReader(ctx, f) != nil {
		t.Fatalf("want the plain read for a regular file")
	}
}
