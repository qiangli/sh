# S376 synchronization roots: nil channel comparison, wake-one, shared-read guard (Story #1549)

Measured through the faithful CLI (`bashsharp --bashpp --source=go FILE`, Go
1.27.1, dev Mac darwin/arm64, single samples, machine shared with other
work). Scaled inputs are the unchanged Go 1.27.1 fixture text with only the
named count edited. Baseline is sh `01e04676a`. The exact roots were not run
here; the manager owns the 4-vCPU Linux runs.

## 1. `test/ken/chan.go` was stuck, not slow

At one tenth of the fixture's traffic (`v%100 == 7`, `8` sends per channel)
the baseline still never finished: killed at its cap with ~0.4 s of evaluator
CPU in a 20 s profile, the rest `runtime.Gosched` wakeups from `main`'s
`wait()` poll. A goroutine dump showed four senders parked in a channel send
and no `sel` goroutine.

Cause: `r0.rc != nil` was **false for a non-nil channel** whenever the channel
was read from a struct field, a map/slice/array element, or a variable copied
from one. An interpreter-owned channel stored in an aggregate has no payload;
its identity is the element metadata, and the nil comparison looked only at
the payload. `sel` counted zero live channels, served one case and returned,
leaving its peers blocked forever while `main` polled.

Fix (`interp/bashpp_scalar.go`): a channel whose metadata carries an identity
is not nil.

| Input | Baseline | Candidate |
|---|---|---|
| ken/chan, 8 per channel | killed at 20 s and at 60 s caps | 1.5–1.8 s, exit 0 |
| ken/chan, 19 per channel | — | 1.4 s, exit 0 |
| ken/chan, 38 per channel (half the root) | — | 1.4–1.6 s, exit 0 |

Elapsed is flat in the count (startup dominates), so the full root projects
to about 2 s here. This is a wrong-answer repair; the previous
scheduler/polling non-verdict for this ID is explained by it.

## 2. Contended resident Mutex / RWMutex

`test/fixedbugs/issue79186.go` with only `iters` (and, for the split,
`goroutines`) edited. Wall seconds; per-iteration figures are the difference
between two scales, which removes ~1.5–3 s of startup.

| Shape | Baseline | Candidate |
|---|---|---|
| 256 goroutines x 500 (first sample, before/after wake-one only) | 8.63 | 3.32 |
| 256 x 2000 (same) | 29.25 (34 s sys) | 7.29 -> 5.42 with the guard change |
| 256 x 1000 / 256 x 3000 | 14.68 / 38.83 | 3.21–5.60 / 7.11–8.14 |
| contended, per iteration | ~47 us | ~5–7.6 us |
| 1 goroutine x 100k / 300k (uncontended sync) | 2.18 / 4.48 | 2.20 / 4.66 (~12–13 us/iter) |
| 1 goroutine, lock calls deleted (evaluation only) | — | 1.81–1.90 / 3.46–3.62 (~8.3–8.6 us/iter) |

Two general causes, both removed:

- **Broadcast wakeups.** Every Unlock/RUnlock closed one shared channel and
  woke every blocked goroutine to retry; with 256 goroutines and a writer
  almost always pending, each release cost hundreds of wakeups (baseline
  profile: 81% under `goSourceResidentSyncRequest` in `selectgo`/`lock2`).
  Exclusive acquirers (Mutex lockers, RWMutex writers) now queue and are woken
  one at a time; an abandoned wakeup is handed on. Readers admitted by a
  writer's Unlock are still woken together, since all of them proceed.
- **Shared-cell guard.** A variable captured by many goroutines is read
  through a per-cell guard. It was a mutex, so 256 goroutines dereferencing
  the same captured pointer queued on it and were parked and woken by the
  host scheduler (17% in `bashPPCell.view`, 40% in work-stealing sleeps). The
  guard is now a reader/writer lock; snapshots exclude writers, not each
  other.

`examples/mutexes/mutexes.bsh` (production input, 3 x 10000): 1.19 s baseline,
1.28 s candidate, same output.

## 3. Exact-ID operator decision: `testdir:fixedbugs/issue79186.go`

The root runs 256 x 200,000 = 51.2M iterations, each an interpreted method
call, a map read and a lock pair. Measured on the faithful CLI after the
repairs above:

- evaluation alone, one goroutine, no lock calls: ~8.5 us/iteration
  => 51.2M iterations = ~435 CPU-seconds;
- uncontended resident RWMutex adds ~4–5 us/iteration;
- contended all-in: ~5–7.6 us of wall per iteration => 260–390 s here.

Even with free synchronization and perfect scaling on four cores the
evaluation volume is ~109 s on this machine's cores, above the unchanged 60 s
bound, and the fixture itself asks for `GOMAXPROCS(2)`. The residual is
interpreted call/evaluation cost, not synchronization. **This exact ID returns
to the operator: exclusion as evaluation-bound, or evaluation/call-dispatch
work under its own story.** No timeout, fixture, special-case or native
fallback is proposed.

## 4. Tests

- `TestS376ChannelNilComparisonDifferential` (vs `go run`): field, value
  struct, literal, copy, map/slice/array element, pointer parameter across a
  goroutine, and the ken/chan select stage with channels set to nil as they
  finish.
- `TestS376ResidentSyncContendedDifferential`: 64-goroutine Mutex counter and
  the issue79186 shape at `iters = 40`.
- `TestS376ResidentMutexWakesOneWaiter`,
  `TestS376ResidentSyncAbandonHandsWakeOn`,
  `TestS376ResidentMutexCancelledWaiterLeavesQueue`,
  `TestS376ResidentRWMutexUnlockAdmitsReadersThenWriter`.

Passing here: the above (the internal and contended ones also under `-race`),
all `TestS374Resident*` / `TestS376Resident*`, the `Select`, `Nil` and
`Channel` name subsets, `./gosource`, and a linux/amd64 cross-build.

Failing identically on the baseline, unchanged by this work:
`TestBashPPCellReadersTakeAView` (two `bashpp_fastint.go` readers not on its
list), `TestGoSourceWaitGroupPanicKeepsTheCount`,
`TestGoSourceNativeChannelValidationBeforeCommunication`.

Seen and left alone: `x := c.ch` taken while the field is nil, then assigned a
channel, then `x != nil` reports `BASHPP-EEXPR-NIL` (an error, not a wrong
answer; same on the baseline; neither root does this).
