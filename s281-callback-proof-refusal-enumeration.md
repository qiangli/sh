# Sprint 281 / Story 810 — full remaining scope of the SDK `Parse` callback proof

Sprint: #281
Story: #810
Story-ID: 48c1146a3ab0

## Why a measurement was needed

`dependencyCallbackProof.refuse` records only the **first** region it cannot
certify, so each accepted fix (L4 → P1 budget/memo + goto → P2 callee proof →
P3 callback-reachability frame comparison) only uncovered the next site. From
the refusal alone the operator cannot tell whether the remaining scope is one
rule or fifty.

## The hook

`dependencyCallbackProof.enumerate` (nil in production, the only configuration
in which `refuse` reports a refusal) replaces the verdict of every refusal with
whatever the hook returns. The measurement returns `true`: each refusal becomes
a local *assume-refused*, and the walk continues into the siblings of the region
that refused, so one run reaches every site the production proof would only ever
reach one fix at a time.

`stepLimit` was factored out of the six `p.steps > 20000` guards so the
measurement can see past the first refusal. Production is unchanged: the
constructor sets `stepLimit = dependencyCallbackProofStepBudget = 20000`, the
same budget as before.

Test: `interp/bashpp_s281_callback_proof_enumerate_test.go`
(`TestS281CallbackProofRefusalEnumeration`). It asserts nothing — it is a report.

Two file sets are enumerated, because two gates prove over two of them:

- `selected` — the six files the manager fixtures and
  `TestS281ManagerSDKProofGraphDiagnostic` parse.
- `package` — every non-test `.go` file of `cmd/compile/internal/syntax`, which
  is what the end-to-end `TestS281DependencySourceCallbackProof` proves over.

## BEFORE the fix (lane base `s281-cand2`)

```
=== selected/Parse:      4 distinct sites,  4 refusals reached,  7725 steps, 1 class
=== selected/ParseFile:  4 distinct sites,  4 refusals reached,  7732 steps, 1 class
  [4 sites, 4 hits] recursive call changes callback capture state
      parser.go:507:1                  parser.argList               hits=1
      parser.go:507:1                  parser.complitexpr           hits=1
      parser.go:507:1                  parser.structType            hits=1
      parser.go:507:1                  parser.interfaceType         hits=1

=== package/Parse:       7 distinct sites, 11 refusals reached,  8512 steps, 2 classes
  [5 sites, 5 hits] recursive call changes callback capture state
      parser.go:507:1                  parser.appendGroup           hits=1
      parser.go:507:1                  parser.argList               hits=1
      parser.go:507:1                  parser.complitexpr           hits=1
      parser.go:507:1                  parser.structType            hits=1
      parser.go:507:1                  parser.interfaceType         hits=1
  [2 sites, 6 hits] function X or proof bounds exceeded depth=N steps=N
      printer.go:91:1                  printer.writeBytes           hits=3
      parser.go:758:1                  isTypeElem                   hits=3

=== package/ParseFile:   9 distinct sites, 13 refusals reached,  8518 steps, 2 classes
  [5 sites, 5 hits] recursive call changes callback capture state
      (the same five sites)
  [4 sites, 8 hits] function X or proof bounds exceeded depth=N steps=N
      printer.go:105:1                 printer.writeString          hits=1
      printer.go:91:1                  printer.writeBytes           hits=3
      parser.go:758:1                  combinesWithName             hits=3
      printer.go:932:1                 combinesWithName             hits=1
```

**The dominant class is one rule at one declaration.** `parser.go:507` is
`func (p *parser) list(context string, sep, close token, f func() bool) Pos`.
`list` calls `f`, a *local closure* that parses further and re-enters `p.list`
with a *different* local closure; the `function=` names are the five re-entry
points (`argList`, `complitexpr`, `structType`, `interfaceType`, `appendGroup`).

The frame comparison refused with `argument f identity changed across tainted
state`: `dependencyCallbackValue.tainted()` is true of **any** closure, so two
different local closures looked like two different callback-bearing arguments
even when both held the callback in exactly the same place — `p`'s handler field,
reached through their captured `p` — or, for `appendGroup`, when the new one held
it nowhere at all.

The second class is not a rule: it is the depth-64 bound running out inside a
generalized body summary that reaches the node printer. This lane does not
change it.

## AFTER the fix

```
=== selected/Parse:      0 distinct sites,  0 refusals reached,  7950 steps, 0 classes
=== selected/ParseFile:  0 distinct sites,  0 refusals reached,  7957 steps, 0 classes

=== package/Parse:       2 distinct sites,  6 refusals reached,  8775 steps, 1 class
  [2 sites, 6 hits] function X or proof bounds exceeded depth=N steps=N
      printer.go:91:1                  printer.writeBytes           hits=3
      parser.go:758:1                  isTypeElem                   hits=3

=== package/ParseFile:   4 distinct sites,  8 refusals reached,  8781 steps, 1 class
  [4 sites, 8 hits] function X or proof bounds exceeded depth=N steps=N
      printer.go:105:1                 printer.writeString          hits=1
      printer.go:91:1                  printer.writeBytes           hits=3
      parser.go:758:1                  combinesWithName             hits=3
      printer.go:932:1                 combinesWithName             hits=1
```

The `parser.list` class is gone from both file sets. Over the six selected files
the proof now refuses **nowhere**: `Parse` and `ParseFile` are admitted outright
in 7950 steps, well inside the 20000-step budget.

What remains, and only over the whole package, is the single depth-bound class
that was already there before this lane: the chain main walk → generalized
summary → `printer.printRawNode` → `printer.writeBytes` reaches depth 65 against
the depth-64 bound. It is a budget, not a rule, and it is the remaining scope of
the end-to-end `TestS281DependencySourceCallbackProof` gate.

## The rule this lane added

A recursive edge may re-bind a func-typed parameter from the local closure the
frame was entered with to **another local closure**, provided every place the new
closure holds the original callback is a place the entry closure held it too.
With `places(v)` the set of `(holder entity, name, escaped)` triples at which an
original callback — a closure with no literal — sits in `v`'s graph, holders
identified by lineage:

> `places(current)` is a subset of `places(entry)`, and neither side is a
> callback with no literal.

Which literal each closure is, and the callback-free graph each navigates to
reach those places, are free to differ: they are code and shape, not the
callback's position. Holding the callback in *fewer* places is admitted — that
only shrinks what this argument could retain — but not in one more.

Soundness rests on two things:

1. Admitting the edge yields `generalized = true`, which obliges
   `recursiveBodyStoresCallback` to walk **this** body with **these** actuals and
   refuse any store of a callback-bearing value. The new closure is proved there
   at every point the body uses it — including `done = f()`, which walks the new
   closure's body. The recursive shortcut is a coinductive assumption whose
   obligation is discharged for the very frame assumed.
2. That summary may not skip the obligation it is supposed to discharge. Its
   on-stack shortcut was keyed by function name, so a body re-summarized under a
   different closure returned "no stores" without walking it, and a closure
   reachable only from inside another one was never proved at all. It is now
   keyed by the *obligation*: the projected frame, which elides callback-free
   regions (so a recursion that only grows a lazily allocated map still
   converges) but keeps every function literal's identity (so different code is a
   different obligation).
   `TestS281CallbackProofClosureParameterNestedRetention` is red without this.

The receiver rule is deliberately untouched: a distinct receiver entity remains a
refusal, as `TestDependencyCallbackProofRecursiveCloneLineage` requires.
