// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

package interp_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"testing"

	"mvdan.cc/sh/v3/interp"
)

// testCarrier implements [interp.JobCarrier] by re-execing the test
// binary with GOSH_CMD=carrier (see TestMain), a hermetic helper that
// blocks reading stdin until EOF. Terminate closes the stdin pipe and
// kills the process; Wait reaps it and maps a signal death to its number
// via carrierExitSignal (build-tagged, 0 on non-unix).
type testCarrier struct {
	mu   sync.Mutex
	pids []int // every carrier PID handed out, for leak checks
}

type testCarrierProc struct {
	cmd   *exec.Cmd
	stdin io.Closer
}

func (c *testCarrier) StartCarrier(ctx context.Context) (interp.CarrierProcess, error) {
	cmd := exec.Command(os.Getenv("GOSH_PROG"))
	cmd.Env = append(os.Environ(), "GOSH_CMD=carrier")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.pids = append(c.pids, cmd.Process.Pid)
	c.mu.Unlock()
	return &testCarrierProc{cmd: cmd, stdin: stdin}, nil
}

func (p *testCarrierProc) Pid() int { return p.cmd.Process.Pid }

func (p *testCarrierProc) Wait() int {
	_ = p.cmd.Wait() // a signal death surfaces as *exec.ExitError; state below has the detail
	return carrierExitSignal(p.cmd.ProcessState)
}

func (p *testCarrierProc) Terminate() {
	p.stdin.Close()
	_ = p.cmd.Process.Kill()
}

// startedPids returns a snapshot of every carrier PID started so far.
func (c *testCarrier) startedPids() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]int(nil), c.pids...)
}

// runCarrierScript runs src to completion on a fresh runner configured
// with the given carrier and returns the combined stdout+stderr.
func runCarrierScript(t *testing.T, carrier interp.JobCarrier, src string, opts ...interp.RunnerOption) string {
	t.Helper()
	file := parse(t, nil, src)
	var buf concBuffer
	if carrier != nil {
		opts = append(opts, interp.WithJobCarrier(carrier))
	}
	opts = append(opts, interp.StdIO(nil, &buf, &buf))
	r, err := interp.New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	if err := r.Run(ctx, file); err != nil {
		var es interp.ExitStatus
		if !errors.As(err, &es) {
			t.Fatalf("Run: %v\noutput:\n%s", err, buf.String())
		}
	}
	return buf.String()
}

var numericPidRe = regexp.MustCompile(`^[1-9][0-9]*$`)

// TestJobCarrierIdentity checks that the carrier PID is one numeric
// identity used consistently by $!, jobs -p, wait, and wait -p, even for
// a job that never spawns an external process.
func TestJobCarrierIdentity(t *testing.T) {
	t.Parallel()
	c := new(testCarrier)
	out := runCarrierScript(t, c, `
{ :; } &
p=$!
jp=$(jobs -p)
jl=$(jobs -l)
wait -p w "$p"
echo "st=$?"
[ "$jp" = "$p" ] && echo jobs-ok
case "$jl" in *" $p "*) echo jobsl-ok;; esac
[ "$w" = "$p" ] && echo wait-ok
echo "pid=$p"
`)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	want := []string{"st=0", "jobs-ok", "jobsl-ok", "wait-ok"}
	if len(lines) != 5 || lines[0] != want[0] || lines[1] != want[1] ||
		lines[2] != want[2] || lines[3] != want[3] {
		t.Fatalf("unexpected output:\n%s", out)
	}
	pid := strings.TrimPrefix(lines[4], "pid=")
	if !numericPidRe.MatchString(pid) {
		t.Fatalf("$! = %q, want a real numeric PID", pid)
	}
	started := c.startedPids()
	if len(started) != 1 || fmt.Sprint(started[0]) != pid {
		t.Fatalf("$! = %s, but carrier started pids %v", pid, started)
	}
}

// TestJobCarrierRapidUniqueness starts jobs back to back and checks that
// every $! is a distinct real PID.
func TestJobCarrierRapidUniqueness(t *testing.T) {
	t.Parallel()
	out := runCarrierScript(t, new(testCarrier), `
for i in 1 2 3 4 5 6 7 8; do
	{ :; } &
	echo $!
done
wait
`)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 8 {
		t.Fatalf("want 8 pids, got:\n%s", out)
	}
	seen := make(map[string]bool)
	for _, pid := range lines {
		if !numericPidRe.MatchString(pid) {
			t.Fatalf("$! = %q, want a real numeric PID", pid)
		}
		if seen[pid] {
			t.Fatalf("duplicate $! %s in:\n%s", pid, out)
		}
		seen[pid] = true
	}
}

