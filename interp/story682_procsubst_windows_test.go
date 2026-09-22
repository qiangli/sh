// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build windows

package interp

import (
	"bytes"
	"io"
	"os"
	"testing"
	"time"
)

// Sprint 245, story 682: shopt1.sub runs `diff <($t1) <($t2)`, and our Go
// diff opens each substituted path twice. With the single-instance pipe of
// run #125 the second CreateFile failed with ERROR_PIPE_BUSY ("All pipe
// instances are busy"). The pipe now keeps an instance listening and serves
// every connection from the shell's copy of the body, so both opens read
// the whole substitution — which is what this pins.
func TestStory682ProcSubstPipeTwoOpens(t *testing.T) {
	t.Parallel()

	var r Runner
	pipe, err := r.newProcSubstPipe(true) // `<(cmd)`: the consumer reads
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.cleanup()
	path := pipe.path()
	body := []byte("line one\nline two\nline three\n")

	// The substitution's end blocks until the path is first opened, the way
	// a FIFO open pairs the two sides, so the first open runs concurrently.
	opened := make(chan *os.File, 1)
	failed := make(chan error, 1)
	go func() {
		f, err := os.OpenFile(path, os.O_RDONLY, 0)
		if err != nil {
			failed <- err
			return
		}
		opened <- f
	}()

	w, err := pipe.openWriter()
	if err != nil {
		t.Fatal(err)
	}
	var first *os.File
	select {
	case first = <-opened:
	case err := <-failed:
		t.Fatalf("first open: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the first open never completed")
	}

	// The second open, while the first connection is still live: this is
	// the one that used to fail.
	second, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}

	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for i, f := range []*os.File{first, second} {
		got, err := io.ReadAll(f)
		f.Close()
		if err != nil {
			t.Fatalf("reader %d: %v", i, err)
		}
		if !bytes.Equal(got, body) {
			t.Errorf("reader %d read %q, want the whole body %q", i, got, body)
		}
	}
}

// A `>(cmd)` pipe feeds one stdin and stays single-instance, so it is still
// opened exactly once; the second opener is refused rather than being given
// a write end nothing reads.
func TestStory682ProcSubstPipeOutIsSingleInstance(t *testing.T) {
	t.Parallel()

	var r Runner
	pipe, err := r.newProcSubstPipe(false) // `>(cmd)`: the consumer writes
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.cleanup()
	path := pipe.path()

	opened := make(chan *os.File, 1)
	failed := make(chan error, 1)
	go func() {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			failed <- err
			return
		}
		opened <- f
	}()

	stdin, err := pipe.openReader()
	if err != nil {
		t.Fatal(err)
	}
	var writer *os.File
	select {
	case writer = <-opened:
	case err := <-failed:
		t.Fatalf("open: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the open never completed")
	}
	defer writer.Close()
	if _, err := os.OpenFile(path, os.O_WRONLY, 0); err == nil {
		t.Error("a second open of a >(cmd) pipe must be refused")
	}
	if _, err := writer.Write([]byte("in\n")); err != nil {
		t.Fatal(err)
	}
	writer.Close()
	got, err := io.ReadAll(stdin)
	stdin.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "in\n" {
		t.Errorf("the substitution read %q, want %q", got, "in\n")
	}
}
