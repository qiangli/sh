# Bash++ P2 — evaluator capability and fallback decision

Sprint 114 · parent story `4a1ec792bd10` · recorded 2026-09-03

The design of record makes this document a **precondition** of choosing an
evaluator, not a report on one:

> Decide the fallback for cgo, compiled-only and unsupported-feature packages
> **before an evaluator is selected**, or the choice is made by whatever the
> prototype happened to run.
> — `dhnt/docs/bashpp-posix-superset-syntax.md`, "Yaegi-cannot-interpret → P2"

So the capability classes and their fallbacks are decided here first. The
evaluator time-box is reported second, and it did not get to change any row.

## 1. The two governing rules

1. **Fail closed.** An unclassified package is refused, never executed. The
   zero value of `bashPPCapability` is `capUnknown` and the zero value of
   `bashPPPolicy` is `policyRefuse`, so a class added later without a decision
   table row declines to run instead of quietly reaching the toolchain.
2. **No silent semantic fallback.** Whenever execution moves from the
   in-process evaluator to the reviewed native toolchain, the move is announced
   on stderr. A fallback nobody can observe is indistinguishable from the
   evaluator having been chosen by accident.

Both rules are executable, in `interp/bashpp_eval.go`, and pinned by
`TestBashPPPolicyFailsClosed` and `TestBashPPCallFallbackIsAnnounced`.

## 2. The decision table

Classification is derived from `go list -e -json` **facts**, never from how an
import path is spelled. `interp/bashpp_eval.go` holds the table; the rows below
are its readable projection, and `TestBashPPCapabilityPolicyTable` fails if the
two disagree.

| Capability class | Example | Policy | Diagnostic timing |
|---|---|---|---|
| `capInterpreted` — reviewed pure-Go stdlib | `fmt` | **interpret** in-process; while no interpreter is adopted, announced native fallback | import |
| `capNativeOnly` — pure Go, buildable, outside the reviewed inventory (local module, workspace, vendor, GOPATH) | `example.com/m/greet` | **explicit fallback** to the reviewed toolchain, announced | import |
| `capCgo` — requires cgo | any package with `CgoFiles` | **refused** | import |
| `capCompiledOnly` — no Go source to interpret or rebuild | export-data-only package | **refused** | import |
| `capNotBuildable` — `package main`, no buildable Go files, or excluded on this platform | `example.com/m/cmd` | **refused** | import |
| `capUnreviewedStdlib` — standard, but outside the reviewed Go 1.27 inventory | `internal/…` | **refused** | import |
| `capMissing` — unresolvable | `example.com/nope` | **refused** | import |
| `capUnknown` — unclassified | — | **refused** (rule 1) | import |

### Why cgo is refused rather than delegated

The reviewed native toolchain *could* build a cgo package. It is refused anyway
because `CLAUDE.md` states the project constraint plainly — **"Pure Go only: No
CGo, no C dependencies."** Delegating would make a C toolchain an undeclared
runtime dependency of a shell that promises not to have one. Refusing keeps the
promise, and the diagnostic says exactly which promise is being kept.

### Why the diagnostic timing is import, not first call

Every class that can be decided from package facts is decided at `import`. A
refused package must fail on the line that named it. An import that appears to
succeed and then fails at an unrelated call site reports the error in the wrong
place, and under `set -e` it aborts at the wrong statement. Consequently the
session import registry is only ever populated with packages that passed
policy — which is also what makes a failed import atomic with respect to it.

## 3. The evaluator time-box, and its outcome

The design authorised a time-box on Yaegi before writing a new evaluator. It
was run, against `github.com/traefik/yaegi@v0.16.1` (Apache-2.0, pure Go — both
constraints satisfied) on Go 1.26.5. Licensing was never the problem.

**Outcome: retired, not adopted.** Measured, not assumed:

