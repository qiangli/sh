// Copyright (c) 2025, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// TestHashBuiltin_P records a path with `hash -p` and verifies that
// `hash -t` reports it.
func TestHashBuiltin_P(t *testing.T) {
	t.Parallel()

	// Create a temporary executable.
	dir := t.TempDir()
	exe := filepath.Join(dir, "mycmd")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho hello\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	var stdout strings.Builder
	runner, err := interp.New(
		interp.StdIO(nil, &stdout, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// `hash -p /tmp/mycmd mycmd; hash -t mycmd`
	prog := `hash -p ` + exe + ` mycmd; hash -t mycmd`
	file, err := syntax.NewParser().Parse(strings.NewReader(prog), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(ctx, file); err != nil {
		t.Fatalf("hash -p; hash -t failed: %v", err)
	}
	got := strings.TrimSpace(stdout.String())
	if got != exe {
		t.Fatalf("hash -t printed %q, want %q", got, exe)
	}
}

// TestHashBuiltin_P_missingArgs verifies that `hash -p` without a
// path or without names produces a usage error.
func TestHashBuiltin_P_missingArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		script string
		want   string // substring of stderr
	}{
		{
			script: "hash -p",
			want:   "hash: -p: option requires an argument",
		},
		{
			script: "hash -p /nonexistent",
			want:   "hash: -p: option requires an argument",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.script, func(t *testing.T) {
			t.Parallel()
			var stderr strings.Builder
			runner, err := interp.New(
				interp.StdIO(nil, nil, &stderr),
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			file, err := syntax.NewParser().Parse(strings.NewReader(tc.script), "")
			if err != nil {
				t.Fatal(err)
			}
			_ = runner.Run(ctx, file) // ignore error; we check stderr
			if !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("stderr = %q, want substring %q", stderr.String(), tc.want)
			}
		})
	}
}
