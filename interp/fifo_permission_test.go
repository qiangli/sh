// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

//go:build unix

package interp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"mvdan.cc/sh/v3/syntax"
)

// TestFIFOReadOpenWaitsForWriter covers the FIFO rendezvous used when the
// shell sources a named pipe. A read-only open must not expose EOF merely
// because its writer has not connected yet.
func TestFIFOReadOpenWaitsForWriter(t *testing.T) {
	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "source.fifo")
	if err := mkfifo(fifoPath, 0o600); err != nil {
		t.Fatalf("mkfifo failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	readResult := make(chan struct {
		data string
		err  error
	}, 1)
	go func() {
		f, err := openPath(ctx, fifoPath, os.O_RDONLY, 0)
		if err != nil {
			readResult <- struct {
				data string
				err  error
			}{err: err}
			return
		}
		defer f.Close()
		data, err := io.ReadAll(f)
		readResult <- struct {
			data string
			err  error
		}{string(data), err}
	}()

	// Ensure the reader reaches the open before the writer. This is the order
	// that exposed the premature EOF in bash-5.3's source6.sub fixture.
	time.Sleep(25 * time.Millisecond)
	writer, err := openPath(ctx, fifoPath, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	if _, err := io.WriteString(writer, "echo four - OK\n"); err != nil {
		t.Fatalf("write FIFO: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	result := <-readResult
	if result.err != nil {
		t.Fatalf("read FIFO: %v", result.err)
	}
	if want := "echo four - OK\n"; result.data != want {
		t.Fatalf("FIFO contents = %q, want %q", result.data, want)
	}
}

func TestFIFOReadOpenContextCancel(t *testing.T) {
	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "no_writer.fifo")
	if err := mkfifo(fifoPath, 0o600); err != nil {
		t.Fatalf("mkfifo failed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	f, err := openPath(ctx, fifoPath, os.O_RDONLY, 0)
	if f != nil {
		f.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context error, got %v", err)
	}
}

func TestOpenPathDoesNotTreatENXIOAsFIFO(t *testing.T) {
	f, probeErr := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if probeErr == nil {
		f.Close()
		t.Skip("process has a controlling terminal")
	}
	if !errors.Is(probeErr, unix.ENXIO) {
		t.Skipf("/dev/tty does not report ENXIO on this host: %v", probeErr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	opened, err := openPath(ctx, "/dev/tty", os.O_RDWR, 0)
	if opened != nil {
		opened.Close()
	}
	if !errors.Is(err, unix.ENXIO) {
		t.Fatalf("openPath error = %v, want ENXIO (not a FIFO context error)", err)
	}
}

// TestFIFOOpenContextCancel verifies that opening a FIFO for writing when no reader
// is connected respects context cancellation and returns ctx.Err() rather than blocking forever.
func TestFIFOOpenContextCancel(t *testing.T) {
	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "no_reader.fifo")

	if err := mkfifo(fifoPath, 0o637); err != nil {
		t.Fatalf("mkfifo failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		f, err := openPath(ctx, fifoPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o666)
		if err == nil {
			f.Close()
		}
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			t.Errorf("expected context error, got: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Errorf("FAIL: openPath hung indefinitely on FIFO open")
	}
}

// TestFIFOBackgroundJobRedirectCancel verifies that a background job redirecting to
// a FIFO cleans up properly when the runner context is cancelled.
func TestFIFOBackgroundJobRedirectCancel(t *testing.T) {
	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "bg_job.fifo")

	if err := mkfifo(fifoPath, 0o637); err != nil {
		t.Fatalf("mkfifo failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), Dir(dir))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	script := "echo hello >> bg_job.fifo &"
	prog, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if err := r.Run(ctx, prog); err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("Run failed unexpectedly: %v", err)
	}

	// Give background job goroutine time to complete cleanup
	time.Sleep(50 * time.Millisecond)
}

// TestSupplementaryGroupsResolution verifies that $GROUPS array variable resolves
// the user's groups on Unix systems.
func TestSupplementaryGroupsResolution(t *testing.T) {
	var stdout bytes.Buffer
	r, err := New(StdIO(nil, &stdout, nil))
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	prog, err := syntax.NewParser().Parse(strings.NewReader("echo ${GROUPS[*]}"), "")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if err := r.Run(context.Background(), prog); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		t.Errorf("GROUPS variable output is empty")
	} else {
		t.Logf("GROUPS output: %s", output)
	}
}
