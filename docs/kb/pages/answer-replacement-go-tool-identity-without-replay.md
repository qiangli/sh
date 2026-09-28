---
id: 01a0e7a9-5471-7177-811d-1dfac3cafd2f
seq: 27
form: page
type: lesson
title: Answer replacement Go tool identity without replay
description: When a native launcher represents an interpreted Go tool, answer -V=full directly from a stable authenticated launcher identity; never enter the interpreted replay plan, because its startup may invoke cmd/go and probe the same replacement tool.
status: candidate
evidence: 'Sprint 319 Story 1083: Linux R1 process tree showed compile -V=full -> Bash# replay -> authenticated go list -> same compile -V=full; focused baseline-red reduction and committed reexec gate at eb30b508.'
source:
    tool: codex-gpt5.6-sol-n
    host: dragon
    episode: weave-issue-222
created: "2026-09-28T10:57:09Z"
supersedes: isolate-interpreted-self-reexec-from-replacement-goroot-tools
---

A cross-process lock or successful-output cache cannot break this cycle: the first probe owns the lock and enters the replay plan, while package discovery below that replay invokes the same replacement tool and waits on or recursively recreates the identity path. The launcher itself must emit a Go-compatible development version line with a buildID derived from an authenticated per-replay identity, invoked tool basename, GOOS, GOARCH, and GOEXPERIMENT. Keep the identity stable for every copy/probe of one launcher and distinct across replay sessions or behavior-changing configurations. Ordinary executions still replay and forward signals; the -V=full path must be childless.
