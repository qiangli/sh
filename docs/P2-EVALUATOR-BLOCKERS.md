# Bash++ P2 — retired evaluator evidence

Sprint 114 · parent story `4a1ec792bd10` · revised 2026-09-03

This file preserves the evidence that retired Yaegi. It does not describe an
open P2 blocker. The production contract is the reviewed Go toolchain adapter
documented in `bashpp-p2-evaluator-decision.md`.

## Why Yaegi was retired

The initial `github.com/traefik/yaegi@v0.16.1` probe showed useful behavior:
simple standard-library calls, a GOPATH local package, blank and dot imports,
duplicate-import idempotence, and session variables all worked. Licensing was
not a problem: the module is Apache-2.0 and pure Go.

Independent probes of upstream `master` against the Go 1.27 target found
contract-level failures that a bounded adapter could not repair:

1. Module and workspace resolution was unsupported; non-standard imports were
   resolved from GOPATH source layouts.
2. Language and generated standard-library symbol coverage stopped at Go 1.22.
3. `EvalWithContext` could return while precompiled calls continued producing
   side effects, so cancellation was not sound.
4. Direct `os.Stdout` writes escaped adapter-local capture, defeating output
   atomicity.
5. There was no safe clone/reset operation for shell subsessions.

The cancellation and output failures were decisive: a declined call could
already have emitted output or continued running, and retrying it elsewhere
could execute user code twice. Yaegi was therefore removed completely. No
other in-process Go interpreter was adopted.

Two observed behaviors were not retirement reasons and must not be repeated as
such. Yaegi accepted imports of `package main`, but the capability policy
rejects those before any evaluator sees them. Its range-over-function panic was
outside the P2 selector-call subset and could have been converted to a decline.

## Closed production path

The replacement is not another Go interpreter. Bash++ source still runs
through the shell interpreter, whose package-private evaluator seam dispatches
approved package work to the pinned Go 1.27 toolchain. The policy refuses cgo,
compiled-only, package-main, no-buildable/platform-mismatch, missing,
unreviewed-standard-library, and unknown classes at import time.

Runner-level tests now cover the items formerly listed here as missing:
standard and local resolution (including workspace, vendor, and GOPATH), all
import forms, repeated interactive imports, init and namespace lifecycle,
live `set -o bashpp` initialization, forced-shell escape, atomic collision
rollback, cancellation and streams, subshell/session isolation, and exact
shell-runner versus direct-Go results.
