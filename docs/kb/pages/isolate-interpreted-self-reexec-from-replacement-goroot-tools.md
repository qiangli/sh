---
id: 01a0da29-3775-7791-8e16-c097a66fa557
seq: 18
form: note
type: lesson
title: Isolate interpreted self-reexec from replacement GOROOT tools
description: When an interpreted Go test replaces a GOROOT tool, isolate helper builds and single-flight stable tool identity probes so self-reexec cannot fan out into one interpreter per go command.
status: candidate
evidence: Sprint 281 Story 811 focused regression launches 16 concurrent -V=full probes plus a distinct GOEXPERIMENT probe; all callers receive the replay result while the plan executes once per configuration.
source:
    tool: codex-gpt5.6-sol-n
    host: dragon
    episode: aa5c046bb543
created: "2026-09-25T20:02:09Z"
updated: "2026-09-25T22:25:05Z"
---

When an interpreted Go test replaces a tool inside its GOROOT and then reexecs os.Executable, preserve that GOROOT as program state but pin the replay launcher's private BASHPP_GO selector and all sh-owned go list/build/helper operations to a same-version independently authenticated SDK tree. Otherwise the replayed front end or dependency helper invokes the replacement compiler, which reenters the launcher and grows a Bashsharp/go/compile process tree until OOM.

Isolation alone does not bound startup fanout: every independent go command runs its replacement compiler with -V=full to obtain a tool ID, and cmd/go caches that answer only within its own process. The replay launcher should single-flight concurrent probes across its processes and atomically cache only successful stdout. Scope the cache by launcher basename plus GOOS, GOARCH, and GOEXPERIMENT, because those inputs can change a Go tool's version response; never cache a failed probe or substitute a native compiler response.
