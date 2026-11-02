//go:build unix

package interp

import (
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNativeSignalLifetimeChild(t *testing.T) {
	if os.Getenv("SH_TEST_NATIVE_SIGNAL_LIFETIME") != "1" {
		return
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestNativeProcessWaitJoinsSignalForwarder(t *testing.T) {
	for _, kill := range []bool{false, true} {
		name := "normal"
		if kill {
			name = "killed"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			input, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			defer writer.Close()
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestNativeSignalLifetimeChild$")
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "GOSH_PROG=") && !strings.HasPrefix(entry, "GOSH_CMD=") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			cmd.Env = append(cmd.Env, "SH_TEST_NATIVE_SIGNAL_LIFETIME=1")
			cmd.Stdin = input
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Kill()
			actualStop := sync.OnceFunc(forwardBashPPNativeSignalsWithReport(cmd.Process.Pid, nil))
			defer actualStop()
			entered, released, stopped := make(chan struct{}), make(chan struct{}), make(chan struct{})
			release := sync.OnceFunc(func() { close(released) })
			defer release()
			stop := sync.OnceFunc(func() { close(entered); <-released; actualStop(); close(stopped) })
			s := &bashPPNativeSession{cmd: cmd, done: make(chan struct{}), stopSignals: stop}
			waiterDone := make(chan struct{})
			go func() { defer close(waiterDone); s.waitNativeProcess(io.NopCloser(strings.NewReader(""))) }()
			if kill {
				if err := cmd.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("waiter did not retire signal forwarding after child exit")
			}
			select {
			case <-s.done:
				t.Fatal("completion published before forwarding joined")
			default:
			}
			// Race explicit owner close with natural/abnormal process completion. Both
			// share one stop operation; neither may return before the forwarder joins.
			ownerDone := make(chan struct{})
			go func() { defer close(ownerDone); s.close() }()
			release()
			for _, done := range []<-chan struct{}{stopped, s.done, waiterDone, ownerDone} {
				select {
				case <-done:
				case <-ctx.Done():
					t.Fatal("process cleanup did not finish")
				}
			}
			if (s.waitErr != nil) != kill {
				t.Fatalf("kill=%v waitErr=%v", kill, s.waitErr)
			}
			s.close() // repeat owner cleanup must remain harmless
		})
	}
}
