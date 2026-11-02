//go:build unix

package interp

import (
	"bytes"
	"context"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func TestVirtualSelfSignalRouting(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatal(err)
	}
	child := r.Subshell()
	child.startupIgnored = nil
	sig, ok := signalByName("ALRM")
	if !ok {
		t.Fatal("missing ALRM")
	}
	// Direct routing checks send no OS signals, including the foreign-PID cases.
	for _, pid := range []int{r.shellPid(), child.bashPID() + 1000000, -1, 0} {
		if handled, _ := child.signalVirtualSelf(pid, sig); handled {
			t.Fatalf("claimed foreign/ancestor target %d", pid)
		}
	}
	handled, status := child.signalVirtualSelf(child.bashPID(), sig)
	if !handled || !status.exiting || int(status.code) != 128+sigNum(sig) {
		t.Fatalf("default routing: %v %+v", handled, status)
	}
	// Uncatchable signals must not acquire ignore semantics merely because
	// trap reporting retains an empty callback for them.
	child.trapCallbacks = map[string]string{"KILL": "", "STOP": "", "EXIT": "must-not-run"}
	child.startupIgnored = map[string]bool{"KILL": true}
	kill, _ := signalByName("KILL")
	if handled, status = child.signalVirtualSelf(child.bashPID(), kill); !handled || !status.exiting || int(status.code) != 128+sigNum(kill) {
		t.Fatalf("uncatchable KILL: %v %+v", handled, status)
	}
	if child.trapCallbacks["EXIT"] != "" {
		t.Fatal("KILL retained EXIT callback")
	}
	child.trapCallbacks["KILL"] = "must-not-run"
	if handled, status = child.signalVirtualSelf(child.bashPID(), kill); !handled || !status.exiting || int(status.code) != 128+sigNum(kill) {
		t.Fatalf("caught KILL: %v %+v", handled, status)
	}
	stop, _ := signalByName("STOP")
	child.trapCallbacks["STOP"] = "must-not-run"
	if handled, status = child.signalVirtualSelf(child.bashPID(), stop); !handled || status.exiting || status.code != 0 || len(child.pendingSig) != 0 {
		t.Fatalf("STOP no-op compatibility: %v %+v pending=%v", handled, status, child.pendingSig)
	}
	zero, _, _ := signalByNumber(0)
	if handled, status = child.signalVirtualSelf(child.bashPID(), zero); !handled || status.exiting || status.code != 0 {
		t.Fatalf("probe routing: %v %+v", handled, status)
	}
}

func TestVirtualSelfSignalBash(t *testing.T) {
	// Check the local routing contract before running scripts. The test runner
	// below also places synthetic identities above the Linux/macOS kernel PID
	// range so a routing regression cannot target an adjacent process/thread.
	guard, err := New()
	if err != nil {
		t.Fatal(err)
	}
	child := guard.Subshell()
	sig, _ := signalByName("ALRM")
	if handled, _ := child.signalVirtualSelf(child.bashPID(), sig); !handled {
		t.Fatal("unsafe virtual target routing")
	}
	reference, err := sourceAliasBashReference()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, script string }{
		{"trap-replace-function-exit", `v=$(trap 'printf first' ALRM; kill -s ALRM "$BASHPID"; trap - ALRM; trap 'printf second; exit 7' ALRM; f() { kill -s ALRM "$BASHPID"; printf bad; }; f); s=$?; printf '<%s>:%s:parent\n' "$v" "$s"`},
		{"kill-skips-exit", `v=$(trap 'printf bad' EXIT; trap '' KILL; kill -KILL "$BASHPID"; printf bad); s=$?; printf '<%s>:%s:parent\n' "$v" "$s"`},
		{"catchable-default-exit-cleanup", `v=$(trap 'printf cleanup' EXIT; kill -ALRM "$BASHPID"; printf bad); s=$?; printf '<%s>:%s:parent
' "$v" "$s"`},
		{"default-exit", `v=$(kill -s ALRM "$BASHPID"; printf bad); s=$?; printf '<%s>:%s:parent\n' "$v" "$s"`},
		{"inherited-ignore", `trap '' ALRM; v=$(kill -s ALRM "$BASHPID"; printf child); printf '<%s>:%s\n' "$v" "$?"`},
		{"inherited-caught-reset", `trap 'printf bad-parent' ALRM; v=$(kill -s ALRM "$BASHPID"; printf bad-child); s=$?; printf '<%s>:%s:parent\n' "$v" "$s"`},
		{"ancestor-dollar", `trap 'printf parent-trap' ALRM; v=$(kill -s ALRM "$$"; printf child); printf '<%s>:%s\n' "$v" "$?"`},
		{"nested-self", `v=$(w=$(trap 'printf inner; exit 3' ALRM; kill -s ALRM "$BASHPID"; printf bad); printf '[%s]:%s' "$w" "$?"); printf '<%s>:%s\n' "$v" "$?"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// trap-ignore cases must not leak OS dispositions to later cases.
			saved, haveSaved := saveSignalDisposition(syscall.SIGALRM)
			signal.Reset(syscall.SIGALRM)
			restoreExecSignal(syscall.SIGALRM)
			t.Cleanup(func() {
				signal.Reset(syscall.SIGALRM)
				if haveSaved {
					restoreSignalDisposition(syscall.SIGALRM, saved)
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, reference, "-c", tc.script)
			var want, referr bytes.Buffer
			cmd.Stdout = &want
			cmd.Stderr = &referr
			if err := cmd.Run(); err != nil {
				t.Fatalf("GNU: %v %s", err, &referr)
			}
			node, err := syntax.NewParser().Parse(strings.NewReader(tc.script), "virtual-self")
			if err != nil {
				t.Fatal(err)
			}
			var got, stderr bytes.Buffer
			r, err := New(StdIO(nil, &got, &stderr))
			if err != nil {
				t.Fatal(err)
			}
			defer r.stopSignalSubscriptions()
			r.subshellLevel = 1 << 30
			if err = r.Run(ctx, node); err != nil {
				t.Fatalf("run: %v stderr=%s", err, &stderr)
			}
			if got.String() != want.String() {
				t.Fatalf("got %q want %q stderr=%q", got.String(), want.String(), stderr.String())
			}
		})
	}
}
