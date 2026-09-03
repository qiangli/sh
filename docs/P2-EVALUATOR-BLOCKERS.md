# Bash++ P2 — evaluator blockers

Sprint 114 · parent story `4a1ec792bd10` · recorded 2026-09-03 · owner `claude-j`

This records what P2 does **not** close, with the evidence, so the next owner
does not re-run the same time-box. The decided half is in
`docs/bashpp-p2-evaluator-decision.md`.

## Blocker 1 — no adopted in-process interpreted evaluator

**Status: blocked, with a measured cause.** The capability/fallback decision is
made and executable, and the adapter seam exists (`bashPPInterpreter` in
`interp/bashpp_eval.go`), but nothing implements it. Every call therefore
reaches the reviewed native toolchain — announced, never silently.

### Why the obvious candidate was rejected

`github.com/traefik/yaegi@v0.16.1` under Go 1.26.5. Apache-2.0 and pure Go, so
it passes the licensing and pure-Go constraints; it was rejected on behavior.
Reproduction: a `main.go` calling `interp.New` + `stdlib.Symbols`, then
`Eval` per probe.

```
local-module             OK          # example.com/localmod/greet via GoPath
local-module-call        OK          # prints "hello, p2"
local-module-init        OK  Inits=1 # package init ran exactly once
duplicate-import         OK
dup-init-not-rerun       OK  Inits=1 # re-import did not re-run init
blank-import             OK
dot-import / call        OK          # prints "dot-ok"
missing-pkg              FAIL  unable to find source related to: "example.com/nope/absent"
package-main             OK          # <-- WRONG: Go refuses to import package main
range-over-func          PANIC nil type   # <-- yaegi interp/cfg.go
STDLIB-MISS import "iter"     -> unable to find source
STDLIB-MISS import "unique"   -> unable to find source
STDLIB-MISS import "weak"     -> unable to find source
STDLIB-MISS import "structs"  -> unable to find source
```

Session persistence was also confirmed working (`x := 41` then `x + 1` → `42`),
which is the one capability the native path structurally cannot provide — so
the rejection is not for lack of the thing P2 wants.

Neither the `package main` acceptance nor the range-over-func panic is what
retired it, and recording that correction matters. Review established that both
are contained by the policy layer: `classifyBashPPPackage` refuses
`capNotBuildable` at import time so no adapter ever sees a `package main`, and
a `recover` converts the panic to a decline before output is released. A
bounded adapter was built on that basis.

**It was then retired on independent probes of upstream `master` against Go
1.27**, which found contract-level blockers no bounded adapter can work around:

1. **Modules and workspaces are unsupported upstream.** Non-stdlib imports
   resolve from a GOPATH source layout, so local-module, workspace and vendor
   packages — most of what P2 must serve — never reach the interpreter at all.
2. **Stdlib and language support stop at Go 1.22.**
3. **`EvalWithContext` returns while precompiled calls continue to run.**
   Cancellation is unsound: the shell observes a cancelled call whose side
   effects are still happening.
4. **Direct `os.Stdout` escapes** unless process-global environment is set, so
   output atomicity cannot be guaranteed.
5. **No safe fork/reset clone**, so per-subshell isolation cannot be provided.

Rows 3 and 4 are the decisive pair: together they mean a call can begin, emit
output, ignore cancellation, and still be reported as declinable — after which
the announced native fallback runs it a **second time**. Executing
side-effecting code twice is a correctness failure, and nothing in the
published API prevents it.

**Yaegi is not a dependency of this module and must not become one.** The
adapter and the `go.mod` entry were reverted; only the policy slice was kept.

### What the next owner should evaluate instead

**Not another off-the-shelf adapter.** The three requirements that retired
Yaegi are the ones any candidate must satisfy up front, and they should be
probed before any integration work:

1. **Interruptible execution** — cancelling must actually stop side effects,
   not merely return early.
2. **Output atomicity** — a call that declines must have written nothing, or
   the announced fallback double-prints and double-executes.
3. **Clonable per-subshell state** — a shell forks constantly.

Only requirement 3 is about performance; 1 and 2 are correctness. A promising
direction that satisfies all three by construction is a **bounded evaluator
over the reviewed stdlib inventory** using `reflect` over an explicit symbol
registry, compiled by the same Go 1.27 toolchain as the shell so it cannot
drift from the language, with limits that are enumerable rather than emergent.
`syntax.BashPPStdlibImportAllowed` already defines the inventory it would
cover. That was scoped but **not implemented**; P2 evaluator completion is a
separately scoped architecture task.

## Blocker 2 — interpreted/native differential evidence is not obtainable yet


The P2 gate requires interpreted/native differential evidence for bounded
stdlib plus local-module calls. With no interpreted evaluator there is no
second side to compare, so this is **blocked on blocker 1**, not independently
failing. What exists instead:

- the native path is proven end-to-end by the pre-existing
  `TestNativeBashPPSelectorExitStatusIsExact` (real toolchain, `os.Exit(7)`
  observed exactly);
- the *policy transition* that would carry a real interpreter is proven by
  `TestBashPPCallFallbackIsAnnounced` and
  `TestBashPPCallFailureIsNotRetriedNatively`.

Claiming the gate met on those would be dishonest: they test the dispatch, not
an interpreted execution.

## Blocker 3 — acceptance items not covered in this run

Scoped, understood, and deliberately not claimed:

- package init / blank / dot lifecycle **through bashy's own evaluator**
  (measured only against the rejected yaegi prototype above);
- live `set -o bashpp` initialization and actual interactive duplicate import
  as end-to-end shell sessions;
- forced-shell `command import` end-to-end;
- collision / atomicity / cancellation / session / subshell isolation
  end-to-end. The pre-existing helper-level tests in
  `interp/bashpp_import_internal_test.go` cover the registry contract, and
  Sprint 113's handoff explicitly warns that those are **not** end-to-end
  evidence.

## What this run did land


- The capability/fallback decision, executable and fail-closed
  (`interp/bashpp_eval.go`, `docs/bashpp-p2-evaluator-decision.md`).
- Import-time classification from `go list` facts, with refusal on the
  cgo / compiled-only / package-main / platform-mismatch / unreviewed-stdlib /
  missing / unclassified classes.
- The no-silent-fallback rule, enforced and tested.
- The `bashPPInterpreter` adapter seam, package-private, with no public ABI
  change.
- The raw-string import compatibility decision, with byte-identical-to-Classic
  evidence for the grouped form.
- Classic/POSIX preservation: Classic Bash acquires no evaluator at all
  (`TestBashPPDefaultEvaluatorIsThePolicy`).
- End-to-end evidence through a real runner and the reviewed toolchain:
  a local-module package classified and executed
  (`TestBashPPLocalModuleRunsThroughPolicy`); every refused class failing at
  the `import` line rather than at a later call site
  (`TestBashPPRefusedClassesFailAtImport`); and a grouped import containing a
  refused package leaving the session registry empty
  (`TestBashPPRefusedImportLeavesRegistryUntouched`).
