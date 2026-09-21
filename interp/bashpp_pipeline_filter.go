// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"io"
	"os/exec"
	"sync"
)

// B7 — the island-function-as-pipeline-filter adapter. Classic pipes stay
// bytes ("producer | filter | consumer" moves an undifferentiated byte
// stream, never the B5 line/value stream), so this file does NOT build on
// bashPPLineProcess's line-channel: it reuses the same underlying pieces one
// level down —
//
//   - bashPPNativeProcessGroup / bashPPNativeKill / bashPPProcessExitStatus
//     (interp/bashpp_native_process_{unix,other}.go): the exact platform
//     split B14 already established for process-group creation, group-kill,
//     and bash-style (128+N) exit-status mapping.
//   - prepareBackgroundJobCmd / recordBackgroundProcessGroup
//     (interp/os_unix.go, interp/os_notunix.go): the shell's OWN existing
//     job-control wiring, already used by every real external command placed
//     in a pipeline (interp/handler.go), so a filter joins the SAME job pgid
//     an ordinary external pipeline stage would — no parallel job-control
//     model.
//   - the same exact-once Wait / kill-vs-reap mutex pattern bashPPCmdSource
//     uses, so a pid is never signalled after it has been reaped and reused.
//
// xonsh's pipeline/job-control model (xonsh/procs/pipelines.py) is the
// source-derived behavior this adapter must not regress: a downstream stage
// that exits early (e.g. `filter | head -1`) delivers SIGPIPE to the filter's
// stdout writes rather than hanging it, Ctrl-C delivered to the pipeline's
// foreground pgid reaches every real member including the filter, and the
// filter is reaped exactly once whichever side (upstream EOF, downstream
// close, or cancellation) ends the pipeline first. See the fixture file
// header for the pinned commit and license.
type bashPPPipelineFilter struct {
	cmd *exec.Cmd

	ctx    context.Context
	cancel context.CancelFunc

	stdinDone  chan struct{}
	stdoutDone chan struct{}
	stderrDone chan struct{}
	stderrBuf  []byte

	mu      sync.Mutex
	reaped  bool
	copyErr error

	killOnce sync.Once
	waitOnce sync.Once
	status   int
	waitErr  error
}

// bashPPStartPipelineFilter starts cmd as a real child process wired as one
// stage of a shell pipeline: stdin copies from upstream, stdout copies to
// downstream, both as raw bytes, and stderr is drained the same way B14
// drains it. The caller must not have set cmd.Stdin/Stdout/Stderr.
//
// ctx carries the job's background-process value (bgProcCtxKey) exactly as
// an ordinary external pipeline command's context does, so
// prepareBackgroundJobCmd joins this child to the job's pgid (or seeds it,
// for the first real process in the pipeline) instead of starting a second,
// disconnected group.
func bashPPStartPipelineFilter(ctx context.Context, cmd *exec.Cmd, upstream io.Reader, downstream io.Writer, nonPrimary bool) (*bashPPPipelineFilter, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	bashPPNativeProcessGroup(cmd)
	prepareBackgroundJobCmd(ctx, cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if bg, _ := ctx.Value(bgProcCtxKey{}).(*bgProc); bg != nil {
		recordBackgroundProcessGroup(bg, cmd.Process.Pid, nonPrimary)
	}

	fctx, cancel := context.WithCancel(ctx)
	f := &bashPPPipelineFilter{
		cmd: cmd, ctx: fctx, cancel: cancel,
		stdinDone: make(chan struct{}), stdoutDone: make(chan struct{}), stderrDone: make(chan struct{}),
	}

	go func() {
		<-fctx.Done()
		f.kill()
	}()

	go func() {
		defer close(f.stdinDone)
		defer stdin.Close()
		// A downstream-EOF (the reader side closing) or upstream producer
		// error surfaces here as an ordinary write failure once the child's
		// stdin pipe is gone — the SIGPIPE-adjacent case xonsh's pipeline
		// model treats as a normal pipeline-teardown event, not a bug: it is
		// recorded, not escalated to a panic.
		if _, err := io.Copy(stdin, upstream); err != nil {
			f.mu.Lock()
			if f.copyErr == nil {
				f.copyErr = err
			}
			f.mu.Unlock()
		}
	}()
	go func() {
		defer close(f.stdoutDone)
		if _, err := io.Copy(downstream, stdout); err != nil {
			f.mu.Lock()
			if f.copyErr == nil {
				f.copyErr = err
			}
			f.mu.Unlock()
			// The downstream stage is gone (xonsh CommandPipeline._end's
			// broken-pipe teardown, e.g. `filter | head -1` closing early):
			// cancel so a filter watching for that signal can exit on its
			// own, and keep draining stdout to EOF regardless so the child
			// never blocks writing into a pipe nobody reads — the same "every
			// process is drained and reaped" invariant bashPPLineProcess.Wait
			// documents.
			f.cancel()
			_, _ = io.Copy(io.Discard, stdout)
		}
	}()
	go func() {
		defer close(f.stderrDone)
		data, _ := io.ReadAll(stderr)
		f.mu.Lock()
		f.stderrBuf = data
		f.mu.Unlock()
	}()

	return f, nil
}

func (f *bashPPPipelineFilter) kill() {
	f.killOnce.Do(func() {
		f.mu.Lock()
		reaped := f.reaped
		f.mu.Unlock()
		if !reaped {
			bashPPNativeKill(f.cmd)
		}
	})
}

// Wait blocks until the child exits, drains and reaps it, and reports its
// bash-style exit status. Exact-once: every call returns the identical
// (status, error), matching bashPPLineProcess.Wait and bashPPCmdSource.Wait.
func (f *bashPPPipelineFilter) Wait() (int, error) {
	f.waitOnce.Do(func() {
		<-f.stdinDone
		<-f.stdoutDone
		<-f.stderrDone
		werr := f.cmd.Wait()
		f.mu.Lock()
		f.reaped = true
		copyErr := f.copyErr
		f.mu.Unlock()
		f.status = bashPPProcessExitStatus(werr)
		switch {
		case f.ctx.Err() != nil:
			f.waitErr = f.ctx.Err()
		case copyErr != nil:
			f.waitErr = copyErr
		}
		f.cancel()
	})
	return f.status, f.waitErr
}

// Close cancels the filter (if still running), drains its streams and reaps
// it. Safe to call more than once and after full consumption.
func (f *bashPPPipelineFilter) Close() error {
	f.cancel()
	_, err := f.Wait()
	return err
}

// Stderr returns the child's fully drained standard error, empty until Wait
// has returned.
func (f *bashPPPipelineFilter) Stderr() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.stderrBuf)
}
