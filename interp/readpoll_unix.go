// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build unix

package interp

import (
	"context"
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// timeoutReader wraps f in a poll/select-based reader that honours the
// deadline. On unix this is always reliable (terminals, pipes, fifos),
// whereas (*os.File).SetReadDeadline silently no-ops for fds not registered
// with the runtime poller — e.g. a fifo opened via `exec 9<>p` on linux,
// which would otherwise make `read -u 9 -t …` block past its timeout.
func timeoutReader(ctx context.Context, f *os.File, deadline time.Time) io.Reader {
	return &timeoutFileReader{ctx: ctx, file: f, deadline: deadline}
}

func signalReader(ctx context.Context, f *os.File, wake <-chan struct{}) io.Reader {
	return &timeoutFileReader{ctx: ctx, file: f, wake: wake}
}

func fdReadableNow(f *os.File) bool {
	pollFd := []unix.PollFd{{
		Fd:     int32(f.Fd()),
		Events: unix.POLLIN | unix.POLLHUP | unix.POLLERR,
	}}
	n, err := unix.Poll(pollFd, 0)
	return err == nil && n > 0 && pollFd[0].Revents != 0
}

type timeoutFileReader struct {
	ctx      context.Context
	file     *os.File
	deadline time.Time
	wake     <-chan struct{}
}

func (r *timeoutFileReader) Read(p []byte) (int, error) {
	fd := int(r.file.Fd())
	if fd < 0 {
		return 0, os.ErrInvalid
	}
	for {
		// Propagate a real cancellation, but not a plain deadline — a passed
		// deadline still gets one non-blocking poll below so already-buffered
		// input is read first.
		if err := r.ctx.Err(); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return 0, err
		}
		// Wait for readability up to the deadline. A non-positive remaining
		// still does one non-blocking poll (msec 0), so data that is already
		// available — a here-string, or a fifo with bytes ready — is read
		// even when the timeout is tiny or already elapsed, matching bash
		// (ready input is consumed before a -t timeout is reported). poll(2)
		// is used uniformly (no FD_SETSIZE limit, unlike select).
		msec := 100
		if !r.deadline.IsZero() {
			remaining := time.Until(r.deadline)
			msec = int(remaining.Milliseconds())
			if msec < 0 {
				msec = 0
			} else if msec == 0 && remaining > 0 {
				msec = 1 // sub-millisecond remaining: round up, don't busy-spin
			}
		}
		pollFd := []unix.PollFd{{
			Fd:     int32(fd),
			Events: unix.POLLIN | unix.POLLHUP | unix.POLLERR,
		}}
		n, err := unix.Poll(pollFd, int(msec))
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		if n > 0 && pollFd[0].Revents != 0 {
			// Readable (data, EOF, or hangup) — let the real read decide.
			return r.file.Read(p)
		}
		if r.wake != nil {
			select {
			case <-r.wake:
				return 0, errReadInterrupted
			default:
			}
		}
		// Nothing ready; only give up once the deadline has truly passed.
		if !r.deadline.IsZero() && time.Until(r.deadline) <= 0 {
			return 0, os.ErrDeadlineExceeded
		}
	}
}
