// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

//go:build unix

package interp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

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
