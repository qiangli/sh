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

type recordingSignalHost struct {
	ignored []string
}

func (*recordingSignalHost) ResetDefault(int, string) {}

func (h *recordingSignalHost) IgnoreStartup(_ int, name string) {
	h.ignored = append(h.ignored, name)
}

func TestBridgedStartupIgnoreIsRestoredOnlyByOptInHost(t *testing.T) {
	file, err := syntax.NewParser().Parse(strings.NewReader(
		"trap 'echo bad' USR1; trap - USR1; kill -s USR1 $$; echo after"), "")
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	host := &recordingSignalHost{}
	r, err := New(
		Env(expand.ListEnviron(BashyHardIgnoreEnv+"=USR1")),
		StdIO(nil, &stdout, nil),
		WithSignalResetter(host),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), file); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(host.ignored, ","), "USR1"; got != want {
		t.Fatalf("startup ignores restored = %q, want %q", got, want)
	}
	if got, want := stdout.String(), "after\n"; got != want {
		t.Fatalf("startup-ignored signal was made mutable\n got: %q\nwant: %q", got, want)
	}
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

	env = expand.ListEnviron(BashyHardIgnoreEnv + "=INT")
	got = runStartupIgnoredScript(t, env, "trap 'echo bad' INT; trap -p INT")
	if want := "trap -- '' SIGINT\n"; got != want {
		t.Fatalf("wrong bridged INT trap output\nwant:\n%sgot:\n%s", want, got)
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

func TestStartupIgnoredSignalIntNotInferredFromProcessDisposition(t *testing.T) {
	file, err := syntax.NewParser().Parse(strings.NewReader("trap -p INT"), "")
	if err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	r, err := New(Env(expand.ListEnviron(os.Environ()...)), StdIO(nil, &stdout, nil))
	if err != nil {
		t.Fatal(err)
	}
	signal.Ignore(syscall.SIGINT)
	r.Reset()
	restoreExecSignal(syscall.SIGINT)
	if err := r.Run(context.Background(), file); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("unexpected inherited INT trap output:\n%s", got)
	}
}
