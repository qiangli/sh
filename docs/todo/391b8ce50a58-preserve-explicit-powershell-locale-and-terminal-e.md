---
id: 391b8ce50a58
kind: bug
title: Preserve explicit PowerShell locale and terminal environment across fence modes
seq: 166
status: done
priority: p1
created: 2026-11-05T05:29:09.691721Z
weave: 22
assignee: s562-ps-locale
sprint: 562
sprint_id: 2e8945a5-d199-539c-aaea-321a738ab9df
sprint_title: 'Fence corpora: complete required coverage and repair current failures'
closed: 2026-11-05T08:04:23.151232Z
closed_by: codex-gpt6-sol
---

Actual Sprint562 PS subset proves run and compiled date output differs from native en_US oracle because powerShellLaunchEnvironment drops LANG/LC_ALL/TERM. Preserve explicit admitted locale and TERM inputs in immutable environment plan/fingerprint and compiled execution; no golden normalization or ambient broad forwarding. Exact affected arrays L564 and 16 recipe cases, Linux and Windows relevant regression. Stage bounded native/interpreted/compiled evidence for manager acceptance. Based on combined f1 or newer verified lifecycle candidate; coordinate environment.go with CSharp assembly owner.
