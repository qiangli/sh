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
