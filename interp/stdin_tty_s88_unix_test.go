//go:build unix

package interp

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/creack/pty"
	"golang.org/x/term"
	"mvdan.cc/sh/v3/syntax"
)

// TestS88InteractiveCommandPreservesTTYStdin is a public reducer for a login
// session running a foreground provider such as mesg. Interactive source text
// may come from fd 0, but a child command must still inherit the session's tty
// as fd 0; the noninteractive stdin-script tail is not a substitute terminal.
func TestS88InteractiveCommandPreservesTTYStdin(t *testing.T) {
	for _, interactive := range []bool{true, false} {
		t.Run(map[bool]string{true: "interactive", false: "noninteractive-login-reader"}[interactive], func(t *testing.T) {
			ptmx, tty, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer ptmx.Close()
			defer tty.Close()

			const src = "public-tty-probe\n"
			file, err := syntax.NewParser().Parse(strings.NewReader(src), "")
			if err != nil {
				t.Fatal(err)
			}
			var childStdin io.Reader
			capture := func(next ExecHandlerFunc) ExecHandlerFunc {
				return func(ctx context.Context, args []string) error {
					childStdin = HandlerCtx(ctx).Stdin
					return nil
				}
			}
			r, err := New(
				Interactive(interactive),
				StdIO(tty, io.Discard, io.Discard),
				WithBashSource([]byte(src)),
				ExecHandlers(capture),
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Run(context.Background(), file); err != nil {
				t.Fatal(err)
			}
			f, ok := childStdin.(interface{ Fd() uintptr })
			if !ok || !term.IsTerminal(int(f.Fd())) {
				t.Fatalf("foreground child stdin = %T, want terminal fd", childStdin)
			}
		})
	}
}
