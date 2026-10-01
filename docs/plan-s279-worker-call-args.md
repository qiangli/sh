# Sprint 279: bounded worker argument scratch (run 58)

Story #757 (`609c89bfa598`). Accepted starting sh: `43f762738`.
This is a bounded, unverified partial for independent review, not G2 acceptance.

## Decision and ownership

Continue the run-56 direction without its `any`/slice type error. Reuse only
reflection argument carriers inside the generated worker's dispatch frame.
Keep native/interpreter channel domains, callback routing, bridge scalar
representations, request validation, and transport exchanges unchanged.

A `sync.Pool` lends a pointer to 16 reflection slots to each active small call.
The local argument variable is a typed `[]reflect.Value`. Zero-argument calls
need no storage. Calls wider than 16 arguments allocate an exact-size slice
and never put it in the pool; retained capacity per pooled object is bounded.
Pool misses and garbage collection are safe. Concurrent and recursive dispatch
borrow separate objects. Pooling pointers avoids allocating a boxed slice
header on every return.

The cleanup defer captures the borrowed owner, not the mutable `args` variable:
`originalFmtArgs` can replace that slice. Cleanup runs after result encoding,
callback-frame removal, and deferred pointer/slice writeback, including panic
unwinding and partial decode errors. It clears every reflection slot before
returning the object. Clearing a carrier does not mutate a callee-retained Go
value or its backing storage. No reply channels or mutable request state are
pooled. This reduces one possible worker allocation per small call; it does
not reduce round trips. Neither overall speedup nor a sub-60-second projection
has been measured.

## Tests and independent gate

`TestBashPPS279WorkerCallArgs` generates the actual dependency worker and runs
its tests under `-race -count=3`. Coverage includes zero/small/wide arities,
repeated mixed types, ordinary/spread variadics, `%T` formatting projection,
partial decode errors, panic unwinding, retained arguments, concurrent nested
dispatch, deferred slice writeback, exclusive scratch ownership, clearing, and
oversized allocation exclusion. The child explicitly inherits the caller's
cache and resource flags. This is a worker dispatch gate, not a routed socket
callback or corpus gate; retain the existing callback regressions too.

Attempted in this workspace with Go 1.27.1:

```sh
GOFLAGS=-p=1 GOMAXPROCS=2 go test -tags full ./interp \
  -run '^TestBashPPS279WorkerCallArgs$' -count=1 -timeout=3m
```

**BLOCKED before compilation**: initializing the prescribed weave `run-58`
GOCACHE failed at `mkdir .../run-58/00: operation not permitted`.
The initial language-version smoke gate failed for the same reason. No cache
substitution, build, test PASS, race PASS, or benchmark measurement is claimed.
The worker template parses through `gofmt`; new Go/fixture formatting and
`git diff --check` are clean. These static checks do not prove compilation.

The conductor must run the focused gate above using the exact managed cache,
then the relevant prior S270 ordering, encoder/cancellation, and numeric gates.
For paired measurements, run serially with the same authenticated SDK and cache:

```sh
GOFLAGS=-p=1 GOMAXPROCS=2 go test -tags full ./interp -run '^$' \
  -bench '^BenchmarkBashPPS279WorkerDispatch$' -benchtime=10000x -count=10 -benchmem
GOFLAGS=-p=1 GOMAXPROCS=2 go test -tags full ./interp -run '^$' \
  -bench '^BenchmarkGoSourceNativeCallHotpath$' -benchtime=1x -count=10 -benchmem
```

Run on both the accepted base and candidate. The new benchmark adapter and
common worker fixture can be overlaid on the accepted base without production
changes: pool-only tests are a separate fixture excluded from benchmark builds.
The dispatch benchmark reports child-worker ns/op, B/op, and allocs/op, excludes
compilation, and rejects execution failures. The existing hotpath benchmark
measures the interpreter/transport path; its parent-process allocation counts
do not measure the worker's allocations. Keep both raw samples and medians.

No short-suite or Linux leaf run was attempted. The frozen G2 verdict remains
12/35 PASS. Review, paired performance evidence, and the later frozen-candidate
Linux gate remain outstanding; do not close the story from this partial.