| Probe | Result |
|---|---|
| `fmt.Println`, session persistence (`x := 41` → `x + 1`) | works — and is the capability the native path structurally lacks |
| local module, `init()` ran once, duplicate import idempotent | works (`Inits=1` after re-import) |
| blank and dot imports | work |
| missing package | clean, diagnosable error |
| post-1.22 stdlib: `iter`, `unique`, `weak`, `structs` | **absent** |
| `import` of a `package main` | **wrongly accepted** — Go refuses this |
| range-over-func (Go 1.23) | **panics** (`nil type`, in `interp/cfg.go`) |

Two of those look disqualifying but are **not**, and saying so matters because
an earlier draft of this document retired Yaegi on them and was wrong:

- **`import` of a `package main`** never reaches an adapter. The policy above
  refuses `capNotBuildable` at import time, so this blocker was double-counted.
- **The range-over-func panic** is outside the P2 selector-call subset, and a
  `recover` in the adapter converts a panic to a decline before any output is
  released.

Yaegi was nevertheless **retired**, on independent probes of upstream `master`
against Go 1.27 that found contract-level blockers a bounded adapter cannot
work around:

| Blocker | Why a bounded adapter cannot contain it |
|---|---|
| **Modules and workspaces are unsupported upstream** | Yaegi resolves non-stdlib imports from a GOPATH source layout. Local-module, workspace and vendor packages — most of what P2 must serve — never reach it. |
| **Stdlib and language stop at Go 1.22** | The gap against a Go 1.27 target is structural: v0.16.1 (April 2024, `go 1.21`) is the newest release and stdlib symbol tables are generated per Go release. |
| **`EvalWithContext` returns while precompiled calls keep running** | Cancellation is therefore unsound: the shell would observe a cancelled call whose side effects continue. There is no in-process way to interrupt it. |
| **Direct `os.Stdout` escapes** unless process-global env is set | Output atomicity cannot be guaranteed, so "a decline has written nothing" — the invariant that makes an announced fallback safe — cannot be upheld. |
| **No safe fork/reset clone** | A shell forks subshells constantly. Without a clonable interpreter there is no way to give each subshell isolated state. |

The third and fourth rows are the decisive pair. Together they mean a call can
begin, emit output, ignore cancellation, and still be reported as declinable —
at which point the announced native fallback would run it a **second time**.
Side-effecting code executed twice is a correctness failure, not a performance
one, and no wrapper around the published API prevents it.

**Yaegi is therefore not a dependency of this module, and must not become one.**

**What was retained.** The reviewed native toolchain
(`nativeBashPPEvaluator`, pinned by digest in `bashPPGoReviews`) stays as the
**policy-approved engine**, reached through the policy layer so that
classification always runs first. It is not session-persistent, which is the
capability P2 still owes.

P2 evaluator completion needs a **separately scoped architecture**, not another
adapter attempt. See `docs/P2-EVALUATOR-BLOCKERS.md`.

## 4. Raw-string imports — an explicit compatibility decision

Go accepts both `import "fmt"` and ``import `fmt` ``. **Bash++ claims only the
interpreted-string form.**

A backquote is already shell command substitution: in stock bash,
``import `fmt` `` runs the command `fmt` and passes its output to `import`. The
shape is Class E with a real, common meaning, so claiming it would silently
change what such a line does. `'…'` and `$'…'` fall to shell for the same
reason — they are shell quoting, not Go syntax.

The cost is accepted: ``import `fmt` `` is not a Bash++ import, and a user who
wants one writes the interpreted form, which is what gofmt produces anyway.
`syntax/bashpp_import_rawstring_test.go` pins both halves — that the raw forms
are unclaimed, and that a raw string inside a grouped import produces an error
**byte-identical to Classic Bash's**, so Bash++ has not even changed what a
broken script reports.

## 5. What this decision does not close

Genuine interpreted/native differential evidence still requires an adopted
in-process evaluator, and there is none. See `docs/P2-EVALUATOR-BLOCKERS.md`.
