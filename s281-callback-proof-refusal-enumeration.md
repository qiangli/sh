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

## BEFORE the fix (lane base `s281-cand2`)

```
=== syntax.Parse: 4 distinct sites, 4 refusals reached, 7725 steps, 1 classes
  [4 sites, 4 hits] recursive call changes callback capture state
      parser.go:507:1                                      parser.argList               hits=1
      parser.go:507:1                                      parser.complitexpr           hits=1
      parser.go:507:1                                      parser.structType            hits=1
      parser.go:507:1                                      parser.interfaceType         hits=1

=== syntax.ParseFile: 4 distinct sites, 4 refusals reached, 7732 steps, 1 classes
  [4 sites, 4 hits] recursive call changes callback capture state
      parser.go:507:1                                      parser.argList               hits=1
      parser.go:507:1                                      parser.complitexpr           hits=1
      parser.go:507:1                                      parser.structType            hits=1
      parser.go:507:1                                      parser.interfaceType         hits=1
```

**The entire remaining scope is one class at one declaration.** `parser.go:507`
is `func (p *parser) list(context string, sep, close token, f func() bool) Pos`.
`list` calls `f`, a *local closure* that parses further and re-enters `p.list`
with a *different* local closure; the four `function=` names are the four
re-entry points (`argList`, `complitexpr`, `structType`, `interfaceType`).

The frame comparison refused with `argument f identity changed across tainted
state`: `dependencyCallbackValue.tainted()` is true of **any** closure, so two
unrelated local closures — neither of which can reach the original callback —
looked like two different callback-bearing arguments.

## AFTER the fix

See `## AFTER` below (filled in by the same test after the closure-parameter
reachability fix).
