---
id: 2daf9ef04ad4
kind: task
title: Build Go example corpus as Bash++ differential tests
seq: 1
status: assigned
priority: p1
created: 2026-08-07T04:54:28.411321Z
weave: 31
assignee: qiangli
---

Create a durable Bash++ language-coverage corpus derived safely from the official Go Tour and Go by Example. First inventory what Bash++ syntax/runtime is actually implemented versus design-only; do not turn roadmap syntax into passing claims. Pin the exact upstream repository revision, path, and example identifier used by every fixture. The Go Tour repository is BSD-3-Clause; retain its required notices. Go by Example is licensed CC BY 3.0: adaptations are permitted with attribution, identification of material changes, a license link, and a third-party notice; preserve the separate Renée French attribution for Gopher artwork when applicable. Prefer independently authored cases when verbatim adaptation adds no test value. Define a manifest mapping each example to source/topic, Bash++ phase/feature, expected stdout/stderr/status, determinism rules, provenance, and status supported/planned/not-applicable. Build a hermetic differential runner that executes the Go oracle and Bash++ form, compares normalized observable behavior, rejects 0/0 denominators, and has no network dependency in CI. Start with a small deterministic tranche covering values, variables, constants, loops, conditionals, functions, multiple returns/error bridging, arrays/slices/maps where implemented; defer filesystem/network/time/random/concurrency until fixtures and semantics are controlled. Preserve Bash superset mode-off/on gates. Add documentation, attribution, focused tests, full relevant Go tests, and race tests. The concurrency tranche is blocked on the dedicated Bash++ data-race gate task: it must use race-safe observers and pass repeated `go test -race` coverage rather than treating ordinary functional success as proof. Commit locally for conductor review; do not push.
