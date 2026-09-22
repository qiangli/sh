// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestReadTimeoutRequiresFailedRead(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "complete line beats expired context", err: nil, want: false},
		{name: "failed read observes expired context", err: io.EOF, want: true},
		{name: "deadline error", err: os.ErrDeadlineExceeded, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := readErrorIsTimeout(ctx, tc.err, time.Millisecond); got != tc.want {
				t.Fatalf("readErrorIsTimeout() = %v, want %v", got, tc.want)
			}
		})
	}

	if readErrorIsTimeout(ctx, errors.New("read failed"), 0) {
		t.Fatal("a read without -t must not be classified as a timeout")
	}
}
