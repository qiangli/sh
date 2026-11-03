//go:build darwin

package interp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func TestDarwinProcSubstBrokenPipeIsolation(t *testing.T) {
	const marker = "SH_TEST_DARWIN_PROCSUBST_EPIPE"
	if os.Getenv(marker) == "1" {
		signals := make(chan os.Signal, 8)
		signal.Notify(signals, syscall.SIGPIPE)
		defer signal.Stop(signals)
		released := make(chan struct{})
		var out syncBuffer
		r, err := New(StdIO(nil, &out, &out), CallHandler(func(ctx context.Context, args []string) ([]string, error) {
			if len(args) > 0 {
				switch args[0] {
				case "holdwrite":
					select {
					case <-released:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
					return []string{":"}, nil
				case "releasewrite":
					close(released)
					return []string{":"}, nil
				}
			}
			return args, nil
		}))
		if err != nil {
			t.Fatal(err)
		}
		file, err := syntax.NewParser().Parse(strings.NewReader(`: <(holdwrite; echo unused; echo CHILD_CONTINUED >&2); releasewrite; wait; echo PARENT_SURVIVED`), "")
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.Run(ctx, file); err != nil {
			t.Fatal(err)
		}
		status := int(r.lastBang.exit.code)
		fmt.Printf("child=%d output=%q\n", status, out.String())
		select {
		case s := <-signals:
			t.Fatalf("producer leaked OS signal %v", s)
		default:
		}
		if status != 141 || out.String() != "PARENT_SURVIVED\n" {
			t.Fatal("broken pipe escaped producer boundary")
		}
		reference, err := sourceAliasBashReference()
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ prefix, child string }{
			{},
			{prefix: "trap '' PIPE; "},
			{prefix: "trap 'echo PARENT_PIPE' PIPE; "},
			{child: "trap 'echo CHILD_PIPE >&2' PIPE; "},
			{child: "trap 'exit 23' PIPE; "},
			{child: "trap 'echo WRONG_PIPE >&2' PIPE; trap - PIPE; "},
		} {
			prefix := tc.prefix
			src := prefix + `: <(` + tc.child + `printf '%131072s' x 2>/dev/null; echo CHILD_CONTINUED >&2); p=$!; wait "$p"; printf 'status=%s\n' "$?"; echo PARENT_SURVIVED`
			file, err := syntax.NewParser().Parse(strings.NewReader(src), "")
			if err != nil {
				t.Fatal(err)
			}
			var actual syncBuffer
			runner, err := New(StdIO(nil, &actual, &actual))
			if err != nil {
				t.Fatal(err)
			}
			if err := runner.Run(ctx, file); err != nil {
				t.Fatal(err)
			}
			want, err := exec.CommandContext(ctx, reference, "-c", src).CombinedOutput()
			if err != nil {
				t.Fatalf("reference %q: %v %s", prefix, err, want)
			}
			if actual.String() != string(want) {
				t.Fatalf("prefix %q: got %q want %q", prefix, actual.String(), want)
			}
			select {
			case sig := <-signals:
				t.Fatalf("prefix %q child %q leaked %v", prefix, tc.child, sig)
			default:
			}
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDarwinProcSubstBrokenPipeIsolation$")
	cmd.Env = []string{marker + "=1"}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOSH_PROG=") && !strings.HasPrefix(entry, "GOSH_CMD=") && !strings.HasPrefix(entry, marker+"=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated producer: %v\n%s", err, out)
	}
}

func TestDarwinProcSubstUnusedHistory(t *testing.T) {
	const marker = "SH_TEST_DARWIN_PROCSUBST_HISTORY"
	if os.Getenv(marker) == "1" {
		const src = "HISTFILE=/dev/null\nset -o history\nset -o histexpand\necho prefix/<(echo unused)/suffix >/dev/null\necho !:*\necho <(echo unused)/suffix >/dev/null\necho !:*\necho TAIL\nset +o history\n"
		path := filepath.Join(t.TempDir(), "history.sh")
		if err := os.WriteFile(path, []byte(src), 0600); err != nil {
			t.Fatal(err)
		}
		reference, err := sourceAliasBashReference()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		want, err := exec.CommandContext(ctx, reference, path).CombinedOutput()
		if err != nil {
			t.Fatalf("reference: %v %s", err, want)
		}
		for range 50 {
			histReset()
			file, err := syntax.NewParser().Parse(strings.NewReader(src), path)
			if err != nil {
				t.Fatal(err)
			}
			var out syncBuffer
			r, err := New(StdIO(nil, &out, &out), WithSignalResetter(OSSignalResetter{}), WithStandaloneSignalDefaults())
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Run(ctx, file); err != nil {
				t.Fatalf("run: %v %q", err, out.String())
			}
			if out.String() != string(want) {
				t.Fatalf("history got %q want %q", out.String(), want)
			}
		}
		histReset()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDarwinProcSubstUnusedHistory$")
	cmd.Env = []string{marker + "=1"}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOSH_PROG=") && !strings.HasPrefix(entry, "GOSH_CMD=") && !strings.HasPrefix(entry, marker+"=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("history helper: %v\n%s", err, out)
	}
}
