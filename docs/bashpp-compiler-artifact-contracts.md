# Bash# compiler-artifact contracts

**Ratified by the operator, 2026-09-22 (Sprint 249).** This page applies the
language-level policy in the `bashsharp` repository,
`docs/bashpp-go-implementation-claim.md`, section "Language, not
implementation: what Bash# does not mimic". Bash# is a language. It matches
Go's program semantics and does not mimic every feature or artifact of the gc
toolchain.

## Product decision

Bash# executes the typed source program in its interpreter, or compiles the
lowered source through the supported Go compiler.  It does not implement or
promise to reproduce `cmd/compile`'s optimizer, liveness analyser, proof pass,
inliner diagnostics, or target-specific assembler.  Those are implementation
artifacts of a particular Go compiler and target, not source-language
observables in either Bash# mode.

Accordingly, an upstream compiler-corpus recipe that matches one of those
artifacts is **inapplicable as an artifact assertion**.  This is a product
decision, not a PASS: a harness must report the named decision while retaining
the root and both requested modes in its accounting.  It must not synthesize
gc's expected lines, match source-path-specific strings, execute the tested
source natively, or reduce its denominator.

## Classified upstream roots

| Root | gc-owned assertion absent from Bash# | Replacement observable contract |
| --- | --- | --- |
| `closure3.go` | `-m` inliner decisions and diagnostic positions | Function literals capture lexical variables and calls observe their retained state. |
| `codegen/switch.go` | target-specific assembly opcode patterns | Expression-switch selection and case grouping preserve source behavior. |
| `live_regabi.go` | register-ABI liveness and `-live` diagnostics | Select receive assignment commits its value and `ok` result to dereferenced destinations after the selected case wins. |
| `nilptr3.go` | `-d=nil` generated/removed nil-check diagnostics | A nil pointer dereference remains an observable panic. |
| `prove.go` | `-d=ssa/prove/debug=1` proof-pass diagnostics | A source bounds guard controls whether indexing is performed. |

`TestS249CompilerArtifactReplacementConformance` is the outside-corpus,
three-mode conformance test for these replacement contracts.  It compares
unchanged Go execution, interpreted Bash#, and the compiled Bash# artifact;
therefore it cannot turn a missing optimizer artifact into an administrative
PASS.

## Sprint 248: runtime and toolchain observations

**Ratified by the operator, 2026-09-22 (Sprint 248 decision D1).** The same
policy covers five runtime/native-bridge roots whose assertions are about the
gc toolchain or runtime, not the language. Each passes natively and in
compiled mode (the compiled artifact is built by gc). Interpreted mode has no gc
compiler hook, experiment or heap to observe, so each stays **FAIL by ID in
interpreted mode**, with no synthetic PASS and no reduction of the 21-root
Sprint 248 denominator.

| Root | Reason | gc-owned observation absent from interpreted Bash# | Replacement observable contract |
| --- | --- | --- | --- |
| `maymorestack.go` | `gc-only-check` | `-gcflags=-d=maymorestack` hook called before every stack-growth check | Deep recursion with large frames computes exactly. |
| `fixedbugs/issue47928.go` | `gc-only-check` | `//go:nointerface` under `-goexperiment fieldtrack` | Promoted pointer methods satisfy interfaces without the experiment. |
| `typeparam/mdempsky/15.go` | `gc-only-check` | `//go:nointerface` under `-goexperiment fieldtrack`, on generic promoted methods | Methods promoted through embedded generic types satisfy interfaces; absent methods do not. |
| `fixedbugs/issue15277.go` | `gc-observation` | `runtime.MemStats` heap deltas around `KeepAlive` | A kept-alive allocation keeps its contents; releasing it is well defined. |
| `fixedbugs/issue9110.go` | `gc-observation` | `runtime.MemStats` object counts (leaked sudogs) | Goroutines abandoning a select on timeout all complete. |
| `nilptr.go` | `unsafe-reinterpretation` | placement of a global in the first 256 MB of the address space | Nil pointer indirection through large arrays and structs panics recoverably. |

`TestS248RuntimeObservationReplacementConformance` compares unchanged Go,
interpreted Bash# and the compiled Bash# artifact for these contracts.
