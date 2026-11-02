//go:build unix

package interp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
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
			// Output returns immutable snapshots after Wait joins the copiers.
			want, err := exec.CommandContext(ctx, reference, "-c", tc.script).Output()
			if err != nil {
				var referr []byte
				if ee, ok := err.(*exec.ExitError); ok {
					referr = ee.Stderr
				}
				t.Fatalf("GNU: %v %s", err, referr)
			}
			node, err := syntax.NewParser().Parse(strings.NewReader(tc.script), "virtual-self")
			if err != nil {
				t.Fatal(err)
			}
			var got, stderr lockedBuffer
			r, err := New(StdIO(nil, &got, &stderr))
			if err != nil {
				t.Fatal(err)
			}
			defer r.stopSignalSubscriptions()
			r.subshellLevel = 1 << 30
			if err = r.Run(ctx, node); err != nil {
				t.Fatalf("run: %v stderr=%s", err, &stderr)
			}
			if got.String() != string(want) {
				t.Fatalf("got %q want %q stderr=%q", got.String(), want, stderr.String())
			}
		})
	}
}

// asyncReplacementRunner is an asynchronous-list subshell whose virtual
// BASHPID is exactly virtualPid and whose shared exec replacement state holds
// attempt. It sends no signal by itself.
func asyncReplacementRunner(t *testing.T, virtualPid int, attempt *execReplacementAttempt) *Runner {
	t.Helper()
	parent, err := New()
	if err != nil {
		t.Fatal(err)
	}
	r := parent.Subshell()
	r.asyncList = true
	r.subshellLevel = virtualPid - os.Getpid()
	if r.bashPID() != virtualPid || r.bashPID() == r.shellPid() {
		t.Fatalf("virtual BASHPID %d, want %d distinct from $$ %d", r.bashPID(), virtualPid, r.shellPid())
	}
	r.execReplacement.current.Store(attempt)
	return r
}

// TestVirtualSelfYieldsToPublishedReplacementPid pins the routing order with a
// deterministic collision instead of relying on sequential PID allocation: the
// virtual BASHPID is set equal to the PID of a helper child this test owns and
// published as the live exec replacement. TERM must reach that child, not end
// the virtual subshell with 143. No other PID is signalled.
func TestVirtualSelfYieldsToPublishedReplacementPid(t *testing.T) {
	helper := exec.Command("sleep", "30")
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	var waitErr error
	waited := make(chan struct{})
	go func() { waitErr = helper.Wait(); close(waited) }()
	t.Cleanup(func() {
		select {
		case <-waited:
		default:
			_ = helper.Process.Kill()
			<-waited
		}
	})
	attempt := &execReplacementAttempt{ready: make(chan struct{})}
	attempt.publish(helper.Process.Pid)
	r := asyncReplacementRunner(t, helper.Process.Pid, attempt)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status := r.builtin(ctx, syntax.Pos{}, "kill", []string{"-TERM", strconv.Itoa(helper.Process.Pid)})
	if status.exiting || status.code != 0 {
		t.Fatalf("kill -TERM collided identity: %+v; want delivery to the replacement child", status)
	}
	select {
	case <-waited:
		err := waitErr
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatalf("helper wait: %v", err)
		}
		ws, ok := exitErr.Sys().(syscall.WaitStatus)
		if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGTERM {
			t.Fatalf("helper ended %v, want SIGTERM", err)
		}
	case <-ctx.Done():
		t.Fatal("TERM did not reach the replacement child")
	}
}

// TestVirtualSelfReplacementReadiness covers the readiness handoff the kill
// routing waits on: a pending attempt must not block past cancellation, and a
// failed start (PID 0) leaves virtual self-signals virtual and refuses $$.
func TestVirtualSelfReplacementReadiness(t *testing.T) {
	sig, _ := signalByName("ALRM")
	// Synthetic identity above the Linux/macOS kernel PID range: a routing
	// regression cannot reach a real process.
	virtualPid := os.Getpid() + 1<<30

	t.Run("pending-cancelled", func(t *testing.T) {
		r := asyncReplacementRunner(t, virtualPid, &execReplacementAttempt{ready: make(chan struct{})})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan exitStatus, 1)
		go func() { done <- r.builtin(ctx, syntax.Pos{}, "kill", []string{"-ALRM", strconv.Itoa(virtualPid)}) }()
		select {
		case status := <-done:
			t.Fatalf("kill returned %+v before publication or cancellation", status)
		case <-time.After(50 * time.Millisecond):
		}
		cancel()
		select {
		case status := <-done:
			if status.exiting || status.code != 1 {
				t.Fatalf("cancelled wait: %+v, want non-exiting failure", status)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("kill stayed blocked on an unpublished replacement after cancellation")
		}
	})

	t.Run("failed-start", func(t *testing.T) {
		attempt := &execReplacementAttempt{ready: make(chan struct{})}
		attempt.publish(0)
		attempt.publish(os.Getpid()) // later publications are ignored
		if pid, ok := attempt.await(context.Background()); !ok || pid != 0 {
			t.Fatalf("await = %d, %v; want failed start 0", pid, ok)
		}
		r := asyncReplacementRunner(t, virtualPid, attempt)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		status := r.builtin(ctx, syntax.Pos{}, "kill", []string{"-ALRM", strconv.Itoa(virtualPid)})
		if !status.exiting || int(status.code) != 128+sigNum(sig) {
			t.Fatalf("virtual self after failed start: %+v", status)
		}
		// $$ with a failed replacement start is refused without a kernel send.
		r = asyncReplacementRunner(t, virtualPid, attempt)
		if status = r.builtin(ctx, syntax.Pos{}, "kill", []string{"-0", strconv.Itoa(r.shellPid())}); status.exiting || status.code != 1 {
			t.Fatalf("$$ after failed start: %+v, want refusal", status)
		}
	})
}
