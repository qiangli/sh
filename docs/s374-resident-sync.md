# Resident synchronization design (Story #1510)

Before implementation: extend the resident-channel ownership proof to imported
synchronization values. Certify a type only when all reachable storage stays in
interpreted code. Imported variables/results, native arguments/receivers, indirect
calls, interface storage, unsafe and escaping callbacks invalidate ownership.
Covered direct sync methods are operations on that storage, not escapes. A type
certificate must survive inferred declarations, pointers and embedded fields.
Uncertified allocations retain the existing worker path; never migrate live locks.

Prototype scope is sync.Mutex and sync.WaitGroup (Add, Done, Wait), with real Go
values held in host-only bridge metadata. Existing interpreter cells and aggregate
metadata carry identity. Dispatch recognizes that metadata before worker transport.
Blocking operations must release the task launch handshake and honor cancellation.
No host-only value may serialize into a dependency request. Copies before first
use need independent zero state; copying synchronization state after use remains
subject to Go's API restrictions. Method values and unsupported operations must
conservatively disable the certificate.

RWMutex follows the same allocation/dispatch model. Once.Do and WaitGroup.Go need
interpreted callback execution and panic/unwind integration; atomic values need
explicit typed scalar codecs (Value and Pointer additionally need provenance).
These are future extensions, not permission to route arbitrary sync symbols here.

Validation: write a trace-based failing test first, then differential small Go
programs including embedded fields, pointer aliases, goroutines and escape controls.
Measure warmed interpreted Lock/Unlock pairs with certificates enabled/cleared.
Run the unchanged SDK ken/chan.go remotely under its existing 60-second bound,
before and after. Keep all heavy execution on novidesign.local, one capped process
at a time. Record limitations and exact results, including failures.

## Implemented prototype and limits

`BashPPNamedType.LocalSync` carries a canonical type certificate minted from
`go/types` (including the existing synthetic checked-type bindings). The initial
proof is package-wide, more conservative than the channel type-group proof.
One escape or unsupported operation declines every sync certificate. The runtime
also authenticates the source import alias before allocating. Zero construction,
`new`, pointer aliases, local pointer parameters, named fields and promoted methods
on embedded fields work. Task capture and descriptor snapshots preserve the
host-only identity. A last transport guard refuses nested resident state.

Supported operations: Mutex Lock, Unlock, TryLock; WaitGroup Add, Done, Wait.
Lock tries the real Go mutex and parks on a generation notification when busy.
Unlock calls the real Go mutex and wakes contenders. WaitGroup Add updates the
real Go WaitGroup under the bookkeeping gate; zero completion wakes Wait, which
checks reuse and calls the real Wait before returning. These notification waits
release the task launch handshake and select on task cancellation. They do not
spawn helper goroutines that could outlive cancellation. Invalid Unlock is
reported as an interpreter error instead of invoking a fatal throw in the host.
Exact fatal diagnostics for synchronization misuse are outside this prototype.

Value copies (including aggregates, value parameters and receivers), comparisons,
method values/expressions, indirect calls, interface storage, unsafe, callbacks
passed to native functions and unsupported sync methods retain today's worker
path. Copy support was deliberately deferred after inspecting the existing
identity-preserving native descriptor assignment path. This is a conservative
prototype, not a claim that all nonescaping sync values are optimized. RWMutex,
Once and atomic value methods are not implemented. Existing integer atomic
function interception is unchanged. No fixture or interpreted program body was
rewritten, compiled as a fallback, or sent to native execution.

## Validation and measurements

Go 1.27.1, novidesign.local, Darwin arm64, Apple M4 Max. Only individual narrow
tests ran on the operator host. Remote commands were serialized under Python
`subprocess.run` time caps. The design and trace regression were written first;
the first executing regression failed with one native request each for Mutex
and WaitGroup type/new plus Lock, Unlock, Add, Done and Wait.

Final focused gate (11.796 s):

    go test -tags full -count=1 -timeout=90s -run '^TestS374ResidentSync' -v ./interp/

Seven differential cases run `go run` on their original source and compare both
output streams: embedded fields, pointer aliases/defer/reuse, literals, reflection
escape, negative Add, contended Lock and Wait in a launched task. Additional tests
cover 15 certificate controls, zero sync worker requests, cancellation of both
blocking methods and nested transport refusal.

Final warmed 2,000-pair benchmark (one pair = Lock + Unlock):

    go test -tags full -run '^$' -bench '^BenchmarkS374ResidentSync$' -benchtime=2000x -count=1 ./interp/

| Domain | ns/pair | pairs/s |
| --- | ---: | ---: |
| Native (certificate cleared) | 103,963 | 9,619 |
| Resident | 8,762 | 114,129 |

11.86x throughput in this single sample. The earlier sample was 134,105 vs
6,660 ns/pair and 7,457 vs 150,142 pairs/s; host variability is substantial.
Both paths interpret exactly the same loop; clocks surround only the warmed
loop, excluding parse/build/startup. Neither sample is a confidence interval.

Original ken/chan.go remains blocked: before 60.000949666 s; after
60.000981958 s; final profiled attempt 60.000794875 s. These are elapsed times
at the existing deadline, **not successful completion times**. See
`s374-resident-sync-BLOCKERS.md` for the trace and stopping diagnosis.

The final focused concurrency check also passed remotely (package 2.382 s):

    go test -race -tags full -count=1 -timeout=60s -run '^TestS374ResidentSyncNoRequests$' -v ./interp/

The outer cap for this race build/test was 240 s. The test itself took 0.81 s
and made zero native sync requests. The baseline-confirmed Bash comparison
failures are recorded in the blocker note, not counted as a passing gate.
