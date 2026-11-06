---
id: 0f4ea3117c92
kind: bug
title: S562 Go command invocation context and lifecycle parity
seq: 168
status: todo
priority: p0
created: 2026-11-05T05:54:36.530734Z
assignee: s562-go-context
sprint: 578
sprint_id: 70ba48be-af89-5ab1-8a8d-42a15fc1ddbe
sprint_title: 'S562 follow-up: remaining fence corpus work'
---

Manager: codex-gpt6-sol instance95835ea8; delegated supervisor regression_prep. Isolated sh base73f532c527e452c68f0c80bed367b93f672070e9. Eight points, max30m. Design first: explicit command-mode invocation boundary for exported Go fence calls, with caller argv/stdin/cwd/environment and process output/status semantics; do not infer command semantics for typed/value calls or alter existing typed-call contract. Actual Go corpus failures: command-line-arguments/flags/subcommands, line-filters, exit, panic, testing-and-benchmarking; signals additionally needs readiness streaming. callGo currently launches artifact with no argv/cwd/env, consumes stdin for JSON, buffers stdout, treats clean os.Exit0 without result as protocol error, and generated recover changes native panic to generic foreign error. Propose exact API/call-site selection before costly implementation; manager reviews ambiguity. Keep protocol/control transport separate from guest stdin; distinguish os.Exit0/3, panic2, cancellation and malformed/forged result frames; preserve typed exported function result/error behavior. Own callGo/goWorkerSource and invocation wiring interp/lower plus new focused tests; do NOT change Go AnalyzeArtifact build/overlay assets (parallel owner). Coordinate shared go.go hunks before edits. No corpus/comparator/golden changes or pin bumps. Remote parent runs red/green against exact corpus rows and typed regression, cancellation/status/stdout/stderr/cleanup; worker prepares source and bounded gate scripts, no full/heavy dev suites or remote execution. Commit named files with DCO and Sprint/Story/Story-ID trailers. Known traps: same binary must be authentic single product; old original Go gates remain291/255green; foreign wrapper stderr is not native guest stderr. Read existing product semantics before choosing command boundary. No acceptance claims from worker submission.

Carried from S562 (operator-approved early close 2026-11-06); deferral is not PASS. Run/branch/evidence references: weave run 24.
