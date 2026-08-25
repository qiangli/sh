// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

//go:build linux

package interp_test

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestBackgroundExternalLaunchVisibleBeforeNextStatement(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(oldProcs)

	for i := 0; i < 20; i++ {
		marker := filepath.Join(t.TempDir(), "started")
		src := fmt.Sprintf("/usr/bin/touch %q & [ -e %q ]", marker, marker)
		file, err := syntax.NewParser().Parse(strings.NewReader(src), "")
		if err != nil {
			t.Fatal(err)
		}
		runner, err := interp.New(interp.WithJobCarrier(new(testCarrier)))
		if err != nil {
			t.Fatal(err)
		}
		if err := runner.Run(context.Background(), file); err != nil {
			t.Fatalf("iteration %d: parent continued before external launch: %v", i, err)
		}
	}
}