// TestJobCarrierStatusRetention checks that a finished job's status is
// retained: `wait $p` reports it once — including after a bare `wait`
// has reaped the job table, via the saved-status map — and a second
// `wait $p` gets bash's "not a child" 127.
func TestJobCarrierStatusRetention(t *testing.T) {
	t.Parallel()
	out := runCarrierScript(t, new(testCarrier), `
{ exit 7; } &
p=$!
wait
wait "$p"
echo "one=$?"
wait "$p" 2>/dev/null
echo "two=$?"
`)
	if got := strings.TrimSpace(out); got != "one=7\ntwo=127" {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

// TestJobCarrierSubshellIsolation checks that a subshell sees the
// parent's $! (the carrier PID) but cannot wait on the parent's job.
func TestJobCarrierSubshellIsolation(t *testing.T) {
	t.Parallel()
	out := runCarrierScript(t, new(testCarrier), `
{ :; } &
p=$!
( [ "$!" = "$p" ] && echo inherit-ok
  wait "$p" 2>/dev/null
  echo "sub=$?" )
wait "$p"
echo "par=$?"
`)
	if got := strings.TrimSpace(out); got != "inherit-ok\nsub=127\npar=0" {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

// TestJobCarrierCustomExecHandler checks that carrier identity does not
// depend on the default exec handler: a job whose commands are entirely
// satisfied by a custom ExecHandlers middleware still gets a real PID.
func TestJobCarrierCustomExecHandler(t *testing.T) {
	t.Parallel()
	handled := func(next interp.ExecHandlerFunc) interp.ExecHandlerFunc {
		return func(ctx context.Context, args []string) error {
			if args[0] == "frobnicate" {
				return nil // satisfied in-process; no OS process spawned
			}
			return next(ctx, args)
		}
	}
	out := runCarrierScript(t, new(testCarrier), `
{ frobnicate; exit 3; } &
p=$!
wait "$p"
echo "st=$?"
echo "pid=$p"
`, interp.ExecHandlers(handled))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || lines[0] != "st=3" {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if pid := strings.TrimPrefix(lines[1], "pid="); !numericPidRe.MatchString(pid) {
		t.Fatalf("$! = %q, want a real numeric PID", pid)
	}
}

// TestJobCarrierReset checks that the carrier option survives
// [Runner.Reset] and that a reset runner hands out fresh identities.
func TestJobCarrierReset(t *testing.T) {
	t.Parallel()
	c := new(testCarrier)
	var buf concBuffer
	r, err := interp.New(interp.WithJobCarrier(c), interp.StdIO(nil, &buf, &buf))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	src := "{ :; } & echo $!; wait $!"
	if err := r.Run(ctx, parse(t, nil, src)); err != nil {
		t.Fatal(err)
	}
	r.Reset()
	if err := r.Run(ctx, parse(t, nil, src)); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("unexpected output:\n%s", buf.String())
	}
	for _, pid := range lines {
		if !numericPidRe.MatchString(pid) {
			t.Fatalf("$! = %q, want a real numeric PID", pid)
		}
	}
	if lines[0] == lines[1] {
		t.Fatalf("same carrier PID handed out before and after Reset: %s", lines[0])
	}
}

// failingCarrier always errors from StartCarrier.
type failingCarrier struct{}

func (failingCarrier) StartCarrier(ctx context.Context) (interp.CarrierProcess, error) {
	return nil, errors.New("carrier unavailable")
}

// TestJobCarrierStartFailure checks that a failing carrier degrades to
// the legacy synthetic handle instead of breaking job control.
func TestJobCarrierStartFailure(t *testing.T) {
	t.Parallel()
	out := runCarrierScript(t, failingCarrier{}, `
{ exit 5; } &
p=$!
wait "$p"
echo "st=$? pid=$p"
`)
	if got := strings.TrimSpace(out); got != "st=5 pid=g1" {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

// TestNoJobCarrierKeepsSyntheticHandles pins the default behavior for
// embedders that do not opt in: pure-builtin jobs keep the opaque g<N>
// handle for $!, jobs -p, and wait.
func TestNoJobCarrierKeepsSyntheticHandles(t *testing.T) {
	t.Parallel()
	out := runCarrierScript(t, nil, `
{ :; } &
p=$!
jp=$(jobs -p)
[ "$jp" = "$p" ] && echo jobs-ok
wait "$p"
echo "st=$? pid=$p"
`)
	if got := strings.TrimSpace(out); got != "jobs-ok\nst=0 pid=g1" {
		t.Fatalf("unexpected output:\n%s", out)
	}
}
