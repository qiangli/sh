// Copyright (c) 2026, the bashy authors
// See LICENSE for licensing information

//go:build unix

package interp_test

import (
	"bufio"
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"mvdan.cc/sh/v3/interp"
)

// killBinPrelude locates the real kill binary, bypassing the builtin, so
// scripts can exercise the "an external process signals the job" path.
const killBinPrelude = `K=/bin/kill; [ -x "$K" ] || K=/usr/bin/kill` + "\n"

// waitPidsGone polls until none of the given PIDs exist anymore,
// failing the test if any survives the deadline. Guards against leaked
// carrier processes.
func waitPidsGone(t *testing.T, pids []int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for _, pid := range pids {
		for pidLive(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("carrier pid %d still alive; leaked?", pid)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// TestJobCarrierExternalKill0 checks that an external `kill -0 $!` sees
// a live kernel PID for a job with no external process of its own, and
// that the PID is gone once the job has been waited for.
func TestJobCarrierExternalKill0(t *testing.T) {
	t.Parallel()
	c := new(testCarrier)
	out := runCarrierScript(t, c, killBinPrelude+`
{ sleep 10; } &
p=$!
"$K" -0 "$p" && echo alive
"$K" -TERM "$p"
wait "$p"
echo "term=$?"
`)
	if got := strings.TrimSpace(out); got != "alive\nterm=143" {
		t.Fatalf("unexpected output:\n%s", out)
	}
	// The job carried an external `sleep 10`; Run finishing within the
	// 5s test timeout proves carrier death canceled that child too.
	waitPidsGone(t, c.startedPids())
}

// TestJobCarrierExternalChildPublishesPID checks that a simple external
// command displaces the carrier seed in $!. wait/kill still resolve the PID
// through the job registry so signal status and child cleanup are preserved.
func TestJobCarrierExternalChildPublishesPID(t *testing.T) {
	t.Parallel()
	c := new(testCarrier)
	dir := t.TempDir()
	out := runCarrierScript(t, c, `
sh -c 'echo $$ > child.pid; exec sleep 30' &
p=$!
while [ ! -s child.pid ]; do :; done
echo "p=$p"
kill "$p"
wait "$p"
echo "st=$?"
`, interp.Dir(dir))
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "p=") || lines[1] != "st=143" {
		t.Fatalf("unexpected output:\n%s", out)
	}
	pid, err := strconv.Atoi(strings.TrimPrefix(lines[0], "p="))
	if err != nil {
		t.Fatalf("invalid child PID in %q: %v", lines[0], err)
	}
	childBytes, err := os.ReadFile(filepath.Join(dir, "child.pid"))
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(childBytes)))
	if err != nil {
		t.Fatalf("invalid child pid %q: %v", childBytes, err)
	}
	if childPID != pid {
		t.Fatalf("$! = %d, want external child PID %d", pid, childPID)
	}
	if pidLive(childPID) {
		t.Fatalf("external child PID %d survived carrier signal + wait", childPID)
	}
	waitPidsGone(t, c.startedPids())
}

// TestJobCarrierExternalTermAndKill checks the 128+signal status mapping
// for pure-builtin compound jobs killed externally: TERM -> 143 and
// KILL (uncatchable) -> 137.
func TestJobCarrierExternalTermAndKill(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, sig, want string
	}{
		{"Term", "TERM", "143"},
		{"Kill", "KILL", "137"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := new(testCarrier)
			out := runCarrierScript(t, c, killBinPrelude+`
{ while :; do :; done; } &
p=$!
"$K" -`+tc.sig+` "$p"
wait "$p"
echo "st=$?"
`)
			if got := strings.TrimSpace(out); got != "st="+tc.want {
				t.Fatalf("unexpected output:\n%s", out)
			}
			waitPidsGone(t, c.startedPids())
		})
	}
}

// TestJobCarrierTrappedTermRunsTrap checks that an external TERM aimed
// at the job's carrier PID is relayed into the job's trap machinery: the
// async list's own `trap ... TERM` action runs and controls the output
// and exit status, instead of the job dying as 143. The ready file makes
// the external kill deterministic — it only fires once the trap is
// installed.
func TestJobCarrierTrappedTermRunsTrap(t *testing.T) {
	t.Parallel()
	c := new(testCarrier)
	out := runCarrierScript(t, c, killBinPrelude+`
{ trap "echo trapped; exit 23" TERM; : >ready; while :; do :; done; } &
p=$!
while [ ! -e ready ]; do :; done
"$K" -TERM "$p"
wait "$p"
echo "st=$?"
`, interp.Dir(t.TempDir()))
	if got := strings.TrimSpace(out); got != "trapped\nst=23" {
		t.Fatalf("unexpected output:\n%s", out)
	}
	waitPidsGone(t, c.startedPids())
}

