# Sprint 281 / Story 810 — P5: the callback-unreachable callee

Sprint: #281
Story: #810
Story-ID: 48c1146a3ab0

Lane base `s281-cand3` (P4 = refusal enumeration hook `d7985265` + closure
parameter rule `0a22da52`). Read [`s281-callback-proof-refusal-enumeration.md`](s281-callback-proof-refusal-enumeration.md)
first: it measured the whole remaining scope and found exactly one class left
over the whole package.

## The class this lane closes

P4's measurement, over every non-test file of `cmd/compile/internal/syntax`:

```
=== package/Parse:       2 distinct sites,  6 refusals,  8775 steps, 1 class
=== package/ParseFile:   4 distinct sites,  8 refusals,  8781 steps, 1 class
  [..] function X or proof bounds exceeded depth=N steps=N
      printer.go:91    printer.writeBytes     hits=3
      printer.go:105   printer.writeString    hits=1
      parser.go:758    isTypeElem             hits=3
      parser.go:758    combinesWithName       hits=3
      printer.go:932   combinesWithName       hits=1
```

The chain is main walk → generalized recursive summary → `printer.printRawNode`
→ `printer.writeBytes`. Nothing in it can reach the error handler: the SDK
`printer` holds `output io.Writer`, a `linebreaks` bool, an indent counter, a
`lastTok`, and the node it is printing. It does not hold the parser, and it does
not hold `errh` — `printer.go`'s `Fprint(w io.Writer, x Node, ...)` builds the
printer from a writer and a node, and `parser` never hands it itself. The proof
still walked all of it and ran out of depth doing so.

## The rule

> A call whose callee **cannot reach the original callback** need not be walked.
> If no actual argument, no receiver, and no captured variable of a callee
> closure reaches a callback — and no package-level variable can reach one,
> which holds in every state this proof has not already refused — then the
> callee can neither call the callback nor store it. It is summarized as
> effect-free **without descending**: no depth is spent, no further steps are
> charged.

Reachability is a fact about the proof's own value graph, never about a name, a
type, a package or a declaration. `dependencyCallbackValueReachesCallback` runs
the *same* callback-reachable projection P3/P4 introduced for the recursion rule,
`dependencyCallbackCallbackPlaces`: an original callback is a closure with no
literal, and a value reaches one exactly when that projection is non-empty. A
place walk that exhausts `dependencyCallbackPlaceLimit` reports "reaches", so the
rule is only ever applied on a completed walk.

The rule is checked at the three call-dispatch sites in
`callWithResolvedCallee`: the function-literal callee (args + the closure's own
captures), the plain identifier callee (args), and the selector callee (args +
the receiver value actually passed). The summary the caller applies is the one
that was already applied to clean actuals before descending —
`p.markEscaped(...)` — because the callee may have retained those *clean* values,
so a later callback store into them must still refuse.

## Soundness

A Go body observes exactly:

1. its parameters,
2. its receiver,
3. the variables its closure captured,
4. the package-level identifiers, and
5. whatever it builds from (1)–(4), including the results of further calls,
   which by induction observe only their own (1)–(4).

Package-level constants and functions are code, not callback-bearing state.
That leaves package-level **variables**, and no package-level variable can hold a
callback-reachable value in any state this proof has not already refused. Every
route into one is a refusal today:

| route | refusal |
| --- | --- |
| `g = cb` | `assign`, package-global case: *assignment stores callback-bearing value in package global* |
| `g.f = cb`, `g[i] = cb`, `*g = cb` | a global reads as an untracked value, so the base is unowned: *selector/index/pointer assignment stores callback-bearing value …* |
| `unresolvable(cb)` | *tainted call target is not exactly one local function* / *is not source-visible* |
| `resolvable(cb)` that stores into a global | descends (a tainted actual is never skipped) and refuses inside |
| `return cb` out of the region | *return exposes callback-bearing value* / *naked return exposes tainted named result* |

So checking (1), (2), (3) is checking the complete set, and a callee that fails
all three cannot **name** the callback value at all. Not naming it, it cannot
invoke it (synchronously or from a goroutine), cannot store it anywhere, and
cannot return it. Its contribution to the callback's lifetime is nothing, which
is precisely what the summary asserts.

This subsumes — and explains — the descent it replaces. The old comment at that
site was right that *clean actuals alone* do not make a callee harmless: a body
can materialise an unknown value on its own (a pointer of unproven origin
dereferences to the "maybe the callback" marker) and store it where the callback
would be retained. What licenses not descending is the strictly stronger fact,
that **no route from this call site reaches a callback at all** — under which the
unknown-dereference conservatism is vacuous, because the value at the far end of
that pointer is itself reachable only through (1)–(4).

The `syntax` printer falls in this class for the right reason: `p.out` /
`Fprint`'s writer is a sibling value, not the parser, and holds neither `errh`
nor anything that reaches it, so the projection over the printer's receiver and
arguments is empty. Had the printer held the parser or the handler, the receiver
would reach a callback and the body would still be walked.

## Negatives that stay refused

`TestS281CallbackProofUnreachableCalleeNegatives` — five callees whose *direct*
arguments are clean, one per route, each of which must stay refused, and each of
which refuses naming the package global (so the refusal is the store, not a
bound):

- **`package_variable`** — `var g func(); var saved func(); func helper() { saved = g }`,
  driven by `Retain(cb) { g = cb; helper() }`.
