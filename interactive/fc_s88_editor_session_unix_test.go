//go:build unix

package interactive

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
)

// TestFcS88EditorSessionThroughInteractivePTY pins the boundary that the
// interpreter-only reducer cannot cover: readline must stop consuming terminal
// input while fc's editor owns the same PTY. The editor announces readiness
// before the test sends its edit line, so a green cannot come from pre-buffering
// the transcript for the shell loop.
func TestFcS88EditorSessionThroughInteractivePTY(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	if err := pty.Setsize(master, &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	editor := filepath.Join(dir, "s88-editor")
	editorScript := `#!/bin/sh
printf '%s\n' S88_EDITOR_READY >&2
IFS= read -r edit || exit 40
[ "$edit" = 's/world/goodbye/' ] || exit 41
printf '%s\n' 'echo hello goodbye' >"$1" || exit 42
printf '%s\n' S88_EDITOR_DONE >&2
`
	if err := os.WriteFile(editor, []byte(editorScript), 0o700); err != nil {
		t.Fatal(err)
	}

	runner, err := interp.New(
		interp.Dir(dir),
		interp.Env(expand.ListEnviron("HOME="+dir, "HISTFILE=/dev/null", "PATH=/bin:/usr/bin")),
		interp.StdIO(slave, slave, slave),
		interp.Interactive(true),
		interp.WithPosixMode(true),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Runner:    runner,
			PosixMode: true,
			Stdin:     slave,
			Stdout:    slave,
			Stderr:    slave,
			PS1:       func() string { return "S88_PROMPT> " },
			PS2:       func() string { return "S88_MORE> " },
		})
	}()

	var mu sync.Mutex
	var transcript bytes.Buffer
	changed := make(chan struct{}, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := master.Read(buf)
			if n > 0 {
				mu.Lock()
				_, _ = transcript.Write(buf[:n])
				mu.Unlock()
				select {
				case changed <- struct{}{}:
				default:
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	waitFor := func(marker string) {
		t.Helper()
		deadline := time.NewTimer(8 * time.Second)
		defer deadline.Stop()
		for {
			mu.Lock()
			got := transcript.String()
			mu.Unlock()
			if strings.Contains(got, marker) {
				return
			}
			select {
			case <-changed:
			case <-deadline.C:
				t.Fatalf("timeout waiting for %q; transcript=%q", marker, got)
			}
		}
	}
	write := func(s string) {
		t.Helper()
		if _, err := master.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}

	waitFor("S88_PROMPT> ")
	write("echo hello world\r")
	waitFor("hello world")
	write(fmt.Sprintf("fc -e %s\r", editor))
	waitFor("S88_EDITOR_READY")
	write("s/world/goodbye/\n")
	waitFor("S88_EDITOR_DONE")
	waitFor("hello goodbye")
	write("exit\r")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("interactive shell returned %v", err)
		}
	case <-time.After(8 * time.Second):
		mu.Lock()
		got := transcript.String()
		mu.Unlock()
		t.Fatalf("interactive shell did not exit; transcript=%q", got)
	}

	mu.Lock()
	got := transcript.String()
	mu.Unlock()
	for _, marker := range []string{"S88_EDITOR_READY", "S88_EDITOR_DONE", "echo hello goodbye", "hello goodbye"} {
		if !strings.Contains(got, marker) {
			t.Fatalf("transcript missing %q: %q", marker, got)
		}
	}
}
