package interp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func TestCommandSubstitutionExitTrap(t *testing.T) {
	cases := []struct{ name, script, want string }{
		{"natural", `v=$(trap 'printf done' EXIT); printf '<%s>:%s\n' "$v" "$?"`, "<done>:0\n"},
		{"explicit", `v=$(trap 'printf done' EXIT; exit 7); printf '<%s>:%s\n' "$v" "$?"`, "<done>:7\n"},
		{"entry-status", `v=$(trap 'printf "%s" "$?"' EXIT; false); printf '<%s>:%s\n' "$v" "$?"`, "<1>:1\n"},
		{"trap-status-preserved", `v=$(trap 'printf done; false' EXIT; exit 7); printf '<%s>:%s\n' "$v" "$?"`, "<done>:7\n"},
		{"trap-exit-overrides", `v=$(trap 'printf done; exit 9' EXIT; exit 7); printf '<%s>:%s\n' "$v" "$?"`, "<done>:9\n"},
		{"inherited", `trap 'printf parent' EXIT; v=$(printf body); printf '<%s>:%s\n' "$v" "$?"`, "<body>:0\nparent"},
		{"reset", `v=$(trap 'printf bad' EXIT; trap - EXIT; printf body); printf '<%s>:%s\n' "$v" "$?"`, "<body>:0\n"},
		{"nested", `v=$(trap 'printf outer' EXIT; v=$(trap 'printf inner' EXIT); printf '%s:' "$v"); printf '<%s>:%s\n' "$v" "$?"`, "<inner:outer>:0\n"},
		{"sourced", `v=$(command . ./trap.sh || printf BUG); printf '<%s>:%s\n' "$v" "$?"`, "<sourced>:0\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "trap.sh"), []byte("trap 'printf sourced' EXIT\n"), 0600); err != nil {
				t.Fatal(err)
			}
			file, err := syntax.NewParser().Parse(strings.NewReader(tc.script), "trap-test")
			if err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			r, err := New(Dir(dir), StdIO(nil, &out, &stderr))
			if err != nil {
				t.Fatal(err)
			}
			if err = r.Run(context.Background(), file); err != nil {
				t.Fatalf("run: %v stderr:%s", err, &stderr)
			}
			if out.String() != tc.want {
				t.Fatalf("stdout=%q want=%q stderr=%s", out.String(), tc.want, &stderr)
			}
		})
	}
}

func TestCommandSubstitutionExitTrapCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	file, err := syntax.NewParser().Parse(strings.NewReader(`v=$(trap 'printf trap-ran >&2' EXIT; cancel-test)`), "cancel-test")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	r, err := New(StdIO(nil, &out, &out), ExecHandlers(func(next ExecHandlerFunc) ExecHandlerFunc {
		return func(context.Context, []string) error { cancel(); return context.Canceled }
	}))
	if err != nil {
		t.Fatal(err)
	}
	err = r.Run(ctx, file)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want cancellation", err)
	}
	if strings.Contains(out.String(), "trap-ran") {
		t.Fatalf("trap ran after cancellation: %q", out.String())
	}
}

func TestCommandSubstitutionExitTrapPanic(t *testing.T) {
	file, err := syntax.NewParser().Parse(strings.NewReader(`v=$(trap 'printf trap-ran >&2' EXIT; panic-test)`), "panic-test")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	sentinel := errors.New("handler panic")
	r, err := New(StdIO(nil, &out, &out), ExecHandlers(func(next ExecHandlerFunc) ExecHandlerFunc {
		return func(context.Context, []string) error { panic(sentinel) }
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if got := recover(); got != sentinel {
			t.Errorf("panic=%v, want original sentinel", got)
		}
		if strings.Contains(out.String(), "trap-ran") {
			t.Errorf("trap ran during panic unwinding: %q", out.String())
		}
	}()
	_ = r.Run(context.Background(), file)
}
