---
id: bf379147db1c
kind: bug
title: Linux bash53 execscript fixture times out while jobs now passes
status: closed
stage: code
priority: p0
refs:
    - ../bashy
reporter: qiangli
created: 2026-07-13T18:09:18.283401Z
weave: 9
closed: 2026-07-13T18:46:04.727386Z
resolution: fixed
closed_by: qiangli
---

Live GitHub Actions run 29267773644 on bashy c76cbfb: execscript is TIME at 60.049s. In the same run jobs is PASS at 62.096s, contradicting the prior handoff that named jobs as the timeout. Diagnose on Linux using the reusable bashy podman container, trace the stuck subprocess or cleanup path, and fix the shell engine without weakening or skipping the fixture. Required evidence: focused Linux execscript pass, sh Go tests, and downstream macOS make test-bash-parallel remains 86 of 86. Commit the change in the isolated sh workspace.

## Resolution

Root cause was the obsolete bash-5.3-rc2 fixture: its exec3.sub used a brace-expansion loop that final Bash 5.3 replaced with printf -v. bashy CI commit 55c7e0c pins final bash-5.3, removing the timeout trigger. Engine patch 3797bddb was explicitly rejected because raising the DoS cap was unnecessary and is not merged.
