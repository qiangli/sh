// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

//go:build unix

package interp_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestAsyncExternalSurvivesShellExit(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script func(string) string
	}{
		{"delayed", func(out string) string { return fmt.Sprintf("/bin/sh -c 'sleep 1; : > %q' &", out) }},
		{"fast", func(out string) string { return fmt.Sprintf("/usr/bin/touch %q &", out) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			file, err := syntax.NewParser().Parse(strings.NewReader(tc.script(out)), "")
			if err != nil {
				t.Fatal(err)
			}
			runner, err := interp.New()
			if err != nil {
				t.Fatal(err)
			}
			if err := runner.Run(context.Background(), file); err != nil {
				t.Fatal(err)
			}
			// Reset models the runner's teardown after a complete shell file.
			runner.Reset()
			deadline := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(out); err == nil {
					return
				} else if !os.IsNotExist(err) {
					t.Fatal(err)
				}
				if time.Now().After(deadline) {
					t.Fatalf("background command did not create %s", out)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestRunFileDoesNotWaitForExternalBackgroundCompletion(t *testing.T) {
	for _, src := range []string{
		"/bin/sleep 1 &",
		"echo() { /bin/sleep 1; }; echo &",
	} {
		file, err := syntax.NewParser().Parse(strings.NewReader(src), "")
		if err != nil {
			t.Fatal(err)
		}
		runner, err := interp.New()
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		if err := runner.Run(context.Background(), file); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed >= 500*time.Millisecond {
			t.Fatalf("file run waited for %q background completion: %v", src, elapsed)
		}
	}
}
