// Copyright (c) 2019, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

//go:build unix

package interp_test

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
	"mvdan.cc/sh/v3/interp"
)

func TestRunnerTerminalStdIO(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		files func(*testing.T) (secondary io.Writer, primary io.Reader)
		want  string
	}{
		{"Nil", func(t *testing.T) (io.Writer, io.Reader) {
			return nil, strings.NewReader("\n")
		}, "\n"},
		{"Pipe", func(t *testing.T) (io.Writer, io.Reader) {
			pr, pw := io.Pipe()
			return pw, pr
		}, "end\n"},
		{"Pseudo", func(t *testing.T) (io.Writer, io.Reader) {
			primary, secondary, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			return secondary, primary
		}, "012end\r\n"},
	}
	file := parse(t, nil, `
		for n in 0 1 2 3; do if [[ -t $n ]]; then echo -n $n; fi; done; echo end
	`)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			secondary, primary := test.files(t)
			// some secondary ends can be used as stdin too
			secondaryReader, _ := secondary.(io.Reader)

			r, _ := interp.New(interp.StdIO(secondaryReader, secondary, secondary))
			go func() {
				// To mimic [os/exec.Cmd.Start], use a goroutine.
				if err := r.Run(context.Background(), file); err != nil {
					t.Error(err)
				}
			}()

			got, err := bufio.NewReader(primary).ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("\nwant: %q\ngot:  %q", test.want, got)
			}
			if closer, ok := secondary.(io.Closer); ok {
				if err := closer.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if closer, ok := primary.(io.Closer); ok {
				if err := closer.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestRunnerTerminalExec(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		start func(*testing.T, *exec.Cmd) io.Reader
		want  string
	}{
		{"Nil", func(t *testing.T, cmd *exec.Cmd) io.Reader {
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			return strings.NewReader("\n")
		}, "\n"},
		{"Pipe", func(t *testing.T, cmd *exec.Cmd) io.Reader {
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = cmd.Stdout
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			return out
		}, "end\n"},
		{"Pseudo", func(t *testing.T, cmd *exec.Cmd) io.Reader {
			// Note that we avoid pty.Start,
			// as it closes the secondary terminal via a defer,
			// possibly before the command has finished.
			// That can lead to "signal: hangup" flakes.
			primary, secondary, err := pty.Open()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stdin = secondary
			cmd.Stdout = secondary
			cmd.Stderr = secondary
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			return primary
		}, "012end\r\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cmd := exec.Command(os.Getenv("GOSH_PROG"),
				"for n in 0 1 2 3; do if [[ -t $n ]]; then echo -n $n; fi; done; echo end")
			primary := test.start(t, cmd)

			got, err := bufio.NewReader(primary).ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("\nwant: %q\ngot:  %q", test.want, got)
			}
			if closer, ok := primary.(io.Closer); ok {
				if err := closer.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestRunnerTerminalExecInheritedSparseFD compares external terminal commands
// under bash and gosh, then checks Bashy's sparse inherited-FD path. FD 8 has
// no open shell-managed descriptors below it: this is the shape inherited from
// a parent that has kept a terminal on a high descriptor.
func TestRunnerTerminalExecInheritedSparseFD(t *testing.T) {
	const script = "tty >/dev/null && /bin/echo terminal"

	run := func(t *testing.T, cmd *exec.Cmd) string {
		t.Helper()
		primary, secondary, err := pty.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer primary.Close()
		defer secondary.Close()
		cmd.Stdin = secondary
		cmd.Stdout = secondary
		cmd.Stderr = secondary
		// Install the already-open terminal at its real sparse descriptor,
		// rather than using ExtraFiles (which would manufacture fds 3 through
		// 7). This test is deliberately not parallel: the fd-table change
		// lasts only through cmd.Start and is restored before reading output.
		oldFD8, oldErr := unix.Dup(8)
		if err := unix.Dup2(int(secondary.Fd()), 8); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			if oldErr == nil {
				_ = unix.Dup2(oldFD8, 8)
				_ = unix.Close(oldFD8)
			} else {
				_ = unix.Close(8)
			}
			t.Fatal(err)
		}
		if oldErr == nil {
			_ = unix.Dup2(oldFD8, 8)
			_ = unix.Close(oldFD8)
		} else {
			_ = unix.Close(8)
		}
		got, err := bufio.NewReader(primary).ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
		return got
	}

	goshCmd := func(src string) *exec.Cmd {
		gotCmd := exec.Command(os.Getenv("GOSH_PROG"), src)
		gotCmd.Env = append(os.Environ(), interp.BashyInheritedFdsEnv+"=8")
		return gotCmd
	}
	want := run(t, exec.Command("bash", "-c", script))
	got := run(t, goshCmd(script))
	if got != want {
		t.Fatalf("external command output differs with inherited sparse FD 8:\nwant: %q\n got: %q", want, got)
	}

	// The first external command does not mention fd 8. It must not make
	// that already-open terminal disappear before the later external command
	// receives it. This is the sparse-fd path which ExtraFiles cannot encode.
	got = run(t, goshCmd("tty >/dev/null && GOSH_CMD=fd8_is_terminal $GOSH_PROG"))
	if got != "terminal\r\n" {
		t.Fatalf("external command lost inherited terminal fd 8: got %q", got)
	}

	// An explicit shell close must override inheritance and remain effective
	// across a preceding external command; otherwise the raw ambient fd would
	// leak into children even though the shell considers it closed.
	got = run(t, goshCmd("exec 8>&-; /bin/true; GOSH_CMD=fd8_is_terminal $GOSH_PROG"))
	if got != "not-terminal\r\n" {
		t.Fatalf("explicitly closed inherited fd 8 leaked to external command: got %q", got)
	}
}

// TestRunnerExternalTerminalTools checks the shell-owned boundary shared by
// terminal-aware system tools. The tools themselves remain host providers:
// this only compares the environment, session, and controlling terminal they
// receive when launched through gosh with those received through bash.
func TestRunnerExternalTerminalTools(t *testing.T) {
	for _, name := range []string{"logname", "mesg", "write"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s not available: %v", name, err)
		}
	}
	// Some sandboxed hosts permit a shell to invoke write but deny the Go
	// process that backs the interpreter from doing so. That is a host policy,
	// rather than a terminal/session result, so do not compare providers there.
	if err := exec.Command("write", "__gosh_no_such_login__").Run(); err != nil {
		if _, exited := err.(*exec.ExitError); !exited {
			t.Skipf("write cannot be launched by this host: %v", err)
		}
	}

	const script = `
for tool in logname mesg write; do
	case $tool in
	logname) logname ;;
	mesg) mesg ;;
	write) write __gosh_no_such_login__ ;;
	esac
	printf '<%s:%s>\n' "$tool" "$?"
done
`
	run := func(t *testing.T, cmd *exec.Cmd) string {
		t.Helper()
		primary, err := pty.Start(cmd)
		if err != nil {
			t.Fatal(err)
		}
		got, readErr := io.ReadAll(primary)
		if closeErr := primary.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
		if err := cmd.Wait(); err != nil {
			t.Fatalf("%v; output: %q", err, got)
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		return string(got)
	}

	want := run(t, exec.Command("bash", "-c", script))
	got := run(t, exec.Command(os.Getenv("GOSH_PROG"), script))
	if got != want {
		t.Fatalf("terminal-aware system tools differ across the shell boundary:\nwant: %q\n got: %q", want, got)
	}
}

// TestRunnerExecStartFailureContinues verifies that a kernel exec failure is
// an ordinary command failure. A FIFO with execute permission passes shell
// lookup but cannot be executed, so it isolates that launch boundary without
// depending on a particular system utility.
func TestRunnerExecStartFailureContinues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not-a-program")
	if err := unix.Mkfifo(path, 0o755); err != nil {
		t.Fatal(err)
	}

	file := parse(t, nil, "./not-a-program; echo survived")
	var out strings.Builder
	r, err := interp.New(interp.Dir(dir), interp.StdIO(nil, &out, &out))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Run(context.Background(), file); err != nil {
		t.Fatalf("runner stopped after an exec failure: %v; output: %q", err, out.String())
	}
	if !strings.Contains(out.String(), "survived\n") {
		t.Fatalf("later command did not run after an exec failure: %q", out.String())
	}
}

func shortPathName(path string) (string, error) {
	panic("only works on windows")
}
