# Bash++ P2 — production evaluator contract

Sprint 114 · parent story `4a1ec792bd10` · revised 2026-09-03

P2 uses one production evaluator: the reviewed Go 1.27 toolchain adapter. The
adapter is selected behind the package-private `bashPPEvaluator` interface, so
it remains replaceable without creating public ABI or evaluator-specific AST.
Yaegi was time-boxed and retired; it is not a dependency and must not return.

“Interpreted” in the Sprint 114 exit criterion means that source is parsed and
executed through the Bash++ shell interpreter. It does **not** mean that Go
package code runs in an unsafe in-process Go interpreter. Tests and documents
must not use those meanings interchangeably.

## Capability policy

Classification comes from `go list -e -json` facts before the session import
registry changes. The zero values remain fail-closed.

| Capability class | Production policy | Diagnostic timing |
|---|---|---|
| reviewed pure-Go standard library | reviewed toolchain adapter | import |
| pure-Go module/workspace/vendor/GOPATH package | reviewed toolchain adapter | import |
| requires cgo | refuse | import |
| compiled/export-only, with no Go source | refuse | import |
| package `main`, no buildable files, or platform mismatch | refuse | import |
| standard package outside the reviewed inventory | refuse | import |
| missing or unclassified | refuse | import |

Cgo stays refused even when the host toolchain could build it: this repository
promises a pure-Go runtime and does not acquire a C toolchain transitively.
Every other refused class also fails on the import statement, before a later
call can obscure the source of the error. A grouped failure is atomic.

There is no interpreted-to-native fallback in production, so there is no
fallback warning. The reviewed toolchain is the selected engine, not a silent
downgrade from another engine.

## Session, initialization, and process semantics

The import namespace belongs to a `Runner`. Separate interactive `Run` calls
reuse it; a repeated identical import is idempotent; `Reset` clears it; and a
subshell receives a private clone. Ordinary and aliased imports bind package
names, dot imports provide the unqualified call namespace, and blank imports
participate only for initialization side effects.

An import declaration resolves and registers a package but does not itself run
package code. A selector call creates one ordinary Go program and executes it
with the reviewed toolchain. The target package and every blank import in the
session are imports of that program, so their `init` functions run exactly once
for that evaluation unit, just as in directly compiled Go. Package globals do
not pretend to persist across separate selector calls; namespace persistence
and Go-process state persistence are deliberately different contracts.

This process boundary gives cancellation real force: cancelling the runner
kills the build or evaluation process, and the returned error is the context
error. Standard input, output, and error are connected to the runner streams.
Tests compare the Bash++ runner and a directly built Go program exactly on
stdout, stderr, and exit status.

## Resolution and lifecycle evidence

Executable tests cover reviewed standard packages and real temporary fixtures
for local modules, workspace `use` and `replace`, vendor mode, and GOPATH mode.
They cover ordinary, alias, grouped, blank, and dot imports; interactive
idempotence; init behavior; namespace persistence; collision rollback;
subshell/session isolation; cancellation; and forced-shell `command import`
and quoted `"import"` forms.

## Raw-string compatibility

Go accepts both `"fmt"` and raw-string import paths. Bash++ claims only the
interpreted-string spelling. Backquotes already mean shell command
substitution, while single quotes and ANSI-C quotes are shell quoting. Taking
those Class-E forms would change working shell programs. The focused syntax
test keeps their Classic/POSIX fallback byte-identical.

## Yaegi disposition

The time-box evidence is retained in `P2-EVALUATOR-BLOCKERS.md`. Its failures
explain why Yaegi was retired; they are not evidence that this production
toolchain contract is incomplete.
