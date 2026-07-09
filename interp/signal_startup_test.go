//go:build unix

// Copyright (c) 2026, the outpost authors
// See LICENSE for licensing information

package interp

import (
	"bytes"
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"testing"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func runStartupIgnoredScript(t *testing.T, env expand.Environ, src string) string {
	t.Helper()
	file, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	r, err := New(Env(env), StdIO(nil, &stdout, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), file); err != nil {
		t.Fatal(err)
	}
	return stdout.String()
}

func TestTrapStartupIgnoredFromHardIgnoreEnv(t *testing.T) {
	env := expand.ListEnviron(BashyHardIgnoreEnv + "=USR1")
	got := runStartupIgnoredScript(t, env, "trap 'echo bad' USR1; trap -p USR1")
	if want := "trap -- '' SIGUSR1\n"; got != want {
		t.Fatalf("wrong trap output\nwant:\n%sgot:\n%s", want, got)
	}

	got = runStartupIgnoredScript(t, env, "trap - USR1; trap -p USR1")
	if want := "trap -- '' SIGUSR1\n"; got != want {
		t.Fatalf("wrong reset output\nwant:\n%sgot:\n%s", want, got)
	}
}

func TestTrapStartupIgnoredFromInheritedDisposition(t *testing.T) {
	file, err := syntax.NewParser().Parse(strings.NewReader("trap 'echo bad' USR1; trap -p USR1"), "")
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	r, err := New(Env(expand.ListEnviron(os.Environ()...)), StdIO(nil, &stdout, nil))
	if err != nil {
		t.Fatal(err)
	}
	signal.Ignore(syscall.SIGUSR1)
	r.Reset()
	restoreExecSignal(syscall.SIGUSR1)
	if err := r.Run(context.Background(), file); err != nil {
		t.Fatal(err)
	}
	got := stdout.String()
	if want := "trap -- '' SIGUSR1\n"; got != want {
		t.Fatalf("wrong inherited trap output\nwant:\n%sgot:\n%s", want, got)
	}
}
