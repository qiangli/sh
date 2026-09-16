# Plan: typed-callable decorator lowering (Sprint 197, Story 395)

Design authority: `dhnt/docs/bashpp-decorators-and-advice.md` §5 and
`docs/lowering-agentic.md` §Preserving public callable signatures (option 2:
the check — here the chain — lives inside the private entry, so every indirect
route reaches it).

## Shape

- `shellrt.Call` mirrors `interp.Call` field for field (`Name`, `Site`,
  `Caller`, `Args`, `Results`, `Status`, `Agentic`, `Advised`) and `Next()`.
  `Site` is a source-visible `filename:line` string. The native slot is the process-level
  `shellrt.Decorators map[string]DecoratorFunc` with
  `DecoratorFunc func(ctx, *Call, []DecoratorArg) error` — the same signature
  as `interp.DecoratorFunc`, native adapters must bridge the continuation explicitly; copying fields
  cannot transfer either engine's private continuation. Native `Next(ctx)`
  derives a Program for the continuation without mutating its parent.
- A decorated typed callable forces execution mode (like `agentic`), so it is
  emitted as private entry + public wrapper. Inside the private entry, after
  the agentic gate and result/argument plumbing, the original body becomes a
  closure with the callable's own signature (plus the program) and the chain
  runs it through `Program.Decorate`. Every `Next` rebinds the body's
  parameters from `Call.Args` by type assertion (`EDECO-ARG`), and the chain's
  final `Call.Results` are type-checked back into the declared results
  (`EDECO-RESULT`). Public symbol and all indirect routes (method value,
  interface capability dispatch, function handle) are unchanged: they already
  reach the private entry.
- One ordinary Go wrapper per signature: the closure and the assertions are
  generated per callable; no generic decorator framework, no reflection in
  generated code.
- Decorator rungs: a unit function whose first parameter is the predeclared
  `*Call` resolves statically to its private entry, with per-invocation
  argument evaluation through the existing Bash# plan (named + default
  arguments); any other name resolves at call time through
  `shellrt.Decorators`, else `EDECO-UNDEF` status 1.
- The predeclared `Call` is emitted as `type Call = shellrt.Call` only in a
  Bash++ unit that names it and does not declare its own; a user `type Call`
  shadows it exactly as in the interpreter.

## Static diagnostics

`EDECO-RESERVED` (`@pkg.name`), `EDECO-SELF`, `EDECO-SIG` (unit function or
shell function that is not a decorator), `EDECO-CYCLE` (decorator graph among
unit functions), decorated shell functions (`LOWER-EUNSUPPORTED`, out of scope
for the MVP), and result plans that need a capability frame.

## Verification

Generated Go is built and executed for real (`compile`/`execute` in
`lower/compile_test.go`, which also diffs against the interpreter): stack
order, args/results rewrite, skip, repeated `Next`, typed channel and map
identity through the chain, method value / interface / handle routes, the
native slot, the gate ordering (agentic denial before any decorator runs),
and the undecorated D1 fidelity suite.

## Sprint 197 broad-gate performance follow-up

The DO full gate exposed deadline failures in the original mutex and Tour image
fixtures. A CPU profile of the unchanged mutex source showed repeated local type
descriptor construction and duplicate environment enumeration at every native
call. Cache immutable descriptors in the copied toolchain, keyed by source file
and a copied import map; a different file or changed import rebuilds them. Keep
the native session's namespace-drift checks. Collect the shell environment once
per request, preserving the separate Go source runtime environment. Verify import
invalidation, copied-runner isolation, generic instantiation, callback/reset
behavior, and the unchanged original fixtures under the race detector. Fixture
bodies, assertion outputs, and harness deadlines remain unchanged.
