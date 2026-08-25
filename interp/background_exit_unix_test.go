// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

//go:build unix

package interp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

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
