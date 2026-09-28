# Sprint 319 / Story 1086 — the recursive method-value re-bind in the `Parse` callback proof

Sprint: #319
Story: #1086
Story-ID: 2405313cf7e2

## The observation

R1 (frozen Linux, sh `30388a5e`, harness `b59a534`) ran interpreted
`cmd/compile/internal/types2`. Eighteen tests passed — including the
interface indexed-element case the previous types2 worker commit crossed —
and then `TestTypeSetString` failed in 0.18s:

```
panic: gosource: asynchronous or retained original function callbacks are
unsupported for __gosource_import_0_64_0.Parse [recovered, repanicked]
	main.decodeIn.func1(...)
		bashpp-session-3926046011.go:22554
```

`__gosource_import_0_64_0` is `typeset_test.go`'s own alias for
`cmd/compile/internal/syntax` (package 0, file 64 in the lowered per-file
alias numbering, import 0). The call it names is the one on
`typeset_test.go:49`:

```go
errh := func(error) {} // dummy error handler so that parsing continues
src := "package p; type T interface" + body
file, err := syntax.Parse(nil, strings.NewReader(src), errh, nil, 0)
```

That is an original function callback handed to a native package function, so
the only thing that can admit it is
`dependencyFunctionCallbackLifetimeProof` — the general source proof, not a
catalogue entry. It refused.

## The cause is an interaction, not a regression in either rule

The refusal is not environment-dependent: R1's
`cmd/compile/internal/syntax` sources are byte-identical to go1.27.1's, and
the proof reads only sources. It reproduces on the candidate commit as the
already-present end-to-end gate:

```
--- FAIL: TestS281DependencySourceCallbackProof
    Runner: gosource: asynchronous or retained original function callbacks
    are unsupported for syntax.Parse
```

With `BASHPP_CALLBACK_PROOF_DIAG` the first refusal is exact:

```
same-frame rejection: argument f identity changed across tainted state
recursive frame difference for parser.appendGroup
parser.go:532:1: function=parser.declStmt: recursive call changes callback capture state
```

Two rules meet there.

1. **Story #1088 made a method value carry its receiver.** `p.value` of a
   selector that names a method rather than a field now yields a synthetic
   holder wrapping the whole receiver, with the bound declaration recorded.
   That is correct and required: a method value can reach every callback its
   receiver reaches, so storing one must refuse. Its consequence is that
   `p.constDecl` is now *tainted* wherever the parser holds the error handler.

2. **Story #810's recursion rule only knew closures.** A recursive edge may
   re-bind a func-typed parameter, but only from one local *closure* to
   another: `dependencyCallbackClosureKeepsCallbackPlaces` required
   `entry.callback.lit != nil && current.callback.lit != nil`.

`parser.appendGroup` (parser.go:532) is entered from `fileOrNil` as
`appendGroup(f.DeclList, p.constDecl)` and re-entered, through
`constDecl -> ... -> stmtOrNil -> declStmt`, as `appendGroup(nil, f)` with
`f` freshly selected as `p.constDecl`. Same method, same receiver, a
different wrapper object — which under rule 2 read as "identity changed
across tainted state".

## The rule this lane adds

`dependencyCallbackKeepsCallbackPlaces` (renamed from
`...ClosureKeepsCallbackPlaces`) now accepts, on either side, any function
value the proof can follow into:

> `dependencyCallbackProvableFunctionValue(v)`: a local closure — a callback
> *with* a literal — or a method value bound to a source-visible
> declaration.

The places condition is unchanged: `places(current) ⊆ places(entry)`, where
a place is `(holder entity identified by lineage, name, escaped)` at which an
original callback sits. A method-value wrapper contributes *no* place of its
own; it only navigates to its receiver, so two freshly selected
`p.constDecl` values name the same single place `object:<p>.errh` and the
subset holds.

Soundness rests on the same two obligations story #810 established, both of
which already exist for method values:

1. Admitting the edge yields `generalized = true`, which obliges
   `recursiveBodyStoresCallback` to walk **this** body with **these** actuals
   and refuse any store of a callback-bearing value.
2. A *call* of the new value proves its body rather than escaping into an
   opaque callee: `callWithResolvedCallee` dispatches a `boundMethod` holder
   to `callFunction` with the captured receiver.

Everything else stays fail-closed. A callback with no literal (the original
callback, or the unknown value a pointer of unproven origin yields) is not a
provable function value; neither is an object whose binding a join erased,
and a tainted callee without one already refuses at the call.

## The budget this lane moves

With the rule fixed, the walk gets further and runs out of *depth*:

```
package/Parse: ok=false steps=8617 maxDepth=64
  parser.go:864:1: function=parser.type_: generalized recursive body may store callback-bearing value
```

That was a silent exhaustion: `recursiveBodyStoresCallback` answered "this
body stores a callback" at `depth > p.depthLimit` without recording a
refusal, so the outer message named a rule where the cause was a budget. It
now records `generalized body summary bounds exceeded depth=… steps=…`.

The measurement (`TestS319CallbackProofSyntaxParseDepth`):

```
depthLimit=64   ok=false  steps=8617   maxDepth=64
depthLimit=96   ok=true   steps=10426  maxDepth=65
depthLimit=256  ok=true   steps=10426  maxDepth=65   (ParseFile: 67)
```

The walk charges **two** levels per nested Go frame — one for the call
(`callFunction`), one for the body it enters
(`function -> blockWithCurrent`) — so a bound of 64 admitted only 32 nested
frames. A recursive-descent parser exceeds that as a matter of course once
method values are tainted and their bodies are therefore walked.
`dependencyCallbackProofDepthBound` is raised to 256 (128 frames). The step
budget is untouched and remains the cost bound that actually binds: `Parse`
spends 10426 of 20000 steps, and every body summary is memoized by its
obligation, so a deeper bound buys reach without buying re-walks.

`TestS281CallbackProofUnreachableCalleeMiniPrinter` builds a call chain that
must outgrow the bound; its `levels` is now derived from the bound instead of
pinned at 80, so the fixture keeps measuring what it was written to measure.

## What is red without each half

| change reverted | `TestS319CallbackProofRecursiveMethodValue` | whole-package `syntax.Parse` |
| --- | --- | --- |
| places rule (method values rejected) | refused: recursive call changes callback capture state | refused at `declStmt`, maxDepth 45 |
| depth bound back to 64 | passes (shallow reduction) | refused: generalized body summary bounds exceeded depth=65 |
| neither | passes | admitted, maxDepth 65 |

Both halves are necessary; neither is sufficient.

## Negatives preserved

- `interp/bashpp_s319_callback_proof_method_value_test.go` — story #1088's
  interface-dispatch and method-value retention cases, unchanged.
- `interp/bashpp_s319_manager_{future_dispatch,alias_mutation,global_alias}_test.go`
  — unchanged.
- `TestS281CallbackProofUnreachableCalleeNegatives`,
  `TestS281CallbackProofClosureParameterNestedRetention`,
  `TestDependencyCallbackProofRecursiveCloneLineage` — unchanged; the
  receiver rule is still a refusal on a distinct receiver entity.
- New in this lane, both refusing:
  - `places_grow` — the frame is entered with a closure that reaches no
    callback and the recursive edge re-binds to a method value that reaches
    one. One more place than the entry held, so refused.
  - `method_value_retained` — the recursive edge is fine, but the body stores
    the method value into a package global.
