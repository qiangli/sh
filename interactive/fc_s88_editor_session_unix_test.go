//go:build unix

package interactive

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
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
IFS= read -r edit || { printf 'S88_EDITOR_READ_FAILED:%s\n' "$?" >&2; exit 40; }
[ "$edit" = 's/world/goodbye/' ] || { printf 'S88_EDITOR_BAD_LINE:<%s>\n' "$edit" >&2; exit 41; }
printf '%s\n' 'echo hello goodbye' >"$1" || { printf '%s\n' S88_EDITOR_WRITE_FAILED >&2; exit 42; }
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
	var writeMu sync.Mutex
	var transcript bytes.Buffer
	changed := make(chan struct{}, 1)
	terminalError := make(chan error, 1)
	writeTerminal := func(s string) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_, err := master.Write([]byte(s))
		return err
	}
	go func() {
		buf := make([]byte, 4096)
		answeredCPR := 0
		for {
			n, readErr := master.Read(buf)
			if n > 0 {
				mu.Lock()
				requestedCPR := strings.Count(transcript.String()+string(buf[:n]), "\x1b[6n")
				answersDue := requestedCPR - answeredCPR
				mu.Unlock()
				for range answersDue {
					// readline expects its terminal emulator to answer the ANSI
					// cursor-position query. This PTY test is that emulator.
					if err := writeTerminal("\x1b[1;1R"); err != nil {
						terminalError <- err
						return
					}
				}
				answeredCPR = requestedCPR
				// Expose prompt/output markers only after this emulator has
				// answered preceding queries. Otherwise the test can type a
				// command before readline receives its pending CPR response.
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

	waitFor := func(markers ...string) {
		t.Helper()
		deadline := time.NewTimer(8 * time.Second)
		defer deadline.Stop()
		for {
			mu.Lock()
			got := transcript.String()
			mu.Unlock()
			rest := got
			found := true
			for _, marker := range markers {
				at := strings.Index(rest, marker)
				if at < 0 {
					found = false
					break
				}
				rest = rest[at+len(marker):]
			}
			if found {
				return
			}
			select {
			case <-changed:
			case err := <-terminalError:
				t.Fatalf("terminal response failed: %v", err)
			case <-deadline.C:
				t.Fatalf("timeout waiting for %q; transcript=%q", markers, got)
			}
		}
	}
	write := func(s string) {
		t.Helper()
		if err := writeTerminal(s); err != nil {
			t.Fatal(err)
		}
	}

	waitFor("S88_PROMPT> ")
	write("echo hello world\r")
	// Match execution output, not the echoed input, then wait for the next
	// prompt before sending another command to the terminal.
	waitFor("echo hello world", "\n", "hello world\r\n", "S88_PROMPT> ")
	write(fmt.Sprintf("fc -e %s\r", editor))
	waitFor("S88_EDITOR_READY")
	write("s/world/goodbye/\n")
	waitFor("S88_EDITOR_DONE")
	waitFor("echo hello goodbye", "\n", "hello goodbye\r\n", "S88_PROMPT> ")
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

// TestFcS88EditorConsumesNonTTYInteractiveInput reproduces the certification
// boundary directly: `sh -i` may receive interactive input from a pipe rather
// than a terminal, and fc's editor must consume the lines following the fc
// command before the shell parser reads them as later commands.
func TestFcS88EditorConsumesNonTTYInteractiveInput(t *testing.T) {
	if _, err := exec.LookPath("ed"); err != nil {
		t.Skipf("ed not found: %v", err)
	}

	stdin, input, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	go func() {
		_, _ = input.WriteString("echo hello world\nfc -e ed\ns/world/goodbye/\nw\nq\n")
		_ = input.Close()
	}()

	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	runner, err := interp.New(
		interp.Dir(dir),
		interp.Env(expand.ListEnviron("HOME="+dir, "HISTFILE=/dev/null", "PATH=/bin:/usr/bin")),
		interp.StdIO(stdin, &stdout, &stderr),
		interp.Interactive(true),
		interp.WithPosixMode(true),
	)
	if err != nil {
		t.Fatal(err)
	}

	err = Run(context.Background(), Options{
		Runner:        runner,
		PosixMode:     true,
		PlainTerminal: true,
		Stdin:         stdin,
		Stdout:        &stdout,
		Stderr:        &stderr,
		PS1:           func() string { return "" },
		PS2:           func() string { return "" },
	})
	if err != nil {
		t.Fatalf("interactive run: %v; stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if got := stdout.String(); !strings.Contains(got, "hello goodbye\n") {
		t.Fatalf("stdout = %q, want edited command output", got)
	}
	if got := stderr.String(); got != "echo hello goodbye\n" {
		t.Fatalf("stderr = %q, want only fc's edited-command trace", got)
	}
}
