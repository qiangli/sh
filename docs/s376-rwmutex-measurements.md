# Sprint 376 follow-on: resident RWMutex + scaled bridge/eval/scheduler split

Story: #1549. General mechanism only: the resident-sync certificate model
(`sh/gosource/sync_domain.go`, `sh/interp/gosource_resident_sync.go`) now
covers `sync.RWMutex` read/write paths (RLock/RUnlock, Lock/Unlock,
TryLock/TryRLock) under the same package-wide escape rules as
Mutex/WaitGroup. No timeout raise, no fixture edit, no per-root special
case, no native fallback. `RLocker` is deliberately unsupported (it would
smuggle lock identity across as a `Locker` interface) and declines the
package certificate, keeping the worker path.

Narrow tests: `sh/interp/gosource_s376_rwmutex_test.go` (8 differential
cases vs `go run`, misuse errors, 16 certificate controls, native/resident
benchmark) and `sh/interp/gosource_s376_rwmutex_internal_test.go`
(zero-`sync.` bridge requests, RLock/Lock cancellation, transport refusal).

## Scaled repros (Go 1.27.1, Darwin arm64 `dragon`, single samples)

Loop shapes mirror the fixtures at N=2000/4000; program-internal clocks
exclude parse/build/startup. Linux 4-vCPU projections use these dragon
figures as lower bounds (historically ~4x on bridge; eval factor unmeasured).

| Repro | N=2000 | N=4000 | Per-iter |
|---|---|---|---|
| A eval floor (issue79186 loop+map shape, no sync) | 10,417,958 ns | 20,379,416 ns | ~5.1 us, linear |
| B RWMutex loop shape, native bridge (before) | 187,438,125 ns | — | ~93.7 us |
| B RWMutex loop shape, resident (after) | 27,112,834 ns | 49,660,917 ns | ~12.4–13.6 us (~7x all-in) |
| C ken/chan poll shape (Gosched + Mutex counter) | 17–29 ms total | 35,856,500 ns | ~8.6–14.6 us (host range) |
| Bare RLock/RUnlock pair bench native vs resident | — | — | 99,515 ns vs 3,283 ns (~30x) |

Split per issue79186 iteration (~1.1 lock pairs: one Get RLock/RUnlock,
periodic Set Lock/Unlock): evaluation ~5.1 us + native bridge ~79 us/pair
vs resident sync ~3.3–7.6 us/pair + interpreted call dispatch on the
Get/Set wrappers.

## Exact-ID operator decisions (full 60 s roots NOT run here)

Heavy runs stay on a claimed non-dragon host; `dragon` is the excluded
class and the 4-vCPU Linux acceptance host is not present in this session,
so no full-root verdict is claimed from these scales.

- `testdir:fixedbugs/issue79186.go` (51.2M iters): evaluation floor alone
  is 51.2M x ~5.1 us ≈ 261 s on dragon (≈130 s even at ideal 2-way
  parallelism under the fixture's GOMAXPROCS=2), before any bridge, call,
  or scheduler cost. Resident RWMutex removes the ~79 us/pair bridge class
  (measured above) but the residual all-in rate (~12–14 us/iter ≈ 650 s)
  still exceeds the unchanged 60 s bound by an order of magnitude.
  **Return this exact ID to the operator: exclusion vs evaluation/call
  dispatch work.** Not a timeout, fixture, special-case, or
  native-fallback change.
- `testdir:ken/chan.go`: uses only Mutex/channels, both already resident;
  the RWMutex mechanism does not apply. Remaining cost is the
  scheduler/polling class (~8.6–14.6 us per Gosched+counter poll-iter on
  dragon) with the prior scheduler/condvar non-verdict unchanged (zero
  native channel/sync requests, 60 s timeout, progress-vs-stuck
  undetermined, three-attempt stop already applied). **Return this exact ID
  to the operator under the existing polling non-verdict.** No new
  mechanism is proposed for it here.

## Regression sentinels

`testdir:rangegen.go`, `testdir:fixedbugs/issue9604b.go`,
`testdir:fixedbugs/issue16249.go` contain no `sync` import, so the widened
certificate planner cannot alter their plans (verified by fixture source
inspection). Focused gates observed passing: all `TestS374ResidentSync*`,
`TestS374ResidentNoChannelRequests`, `TestS374ResidentChannel*`,
`gosource` package tests, and the new `TestS376ResidentRWMutex*` suite.
