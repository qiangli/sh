---
id: f0bbecd2b95c
kind: bug
title: 'jobs fixture: bare ''wait'' hangs on Linux (cumulative job reaping)'
status: triaged
stage: code
reporter: qiangli
created: 2026-07-13T11:12:38.822896Z
weave: 6
---

The bash-5.3 'jobs' fixture TIMES OUT on Linux only (passes on macOS; the 60s cap is hit). Two kill-parsing hangs were already fixed (kill -sHUP/-n9 attached args, interp/builtin.go). The REMAINING hang: after the kill fixes, jobs.tests runs to the 'async list wait-for-background-pids' section then a bare 'wait' blocks forever. It is NOT the async list in isolation (sleep 1 & sleep 2 & wait completes fine). It is CUMULATIVE: jobs.tests line ~48 does 'sleep 20 &' then 'fg %1' (errors, no job control) and never kills that sleep; a later bare 'wait' (wait-for-all-children) appears to block on an orphaned/unreaped background process on Linux. TASK: trace jobs.tests job state step by step under a Linux build, find where a background child is not reaped (SIGCHLD/waitpid handling in interp on Linux differs from macOS), and fix so bare 'wait' returns. GATE: (1) macOS 'make test-bash-parallel' stays 86/86 (the moat — non-negotiable); (2) the jobs fixture passes on Linux. VERIFY on Linux: the steward has a reusable container repro at ~/binlinux/run.sh — 'bashy podman run --rm -v /Users/qiangli/projects/poc/dhnt:/Users/qiangli/projects/poc/dhnt -v /Users/qiangli/projects/poc/ai:/Users/qiangli/projects/poc/ai -v /Users/qiangli/binlinux:/out -e TESTS=jobs golang:1.26-bookworm /out/run.sh' (builds bin/bash for linux/arm64 against the umbrella sh, runs the jobs fixture non-root in a private tree). Since that recipe builds against the umbrella's live sh, coordinate with the steward for Linux verification of your patch, or adapt the recipe to mount your workspace sh as the build's ../sh. Keep changes brand-neutral. Do NOT claim fixed until BOTH gates pass.

---
**qiangli** · 2026-07-13T15:54:51Z

Run #6 (noteBgSignal + ignoreNextContinue in interp/builtin.go) did NOT fix it: macOS stayed 86/86 but Linux jobs still timed out — reverted by the steward. The hang is the orphaned 'sleep 20 &' (jobs.tests ~line 48, never killed after 'fg %1' errors) that a later bare 'wait' blocks on. Go deeper on SIGCHLD/waitpid reaping of ORPHANED background procs so a bare 'wait' returns on Linux. Full steward handoff brief + all session context: ~/.bashy/handoff/20260713T155157Z-e28ad4c7.json
