// Copyright (c) 2025, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

// TestAwdBuiltin covers the bashy `awd DIR [--] cmd…` builtin: it runs the
// command with the cwd temporarily set to DIR, then fully restores the shell's
// cwd; it errors like cd on a bad dir; and it propagates the command's exit.
func TestAwdBuiltin(t *testing.T) {
	start := t.TempDir()
	elsewhere := t.TempDir()

	run := func(src string) (out, errOut string, code uint8) {
		var so, se bytes.Buffer
		r, err := New(Dir(start), StdIO(nil, &so, &se))
		if err != nil {
			t.Fatal(err)
		}
		f, perr := syntax.NewParser().Parse(strings.NewReader(src), "")
		if perr != nil {
			t.Fatal(perr)
		}
		rerr := r.Run(context.Background(), f)
		var st ExitStatus
		if errors.As(rerr, &st) {
			code = uint8(st)
		}
		return so.String(), se.String(), code
	}

	// Restoration: pwd before and after awd are identical (ephemeral chdir).
	out, _, code := run("pwd; awd " + elsewhere + " true; pwd")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || lines[0] != lines[1] {
		t.Errorf("cwd not restored: %q", out)
	}
	if code != 0 {
		t.Errorf("awd true: exit %d, want 0", code)
	}

	// During: the wrapped command observes DIR as its cwd.
	out, _, _ = run("pwd; awd " + elsewhere + " pwd")
	lines = strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || lines[0] == lines[1] {
		t.Errorf("awd did not change cwd for the command: %q", out)
	}

	// Exit status propagates from the wrapped command.
	if _, _, code := run("awd " + elsewhere + " false"); code != 1 {
		t.Errorf("awd false: exit %d, want 1", code)
	}

	// A missing directory errors like cd (non-zero, cd-style message).
	_, errOut, code := run("awd /no/such/dir true")
	if code == 0 {
		t.Error("awd on a missing dir should fail")
	}
	if !strings.Contains(errOut, "No such file or directory") {
		t.Errorf("missing-dir error wording: %q", errOut)
	}

	// Usage errors: no args, and a dir with no command.
	if _, _, code := run("awd"); code != 2 {
		t.Errorf("bare awd: exit %d, want 2", code)
	}
	if _, _, code := run("awd " + elsewhere); code != 2 {
		t.Errorf("awd DIR with no command: exit %d, want 2", code)
	}

	// The `--` separator lets the command be a builtin.
	out, _, _ = run("awd " + elsewhere + " -- echo ok")
	if strings.TrimSpace(out) != "ok" {
		t.Errorf("awd -- echo: %q", out)
	}
}
