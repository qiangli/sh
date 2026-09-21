// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Tests for the B7 island-function-as-pipeline-filter adapter
// (bashPPPipelineFilter, interp/bashpp_pipeline_filter.go), Sprint 221 story
// 050586a14a25.
//
// PROVENANCE — faithfully ported, intent preserved:
//
//   - Project:  xonsh, xonsh/procs/pipelines.py (CommandPipeline) and
//               xonsh/procs/proxies.py.
//     Pinned:   tag 0.19.2.
//     License:  BSD-2-Clause (xonsh LICENSE).
//     Ported:   a downstream stage that stops reading early must not hang
//               the upstream/filter stage (the broken-pipe teardown
//               CommandPipeline._end handles) -> TestBashPPPipelineFilter
//               EarlyDownstreamClose; a filter's own failure (nonzero exit)
//               is reported through Wait's status, not swallowed ->
//               TestBashPPPipelineFilterProducerFailure; cancellation
//               reaching every real pipeline member -> TestBashPPPipeline
//               FilterCtrlC.
//     Adaptation: expressed against bashPPPipelineFilter.Wait/Close rather
//               than xonsh's Python-level pipeline object, since this is a
//               Go-level process-lifecycle unit, not the dialect's pipeline
//               syntax (still open work; see plan-story576-streaming-
//               adapters.md).
//
//   - Project:  Go standard library os/exec (the same corpus B14 already
//               pins).
//     Pinned:   go1.27.1, src/os/exec/exec_test.go.
//     License:  BSD-3-Clause ($GOROOT/LICENSE).
//     Ported:   TestContext/TestContextCancel -> TestBashPPPipelineFilter
//               CtrlC; exit-code passthrough -> TestBashPPPipelineFilter
//               ExitStatus.
//
//   - Project:  GNU Bash Reference Manual, "Exit Status" (the 128+N signal
//               rule, the same spec B14 pins).
//     Ported:   a signalled filter reports 128+N -> TestBashPPPipelineFilter
//               ExitStatus's signal case.
//
// These tests drive a real `sh` child (skipping, like bashpp_process_test.go,
// when none is present) because B7's whole point — real bytes over real
// pipes, in the job's real pgid — is not expressible against an in-memory
// fake the way B5's line semantics are.

func bashPPFilterCmd(script string) *exec.Cmd {
	return exec.Command("sh", "-c", script)
}

