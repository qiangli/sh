// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func TestHistoryProcessSubstitutionReader(t *testing.T) {
	out := runHistScript(t, `HISTFILE=/dev/null
set -o history
set -o histexpand
printf 'seed\n'
cat < <(printf '%s\n' !!)
printf 'tail\n'
printf '%s\n' !!
set +o history
`)
	want := "seed\ncat < <(printf '%s\\n' printf 'seed\\n')\nprintf\nseed\\n\ntail\nprintf '%s\\n' printf 'tail\\n'\nprintf\ntail\\n\n"
	if out != want {
		t.Fatalf("output = %q; want %q", out, want)
	}
}

// Buffer the FIFO to deterministically finish its producer before dispatching
// the parent command; both valid scheduling orders must expand exactly once.
func TestHistoryProcessSubstitutionRunsBeforeReader(t *testing.T) {
	histReset()
	t.Cleanup(histReset)
	src := "HISTFILE=/dev/null\nset -o history\nset -o histexpand\necho seed\ncat < <(echo !!)\necho tail\necho !!\nset +o history\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(src), path)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	base := DefaultOpenHandler()
	r, err := New(Dir(dir), StdIO(nil, &out, &out), OpenHandler(func(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
		f, err := base(ctx, path, flag, perm)
		if err == nil && strings.Contains(filepath.Base(path), fifoNamePrefix) && flag == os.O_RDONLY {
			data, e := io.ReadAll(f)
			f.Close()
			if e != nil {
				return nil, e
			}
			return &historyBufferedPipe{bytes.NewBuffer(data)}, nil
		}
		return f, err
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := r.Run(ctx, file); err != nil {
		t.Fatal(err)
	}
	want := "seed\ncat < <(echo echo seed)\necho seed\ntail\necho echo tail\necho tail\n"
	if out.String() != want {
		t.Fatalf("output = %q; want %q", out.String(), want)
	}
}

type historyBufferedPipe struct{ *bytes.Buffer }

func (*historyBufferedPipe) Close() error { return nil }

// Hold a nested substitution open while its parent consumes another history
// event. A process-global suppression counter drops and later replays that
// parent command. Channels establish the overlap without scheduling sleeps.
func TestHistoryConcurrentSubstitutionReader(t *testing.T) {
	histReset()
	t.Cleanup(histReset)
	src := "HISTFILE=/dev/null\nset -o history\nset -o histexpand\nexec 3< <(printf '%s' \"$(held-child)\")\nawait-child\necho parent\necho !!\nrelease-child\ncat <&3\nwait\nset +o history\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(path, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(src), path)
	if err != nil {
		t.Fatal(err)
	}
	entered, released := make(chan struct{}), make(chan struct{})
	var out bytes.Buffer
	base := DefaultExecHandler(0)
	r, err := New(Dir(dir), StdIO(nil, &out, &out), ExecHandler(func(ctx context.Context, args []string) error {
		switch args[0] {
		case "held-child":
			close(entered)
			select {
			case <-released:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		case "await-child":
			select {
			case <-entered:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		case "release-child":
			close(released)
			return nil
		}
		return base(ctx, args)
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := r.Run(ctx, file); err != nil {
		t.Fatal(err)
	}
	want := "parent\necho echo parent\necho parent\n"
	if out.String() != want {
		t.Fatalf("output = %q; want %q (no suppressed or duplicated parent command)", out.String(), want)
	}
}
