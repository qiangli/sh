# Story 1533: handle leases; origin and closure ownership blocked

Branch: `s374-ownership`.

This is a **partial delivery**, stopped after three rejected origin-ownership
candidates. Native handle reclamation is implemented. Pointer-origin and
string-backed closure reclamation are not. No original program has a native
fallback and no corpus fixture was edited. The three corpus measurements below
use explicitly reduced scratch copies, not original-size conformance runs.

## Base and prerequisites

The requested integration clone resolved to `eacf71b02da8bf17a964105ba8f5b5519f9e94b6`.
It did not contain story 1528's three contained fixes. Those existing commits
were cherry-picked before the final measurements: `279d23e2` -> `7b8fdfda`
(host GC), `2dab3f9e` -> `c34260fa` (closure captures), and `a900985a` ->
`6a028a3c` (optional handle-type cache). Both final measurement binaries include
these prerequisites; only `committed` includes the new lease mechanism.

The supplied `by-id.tsv` says rangegen is FIXED and issue15277/issue39541 are
STILL (killed/timeout). The raw tar was extracted under
`docs/s374-ownership-evidence/evidence/` and inspected, but its 357 MB of existing
evidence is locally ignored rather than duplicated in this commit. A corpus
FIXED status does not establish bounded memory in a persistent runner.

## Failing-first and ownership checks

All tests executed on Linux amd64 with Go 1.27.1. No local Go test or memory
workload ran. Test output is retained under `s374-ownership-evidence/linux/`.

- Before the mechanism, `TestS374OwnershipUnsentHandles` retained **256/256**
  discarded encodings and failed. Afterward both **256 and 512** batches leave
  **0** worker handles.
- `TestS374OwnershipRoundTrips`: after successive **256 and 512** round-trip
  batches, host owners = **1**, worker values/leases/pointer-index = **1/1/1**,
  reflected metadata = **0**. After dropping the last alias all are **0**.
  This also checks an independently exported alias, copy ownership, host type
  facts, native retention after bridge release, and a delayed duplicate release.
- `TestS374OwnershipCallbackAcknowledgement` proves a callback result stays
  owned before acknowledgement and releases afterward.
- The first cleanup implementation captured the session. A failing-first cycle
  probe showed that a cached response then rooted its dead session. The final
  implementation queues into independent state; `TestS374OwnershipSessionCycleCollects`
  passes. This avoids replacing a handle leak with a session leak.

See [design](s374-ownership.md) for the wire protocol, callback acknowledgement,
GC timing and ownership limits. Leases are drained on the next real request;
there is no arbitrary eviction of native values.

## novidesign.local measurements

Darwin arm64, Go 1.27.1, sequential execution under the supplied process-tree
watchdog: **4,000,000,000 bytes / 120 seconds**, sampled every 50 ms. Every run
completed below the cap. Profiling forces worker GC once per second and host GC
at marked checkpoints, so these are diagnostic runs, not uninstrumented RSS
benchmarks. Peak RSS is KiB; host and process-tree maxima may occur separately.

| Reduced workload | Before host/tree peak KiB | After host/tree peak KiB | Before/after seconds |
| --- | ---: | ---: | ---: |
| issue15277, 16,384-byte buffer | 55,696 / 161,632 | 54,704 / 147,040 | 1.476 / 1.464 |
| rangegen, depth 1 repeated 8 times | 106,800 / 180,640 | 110,048 / 164,496 | 3.391 / 3.365 |
| issue39541, 1 goroutine, 100,000 calls | 171,920 / 212,576 | 181,056 / 196,720 | 15.000 / 16.437 |

issue39541's worker samples grow **13,212 -> 197,254 handles** before (heap
**1,412,352 -> 15,238,248 bytes**). Afterward samples range **177–7,986 handles**,
ending at **6,374** (heap **1,226,344 -> 2,145,984 bytes**). Sampling during active
calls is not an exact live-owner count; the round-trip unit test gives that
quiescent assertion. Host checkpoints at 50,000/100,000 calls are
**19,693,312 / 22,756,528 bytes** before and **20,749,992 / 23,579,832 bytes** after.
The result establishes helper reclamation, not a reduction of every heap metric.

rangegen still has **8 -> 16 origins** and **784 -> 1,568 closures** at the two
checkpoints. Before/after final checkpoint heaps are **16,789,912 / 16,808,792
bytes**. The 959,080-byte generated outputs are identical (SHA-256
`11d155ee1f04222b9996945cb39fb2ffb3d4f67beaf2df4dfce192f58b14d1ac`).

