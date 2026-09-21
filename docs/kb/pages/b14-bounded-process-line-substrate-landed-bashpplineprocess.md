---
id: 01a0c27a-29a2-7dcf-880f-372bf2715255
seq: 3
form: note
type: lesson
title: B14 bounded process line substrate landed (bashPPLineProcess)
description: ""
status: candidate
source:
    tool: claude-opus4.8-m
    host: dragon
    episode: weave-issue-13
created: "2026-09-21T05:39:41Z"
---

S221.4 (Story-ID 0d0787f57ca1, commit on agent/weave-issue-13) added interp/bashpp_process.go: bashPPLineProcess, the one internal bounded Go-channel process line substrate B5/B7 must consume. API: bashPPStartLineProcess(ctx, bashPPProcessSource, buffer) -> handle with Lines() <-chan string (producer owns close), Wait() (int,error) exact-once (status is bash 128+N on signal; err only for cancel/scan), Close(), Stderr(). Real os/exec via bashPPStartCmd/bashPPCmdSource (kill/reap serialized against pid reuse). Status split: bashPPProcessExitStatus in bashpp_native_process_{unix,other}.go. Tests race-clean; provenance = go1.27.1 os/exec BSD-3-Clause + Bash 5.3 exit-status rule. RESIDUAL: the run(...).Lines()/start(...) dialect SURFACE spellings are not yet wired (needs bashppParenForm call-chaining tail + CalleeExpr method dispatch); see plan-story575-process-line-substrate.md.