// TestJobCarrierIgnoredTermStaysAlive checks that an external TERM
// relayed to a job that ignores it (`trap ” TERM`) leaves the job
// running: it still completes its own work and exits with its own
// status. The kill-0 loop waits until the carrier is reaped, so the
// relay decision has been made before the job is released.
//
// Deliberately not parallel: `trap ” TERM` sets a process-wide real
// SIG_IGN (so exec'd children inherit it, as bash's children do), which
// a concurrently-started carrier in another test would inherit — its
// external `kill -TERM` would then do nothing. Sequential tests never
// overlap the parallel ones, and the deferred restore undoes the
// SIG_IGN before they run. signal.Reset alone is not enough: the Go
// runtime leaves the kernel disposition at SIG_IGN when resetting an
// Ignored signal, which would make later-started carriers inherit
// SIG_IGN (external TERM tests hang) and later-constructed runners see
// TERM as hard-ignored at startup. A Notify+Stop round-trip forces the
// runtime's own handler back in, so children inherit SIG_DFL again.
func TestJobCarrierIgnoredTermStaysAlive(t *testing.T) {
	defer func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGTERM)
		signal.Stop(ch)
	}()
	c := new(testCarrier)
	out := runCarrierScript(t, c, killBinPrelude+`
{ trap '' TERM; : >ready; until [ -e go ]; do :; done; echo alive; exit 5; } &
p=$!
while [ ! -e ready ]; do :; done
"$K" -TERM "$p"
while "$K" -0 "$p" 2>/dev/null; do :; done
: >go
wait "$p"
echo "st=$?"
`, interp.Dir(t.TempDir()))
	if got := strings.TrimSpace(out); got != "alive\nst=5" {
		t.Fatalf("unexpected output:\n%s", out)
	}
	waitPidsGone(t, c.startedPids())
}

// TestJobCarrierUncatchableKill checks that SIGKILL terminates the job
// as 137 even when the script recorded a trap for it: KILL is
// uncatchable, so the relay must never route it into the trap
// machinery.
func TestJobCarrierUncatchableKill(t *testing.T) {
	t.Parallel()
	c := new(testCarrier)
	out := runCarrierScript(t, c, killBinPrelude+`
{ trap "echo nope" KILL; : >ready; while :; do :; done; } &
p=$!
while [ ! -e ready ]; do :; done
"$K" -KILL "$p"
wait "$p"
echo "st=$?"
`, interp.Dir(t.TempDir()))
	if got := strings.TrimSpace(out); got != "st=137" {
		t.Fatalf("unexpected output:\n%s", out)
	}
	waitPidsGone(t, c.startedPids())
}

// TestJobCarrierConcurrentJobs starts several concurrent pure-builtin
// jobs, checks their carrier PIDs are unique real processes, then kills
// them all externally at once and checks every wait reports 143. Also
// serves as a race check for concurrent relay/teardown (run under -race
// in CI).
func TestJobCarrierConcurrentJobs(t *testing.T) {
	t.Parallel()
	c := new(testCarrier)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r, err := interp.New(interp.WithJobCarrier(c), interp.StdIO(nil, pw, pw))
	if err != nil {
		t.Fatal(err)
	}
	file := parse(t, nil, `
{ while :; do :; done; } & a=$!
{ while :; do :; done; } & b=$!
{ while :; do :; done; } & c=$!
{ while :; do :; done; } & d=$!
echo "$a $b $c $d"
wait "$a"; echo "$?"
wait "$b"; echo "$?"
wait "$c"; echo "$?"
wait "$d"; echo "$?"
`)
	ctx, cancel := context.WithTimeout(context.Background(), runnerRunTimeout)
	defer cancel()
	runErr := make(chan error, 1)
	go func() {
		defer pw.Close()
		runErr <- r.Run(ctx, file)
	}()
	br := bufio.NewReader(pr)
	first, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("reading pids: %v", err)
	}
	pids := strings.Fields(first)
	if len(pids) != 4 {
		t.Fatalf("want 4 pids, got %q", first)
	}
	seen := make(map[string]bool)
	var wg sync.WaitGroup
	for _, pid := range pids {
		if !numericPidRe.MatchString(pid) {
			t.Fatalf("$! = %q, want a real numeric PID", pid)
		}
		if seen[pid] {
			t.Fatalf("duplicate concurrent $! %s", pid)
		}
		seen[pid] = true
		n, _ := strconv.Atoi(pid)
		if !pidLive(n) {
			t.Fatalf("pid %d not alive while its job runs", n)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = syscall.Kill(n, syscall.SIGTERM)
		}()
	}
	wg.Wait()
	for i := range 4 {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("reading status %d: %v", i, err)
		}
		if got := strings.TrimSpace(line); got != "143" {
			t.Fatalf("wait #%d = %q, want 143", i, got)
		}
	}
	if err := <-runErr; err != nil {
		t.Fatalf("Run: %v", err)
	}
	waitPidsGone(t, c.startedPids())
}

// TestJobCarrierCancelReapsCarrier checks that canceling the runner's
// context tears the whole job down: the goroutine job, its external
// child, and the carrier process — no leaks.
func TestJobCarrierCancelReapsCarrier(t *testing.T) {
	t.Parallel()
	c := new(testCarrier)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	r, err := interp.New(interp.WithJobCarrier(c), interp.StdIO(nil, pw, pw))
	if err != nil {
		t.Fatal(err)
	}
	file := parse(t, nil, `{ sleep 30; } & echo "$!"; wait "$!"; echo after`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		defer pw.Close()
		r.Run(ctx, file) // error (if any) is irrelevant; we cancel it
	}()
	br := bufio.NewReader(pr)
	line, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("reading pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("parsing pid %q: %v", line, err)
	}
	if !pidLive(pid) {
		t.Fatalf("carrier pid %d not alive while its job runs", pid)
	}
	cancel()
	select {
	case <-runDone:
	case <-time.After(runnerRunTimeout):
		t.Fatal("Run did not return after context cancellation")
	}
	waitPidsGone(t, c.startedPids())
}
