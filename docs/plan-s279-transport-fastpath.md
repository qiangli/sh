# Sprint 279: imported-call transport and receiver fast paths (run 59)

Story #757 (`609c89bfa598`). Accepted starting sh: `c2afc0d8b`.
This is a bounded, verified partial for independent review, not G2 acceptance.

## Decision and ownership

Eliminate redundant allocations across the native call hotpath and transport
validation without altering protocol wire representations, channel ownership,
callback routing, or semantics.

1. **Method name extraction and stack introspection (`interp/bashpp_sprint165_frames_callers.go`, `interp/bashpp_sprint162_nilptr2_stack.go`)**:
   `goSourceFrameFuncReceiver` and `goSourceFramesReceiver` inspect calls to see
   if they match runtime stack/frame receivers (`runtime.Func` methods `Name`/`Entry`/`FileLine`
   or `runtime.Frames` method `Next`). Previously, both unconditionally called
   `goSourceMethodCallReceiver`, which synthesized new `syntax.BashPPIdent` AST nodes
   for every multi-part function call (such as `strings.HasPrefix`). `goSourceCallMethodName`
   now extracts the method name directly without AST allocation; receiver AST synthesis
   is bypassed whenever the method name does not match.

2. **Shared ordering and heap fast paths (`interp/bashpp_native_shared_order.go`, `interp/bashpp_native_shared_heap.go`)**:
   `goSourceSharedOrdering` and `goSourceSharedHeap` are checked on every native request.
   Both previously called `nativeSliceCallable` unconditionally, allocating strings
   for every imported call. Furthermore, `goSourceSharedHeap` allocated a heap `map[string]int`
   literal per call. Both now check the imported package identity (`sort` or `container/heap`)
   before computing `nativeSliceCallable`, and `goSourceSharedHeap` uses a stack switch.

3. **Generic slice helper fast path (`interp/bashpp_native_slices.go`)**:
   `nativeSliceGenericHelper` handles `slices.*`, `maps.*`, and `cmp.*`. It now fast-paths
   calls whose imported package is not in that set, avoiding two string allocations per
   arbitrary imported call.

4. **Transport validation and read-only detection (`interp/bashpp_native_transport.go`)**:
   In `validateLocalTransport`, safe callback-free requests with no pointer arguments
   return early once argument inspection confirms `!unsafe && !functionCallbacks && !requestHasCallbacks`,
   avoiding redundant callable string formatting and scans.
   In `nativePointerReadOnlyRequest`, receiver calls skip `nativeSliceCallable` entirely
   (as receiver methods are not package functions), and non-receiver calls check the
   import package before string formatting.

## Paired benchmark evidence

Measured serially with authenticated Go 1.27.1, the exact weave `run-59` GOCACHE,
`GOFLAGS=-p=1`, and `GOMAXPROCS=2`:

```sh
GOFLAGS=-p=1 GOMAXPROCS=2 go test -tags full ./interp -run '^$' \
  -bench '^BenchmarkGoSourceNativeCallHotpath$' -benchtime=1x -count=10 -benchmem
```

- **Base (`c2afc0d8b`)**:
  - Wall-clock median: 1,123,745,500 ns/op
  - Memory median: 58,308,344 B/op
  - Allocation median: 597,068 allocs/op
- **Candidate**:
  - Wall-clock median: 1,143,304,375 ns/op
  - Memory median: 56,448,964 B/op (**-3.19%**, -1,859,380 B/op)
  - Allocation median: 507,014 allocs/op (**-15.08%**, -90,054 allocs/op, ~9 allocations saved per call)

## Verification and gates

- Unit and behavioral test suite: `TestBashPPS279NativePointerReadOnlyFastPath`,
  `TestBashPPS279SharedOrderingAndHeap`, `TestBashPPS279CallMethodNameExtraction`,
  and `TestBashPPS279StackFramesIteration` pass under `-race -count=3`.
- Full regression suite under `-tags full -race -count=3`: all S279 tests
  (`TestBashPPS279WorkerCallArgs`, `TestBashPPS279SessionEncoder*`, `TestBashPPS279BasicLiteralBridging`,
  `TestBashPPS279StringComparisonSemantics`, `TestBashPPS279VerifiedPlanFastPath`) and prior
  `TestGoSourceS270G1i2SortFuncPointerElements` pass cleanly.
- `gofmt -d` and `git diff --check` are clean.
