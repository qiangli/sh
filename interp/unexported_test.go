// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func TestElapsedString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in    time.Duration
		posix bool
		want  string
	}{
		{time.Nanosecond, false, "0m0.000s"},
		{time.Millisecond, false, "0m0.001s"},
		{time.Millisecond, true, "0.00"},
		{2500 * time.Millisecond, false, "0m2.500s"},
		{2500 * time.Millisecond, true, "2.50"},
		{
			10*time.Minute + 10*time.Second,
			false,
			"10m10.000s",
		},
		{
			10*time.Minute + 10*time.Second,
			true,
			"610.00",
		},
		{31 * time.Second, false, "0m31.000s"},
		{102 * time.Second, false, "1m42.000s"},
	}
	for _, tc := range tests {
		t.Run(tc.in.String(), func(t *testing.T) {
			got := elapsedString(tc.in, tc.posix)
			if got != tc.want {
				t.Fatalf("wanted %q, got %q", tc.want, got)
			}
		})
	}
}

// TestUnsupportedHints guards against drift in the unsupportedHints map.
// Every key must still be recognized by IsBuiltin (otherwise the dispatcher
// won't see the name to fall into the default arm), and invoking the name
// must produce the expected "<name>: not supported in this shell — <hint>"
// shape so agentic callers can rely on it.
func TestUnsupportedHints(t *testing.T) {
	t.Parallel()

	for name, hint := range unsupportedHints {
		t.Run(name, func(t *testing.T) {
			if !IsBuiltin(name) {
				t.Fatalf("unsupportedHints lists %q but IsBuiltin doesn't recognize it; the dispatcher will never see this name", name)
			}

			file, err := syntax.NewParser().Parse(strings.NewReader(name), "")
			if err != nil {
				t.Fatalf("parsing %q: %v", name, err)
			}
			var stderr bytes.Buffer
			r, err := New(StdIO(nil, nil, &stderr))
			if err != nil {
				t.Fatalf("interp.New: %v", err)
			}
			runErr := r.Run(context.Background(), file)
			if runErr == nil {
				t.Fatalf("running %q: expected non-nil error", name)
			}
			wantPrefix := name + ": not supported in this shell — " + hint
			if got := strings.TrimRight(stderr.String(), "\n"); got != wantPrefix {
				t.Fatalf("running %q:\nwant stderr: %q\ngot:         %q", name, wantPrefix, got)
			}
		})
	}
}