- **`package_variable_via_helper`** — the same, with the global store itself
  behind `stash(cb)`, so the refusal is only reachable by descending into a
  callee that *does* reach the callback.
- **`nested_pointer_field`** — `take(outer{p: &inner{f: cb}})`: the argument is a
  struct value whose nested pointer field reaches the callback.
- **`closure_capture`** — a zero-argument closure `run := func() { saved = f }`
  that reaches the callback only through its captured `f`.
- **`interface_dynamic_value`** — `take(h holder)` given `&impl{f: cb}`: nothing
  in the static type says "callback", and the projection over the value says it.

One case in the lifetime table moved, and moved deliberately:
`unknown star value copy cannot hide callback` —
`func Retain(h *H) { copied := *h; saved = copied.f }` called as `Retain(nil)`
from `Parse(cb)` — asserted a refusal that this rule removes. Nothing `Parse`
hands `Retain` reaches `cb`, and no package variable can, so `Retain` cannot
name `cb`; the refusal was the old model's, not the semantics'. It is now
`want: true` and renamed *unknown star value copy in a callback-unreachable
callee*, and the unknown-dereference conservatism is kept where it is
load-bearing by a new sibling, *unknown star value copy hides callback from a
reaching callee*, which hands the same body a second argument
(`&keep{cb: cb}`) it never even reads and is refused at the same global store.

## Enumeration after

```
=== selected/Parse:      0 distinct sites, 0 refusals, 7819 steps, 0 classes
=== selected/ParseFile:  0 distinct sites, 0 refusals, 7826 steps, 0 classes
=== package/Parse:       0 distinct sites, 0 refusals, 7912 steps, 0 classes
=== package/ParseFile:   0 distinct sites, 0 refusals, 7919 steps, 0 classes
```

Zero remaining sites over the whole package, in **7912 / 7919** steps against the
unchanged 20000-step budget, and within the unchanged depth-64 bound. Steps fell
from 8775 / 8781: not descending is cheaper than descending.

## What a bound change would have required

Measurement only — the bound is **not** changed. The depth bound was factored out
of its three `depth > 64` guards into `p.depthLimit` (production:
`dependencyCallbackProofDepthBound = 64`), exactly as P4 factored the step
budget, and `p.maxDepth` records the deepest body entered.
`TestS281CallbackProofDepthMeasurement` drives the whole package at the
production bound and at a raised test-only bound of 4096:

Without the rule (taken on the red commit `interp: state the
callback-unreachable callee obligation`, which has the factoring but not the
rule):

| entry point | bound | maxDepth | steps | refusals |
| --- | --- | --- | --- | --- |
| `syntax.Parse` | 64 | 63 | 8775 | 6 |
| `syntax.Parse` | 4096 | **65** | 8775 | 0 |
| `syntax.ParseFile` | 64 | 64 | 8781 | 8 |
| `syntax.ParseFile` | 4096 | **67** | 8786 | 0 |

With the rule, both bounds give the same answer and there is nothing to trade:

| entry point | bound | maxDepth | steps | refusals |
| --- | --- | --- | --- | --- |
| `syntax.Parse` | 64 | 47 | 7912 | 0 |
| `syntax.Parse` | 4096 | 47 | 7912 | 0 |
| `syntax.ParseFile` | 64 | 49 | 7919 | 0 |
| `syntax.ParseFile` | 4096 | 49 | 7919 | 0 |

So raising the bound would have had to go to at least **67** to admit both
entry points; with the rule the deepest body entered is **49**, seventeen frames
clear of the unchanged 64 — and that number is a property of one SDK release's call graph,
not of the rule. The structural rule is bound-independent:
`TestS281CallbackProofUnreachableCalleeMiniPrinter` proves a faithful mini
printer (a `writer` struct over a byte buffer, an 80-frame `printRawNode`-like
chain, reached as a plain field of a callback-holding parser) *within* the
production 64, which no bound below 84 could have done.

## End-to-end gate

`TestS281DependencySourceCallbackProof` **PASSES**. The proof error
(`asynchronous or retained original function callbacks are unsupported for
syntax.Parse`) is gone.

One thing in that fixture had to be corrected, and it is not about the proof.
The gate parsed `"package p\n@"` and wanted `true true true true`. Real
go1.27.1 prints `false true true true` for that input: `syntax.Parse` fails at
the top level and returns a **nil** `*File`, so `file != nil` is false natively
too. The expectation had never been exercised, because the proof refused before
the program ever ran. Moving the bad token inside a function body
(`"package p\nfunc f() { @ }"`) makes `Parse` recover and return a `File` as
well as the first error, so all four facts the program prints are non-trivial and
the authored oracle `true true true true` is the one real go1.27.1 prints —
verified by running the same program natively under `$GOROOT/src/cmd/compile`.

## Regression

`TestDependencyFunctionCallbackLifetimeProof|TestS281ManagerCallbackProof.*|TestDependencyCallbackProof.*|TestS281ManagerSDKProofGraphDiagnostic|TestS281DependencySourceCallbackProof|TestS281CallbackProof.*|TestDependencyCallbackSameFrame.*`

- lane base: **203 PASS / 1 FAIL** (`TestS281DependencySourceCallbackProof`)
- after: **215 PASS / 0 FAIL** — the 203 plus 1 new lifetime-table case, the
  mini printer, 1+5 negatives, and 1+2 depth-measurement subtests.

Zero new failures.