issue15277 still has **2 -> 3 origins** at its drop checkpoints and prints the
failed drop-threshold diagnostic. Exit 0 is not treated as a semantic PASS. Its
literal MemStats measures the worker, while `HOST` lines measure the interpreter.
No claim is made that the original 10 MB program now frees its buffers.

Exact measurement commands, from `~/s374/ownership`:

```sh
export PATH=$HOME/sdk/go1.27.1/bin:$PATH GOTOOLCHAIN=local OWNERSHIP_PROFILE=1
python3 guard.py baseline-15277 ./probe-baseline variants/15277-16384.go
python3 guard.py baseline-rangegen ./probe-baseline finalcontrols/rangegen-repeat-8.go
python3 guard.py baseline-39541 ./probe-baseline finalcontrols/39541-100000.go
python3 guard.py committed-15277 ./probe-committed variants/15277-16384.go
python3 guard.py committed-rangegen ./probe-committed finalcontrols/rangegen-repeat-8.go
python3 guard.py committed-39541 ./probe-committed finalcontrols/39541-100000.go
```

`tools/instrument.py` creates a scratch `cmd/ownershipprobe` and profiling hooks
in scratch clones; none of these hooks is in the production interpreter files.
Build commands are `go build -tags full -o ../probe-baseline ./cmd/ownershipprobe`
and `go build -tags full -o ../probe-committed ./cmd/ownershipprobe`, each through
`python3 ../guard.py ../build-LABEL` inside its respective scratch clone.
Raw measurements, watchdog, instrumenter and reduced source inputs are included.
Stored source inputs end in `.go.txt` so they do not become repository packages;
remove the `.txt` suffix when copying them into remote scratch workload directories.
Earlier `before/after/final/released/delivery` log labels record development stages;
**baseline/committed** are the final matched comparison above.

## BLOCKERS: three rejected origin candidates

The executable probes are preserved as `tools/origin_candidates.go.txt`; each
was temporarily installed as a test and run alone under `timeout 120` on Linux,
then removed. They are diagnostic failures, not skipped acceptance tests.

1. **Cleanup on the owning cell:** 32 dead transported cells leave **32 origins,
   32 index entries, 0 cleanups**. Both tables root the cell, so adding a cleanup
   cannot break the retention it is intended to observe.
2. **Weak pointer wrapper:** the registered wrapper dies while a distinct
   pointer wrapper still owns the same cell. Wrapper reachability is not storage
   reachability, so this candidate loses live identity.
3. **Call-scoped origin removal:** a generated worker retains the decoded
   pointer in a native owner, removes the origin at return, and redecodes the
   same origin. The pointers differ; the probe fails with "call-scope release
   split identity from a native retained pointer".

A stronger distributed epoch scheme would have to drop *all* bridge-only roots
(including original slice storage and reflected metadata), establish native
reachability, and hand off interpreter roots while excluding concurrent imports,
callbacks, interior aliases and blocked native calls. The current bridge has no
such safepoint/retention graph. This is a design requirement, not a justification
for weak references or eviction. The original closure registry additionally
needs value ownership that survives string copies in aggregates, active frames,
deferred calls and task snapshots. Neither remaining BUG is reclassified COST.

## Linux final gates

From `/srv/dev/ownership/sh`, with
`PATH=/srv/s374-r1/authenticated-sdk/bin:$PATH`,
`GOROOT=/srv/s374-r1/authenticated-sdk`, `GOTOOLCHAIN=local`, `CGO_ENABLED=0`.
Each command ran alone, in the order shown, on the final production source.

| Exact command | Result / package duration |
| --- | --- |
| `timeout 300 go test -tags full -count=1 -v -run '^TestS374Ownership' ./interp/` | PASS, 1.725 s |
| `timeout 300 go test -tags full -count=1 -v -run TestS374ResidentDifferential ./interp/` | PASS, 10.412 s |
| `timeout 300 go test -tags full -count=1 -v -run S374 ./interp/` | PASS, 47.832 s |
| `timeout 300 go test -tags full -count=1 -v -run S281 ./interp/` | FAIL, 126.792 s; one baseline failure below |
| `timeout 300 go test -tags full -count=1 -v -run Sprint165 ./interp/` | PASS, 33.758 s |
| `timeout 300 go test -tags full -count=1 -v -run TransportOrigin ./interp/` | PASS, 2.241 s |

