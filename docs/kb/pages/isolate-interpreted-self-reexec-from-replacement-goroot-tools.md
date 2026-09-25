---
id: 01a0da29-3775-7791-8e16-c097a66fa557
seq: 18
form: note
type: lesson
title: Isolate interpreted self-reexec from replacement GOROOT tools
description: When an interpreted Go test replaces a GOROOT tool, keep program GOROOT semantics but run replay/front-end/helper tool work from an independent authenticated SDK.
status: candidate
source:
    tool: codex-gpt5.6-sol-n
    host: dragon
    episode: aa5c046bb543
created: "2026-09-25T20:02:09Z"
updated: "2026-09-25T20:02:23Z"
---

When an interpreted Go test replaces a tool inside its GOROOT and then reexecs os.Executable, preserve that GOROOT as program state but pin the replay launcher's private BASHPP_GO selector and all sh-owned go list/build/helper operations to a same-version independently authenticated SDK tree. Otherwise the replayed front end or dependency helper invokes the replacement compiler, which reenters the launcher and grows a Bashsharp/go/compile process tree until OOM.