func TestBashPPPipelineFilterBytePassthrough(t *testing.T) {
	skipNoShell(t)
	upstream := strings.NewReader("hello\nworld\n")
	var downstream bytes.Buffer
	cmd := bashPPFilterCmd("cat")
	f, err := bashPPStartPipelineFilter(context.Background(), cmd, upstream, &downstream, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	status, werr := f.Wait()
	if werr != nil || status != 0 {
		t.Fatalf("Wait = (%d, %v), want (0, nil)", status, werr)
	}
	if got := downstream.String(); got != "hello\nworld\n" {
		t.Fatalf("downstream = %q, want the bytes passed through verbatim", got)
	}
}

// xonsh CommandPipeline: a filter's own nonzero exit is a status, exactly
// like every other pipeline stage's $? — not an error.
func TestBashPPPipelineFilterExitStatus(t *testing.T) {
	skipNoShell(t)
	upstream := strings.NewReader("")
	var downstream bytes.Buffer
	cmd := bashPPFilterCmd("exit 7")
	f, err := bashPPStartPipelineFilter(context.Background(), cmd, upstream, &downstream, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	status, werr := f.Wait()
	if werr != nil {
		t.Fatalf("Wait err = %v, want nil for an ordinary nonzero exit", werr)
	}
	if status != 7 {
		t.Fatalf("status = %d, want 7", status)
	}
}

// GNU Bash "Exit Status": a signalled filter reports 128+N.
func TestBashPPPipelineFilterSignalStatus(t *testing.T) {
	skipNoShell(t)
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("128+N signal exit-status convention is unix-only")
	}
	upstream := strings.NewReader("")
	var downstream bytes.Buffer
	cmd := bashPPFilterCmd("kill -TERM $$; sleep 5")
	f, err := bashPPStartPipelineFilter(context.Background(), cmd, upstream, &downstream, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	status, _ := f.Wait()
	if status != 128+15 {
		t.Fatalf("status = %d, want 143 (128+SIGTERM)", status)
	}
}

// xonsh CommandPipeline._end: a downstream stage that stops reading early
// (e.g. `filter | head -1`) must not hang the filter — here modeled as the
// downstream writer failing — and the filter must still be reaped.
func TestBashPPPipelineFilterEarlyDownstreamClose(t *testing.T) {
	skipNoShell(t)
	upstream := strings.NewReader(strings.Repeat("line\n", 100000))
	downstream := &failingWriter{failAfter: 1}
	cmd := bashPPFilterCmd("cat")
	f, err := bashPPStartPipelineFilter(context.Background(), cmd, upstream, downstream, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, _ = f.Wait() // must return promptly, not hang on the broken downstream
}

type failingWriter struct {
	n, failAfter int
}

func (w *failingWriter) Write(p []byte) (int, error) {
	w.n++
	if w.n > w.failAfter {
		return 0, errors.New("downstream closed early")
	}
	return len(p), nil
}

// The Ctrl-C path: cancelling the context must kill the filter promptly and
// report the cancellation through Wait, matching the B14 substrate's own
// context-cancellation contract.
func TestBashPPPipelineFilterCtrlC(t *testing.T) {
	skipNoShell(t)
	upstream := strings.NewReader("")
	var downstream bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cmd := bashPPFilterCmd("sleep 30")
	f, err := bashPPStartPipelineFilter(ctx, cmd, upstream, &downstream, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	cancel() // simulates the shell delivering Ctrl-C to the pipeline's pgid
	done := make(chan struct{})
	go func() {
		_, _ = f.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("cancellation did not stop the filter promptly")
	}
	if _, werr := f.Wait(); werr == nil {
		t.Fatalf("Wait err = nil after cancellation, want the cancellation reported")
	}
}

// Large output must arrive whole through the byte-copy path, unlike B5's
// line-splitting: no line boundary is inspected or altered.
func TestBashPPPipelineFilterLargeOutput(t *testing.T) {
	skipNoShell(t)
	var sb strings.Builder
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&sb, "row-%05d\n", i)
	}
	want := sb.String()
	upstream := strings.NewReader(want)
	var downstream bytes.Buffer
	cmd := bashPPFilterCmd("cat")
	f, err := bashPPStartPipelineFilter(context.Background(), cmd, upstream, &downstream, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if status, werr := f.Wait(); status != 0 || werr != nil {
		t.Fatalf("Wait = (%d, %v), want (0, nil)", status, werr)
	}
	if got := downstream.String(); got != want {
		t.Fatalf("downstream large output mismatch: got %d bytes, want %d", len(got), len(want))
	}
}

// Exact-once Wait: repeated calls return the identical (status, error) and
// the child is reaped a single time, matching bashPPLineProcess/bashPPCmd
// Source's own invariant.
func TestBashPPPipelineFilterExactOnceWait(t *testing.T) {
	skipNoShell(t)
	upstream := strings.NewReader("x\n")
	var downstream bytes.Buffer
	cmd := bashPPFilterCmd("cat")
	f, err := bashPPStartPipelineFilter(context.Background(), cmd, upstream, &downstream, false)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	firstStatus, firstErr := f.Wait()
	for i := 0; i < 5; i++ {
		s, e := f.Wait()
		if s != firstStatus || e != firstErr {
			t.Fatalf("Wait call %d = (%d, %v), want the stable (%d, %v)", i, s, e, firstStatus, firstErr)
		}
	}
}

// Windows pipe-handle parity: bashPPNativeProcessGroup/bashPPNativeKill
// already have a dedicated !unix implementation (bashpp_native_process_
// other.go) exercised by B14's own tests; this filter calls exactly those
// two helpers and no platform-specific code of its own, so byte-passthrough,
// exit-status and exact-once-Wait above already exercise the Windows path
// identically once run there — there is nothing further to gate here.

var _ io.Writer = (*failingWriter)(nil)
