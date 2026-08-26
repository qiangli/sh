// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

//go:build !plan9 && !js

package interactive

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/ergochat/readline"
	"github.com/go-quicktest/qt"
	"mvdan.cc/sh/v3/interp"
)

func TestConfigureReadlineRunsBeforeConstruction(t *testing.T) {
	r, err := interp.New(interp.Interactive(true), interp.StdIO(strings.NewReader(""), io.Discard, io.Discard))
	qt.Assert(t, qt.IsNil(err))

	called := false
	err = Run(context.Background(), Options{
		Runner: r,
		Stdin:  strings.NewReader(""),
		Stdout: io.Discard,
		Stderr: io.Discard,
		ConfigureReadline: func(cfg *readline.Config) {
			called = true
			cfg.Undo = true
		},
	})
	qt.Assert(t, qt.IsNil(err))
	qt.Check(t, qt.IsTrue(called))
}

func TestPlainTerminalSkipsReadlineConfiguration(t *testing.T) {
	r, err := interp.New(interp.Interactive(true), interp.StdIO(strings.NewReader(""), io.Discard, io.Discard))
	qt.Assert(t, qt.IsNil(err))

	err = Run(context.Background(), Options{
		Runner:        r,
		Stdin:         strings.NewReader(""),
		Stdout:        io.Discard,
		Stderr:        io.Discard,
		PlainTerminal: true,
		ConfigureReadline: func(*readline.Config) {
			t.Fatal("plain terminal unexpectedly constructed readline")
		},
	})
	qt.Assert(t, qt.IsNil(err))
}