The complete gate is **not green**. The sole final S281 failure is
`TestGoSourceS281SelfReexecReplacementToolIsolation`: its child reports import
`fmt`: `go list: exit status 1`. Running
`timeout 120 go test -tags full -count=1 -v -run '^TestGoSourceS281SelfReexecReplacementToolIsolation$' ./interp/`
on the unchanged prerequisite baseline reproduces the same failure (1.430 s).
All other S281 tests pass in the final run. An earlier run with CGO enabled also
failed the `net` import test; CGO_ENABLED=0 resolves that environment mismatch.
No unrelated reexec or cgo behavior was changed to turn the gate green.

The failing-first command for unsent handles was
`timeout 120 go test -tags full -count=1 -v -run '^TestS374OwnershipUnsentHandles$' ./interp/`
(FAIL, 0.630 s before). The cleanup-cycle probe failed in 0.171 s before its
correction and is included in the final passing ownership pattern. Candidate
commands use the same prefix with `^TestOwnershipCandidateCellCleanup$`,
`^TestOwnershipCandidateWeakWrapper$`, and `^TestOwnershipCandidateCallScope$`;
they fail as documented in 0.028, 0.027 and 0.540 s respectively.

A final mutable-pointer-field regression also failed first: after reclamation,
worker values were **0** but the pointer index still had **1** entry. The worker
now records the original index key per handle, removes that exact key when the
handle dies, and validates indexed hits against a mutable field's current
pointer. The test also checks that a still-live native alias is not redirected
through a changed field. This closes a stale-index/address-reuse hole rather
than changing native field ownership.

## Linux amd64 corpus measurements

The same reduced inputs and profiler ran sequentially in `/srv/dev/ownership`,
with the same Go/CGO environment as the gates. Both binaries were built and each
run executed through `timeout 130 python3 guard.py ...`; the inner watchdog still
limits the process tree to **4,000,000,000 bytes / 120 seconds**. All six runs
completed below the cap. `linux-measured/` contains the raw output and numbers.

| Workload | Before host/tree peak KiB | After host/tree peak KiB | Before/after seconds |
| --- | ---: | ---: | ---: |
| issue15277, 16,384 bytes | 52,192 / 154,380 | 52,668 / 156,800 | 1.188 / 0.974 |
| rangegen, depth 1 x 8 | 110,608 / 158,976 | 108,104 / 163,280 | 7.058 / 4.906 |
| issue39541, 100,000 calls | 170,532 / 203,636 | 178,432 / 190,204 | 32.921 / 24.660 |

issue39541's worker grows **5,801 -> 194,428 handles** before (heap
**784,864 -> 14,811,448 bytes**). Afterward samples range **392–6,464**, ending at
**6,133** handles (heap **679,664 -> 1,761,392 bytes**). Host midpoint/end heaps
are **19,458,176 / 22,567,104 bytes** before and **20,561,592 / 23,851,472 bytes**
after. This is helper reclamation with some interpreter owner overhead, not a
claim that total live interpreter memory fell.

rangegen still grows **8 -> 16 origins** and **784 -> 1,568 closures**. Its final
checkpoint heap is **16,572,936 bytes** before and **16,567,680 bytes** after;
the output SHA-256 is the same as the novidesign pair. issue15277 still grows
**2 -> 3 origins** and prints its failed drop-threshold diagnostic. These are
remaining defects on the required Linux platform, not Darwin-only conclusions.

Exact commands after building the two instrumented scratch clones:

```sh
timeout 130 python3 guard.py linux-baseline-15277 ./probe-baseline workloads/15277-16384.go
timeout 130 python3 guard.py linux-baseline-rangegen ./probe-baseline workloads/rangegen-repeat-8.go
timeout 130 python3 guard.py linux-baseline-39541 ./probe-baseline workloads/39541-100000.go
timeout 130 python3 guard.py linux-committed-15277 ./probe-committed workloads/15277-16384.go
timeout 130 python3 guard.py linux-committed-rangegen ./probe-committed workloads/rangegen-repeat-8.go
timeout 130 python3 guard.py linux-committed-39541 ./probe-committed workloads/39541-100000.go
```

Builds, in `measure-baseline` and `measure-delivery` respectively, were
`timeout 130 python3 ../guard.py ../linux-build-baseline go build -tags full -o ../probe-baseline ./cmd/ownershipprobe`
and
`timeout 130 python3 ../guard.py ../linux-build-committed go build -tags full -o ../probe-committed ./cmd/ownershipprobe`.
Execution additionally sets `OWNERSHIP_PROFILE=1`. The copied bashsharp clone is
at `/srv/dev/ownership/bashsharp`; the measurement entry point calls
`gosource.Parse(... RunMain:true)` and `Runner.Run` directly and never compiles
an original program function.
