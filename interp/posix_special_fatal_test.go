// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

package interp

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"mvdan.cc/sh/v3/syntax"
)

// TestInteractiveSpecialBuiltinErrorNonFatal covers VSC-PCTS #643: POSIX
// 2.8.1 makes an error in a special built-in abort a *non-interactive*
// shell, but an interactive shell must report it and carry on. Typing
// `unset` of a readonly variable at the prompt previously killed the whole
// bashy session (the interactive job-control assertion drives exactly that
// through `expect`).
func TestInteractiveSpecialBuiltinErrorNonFatal(t *testing.T) {
	run := func(t *testing.T, interactive bool) string {
		t.Helper()
		var buf bytes.Buffer
		opts := []RunnerOption{
			Params("-o", "posix"),
			StdIO(strings.NewReader(""), &buf, &buf),
		}
		if interactive {
			opts = append(opts, Interactive(true))
		}
		r, err := New(opts...)
		qt.Assert(t, qt.IsNil(err))
		f, err := syntax.NewParser().Parse(
			strings.NewReader("readonly x=1\nunset x\necho AFTER\n"), "")
		qt.Assert(t, qt.IsNil(err))
		_ = r.Run(context.Background(), f)
		return buf.String()
	}

	// Interactive: the readonly-unset error is reported but the shell keeps
	// running, so the following command executes.
	got := run(t, true)
	qt.Check(t, qt.StringContains(got, "cannot unset"))
	qt.Check(t, qt.StringContains(got, "AFTER"))

	// Non-interactive: the special-built-in error is fatal, so the shell
	// exits before the following command.
	got = run(t, false)
	qt.Check(t, qt.StringContains(got, "cannot unset"))
	qt.Check(t, qt.IsFalse(strings.Contains(got, "AFTER")),
		qt.Commentf("non-interactive shell should have exited: %q", got))
}
