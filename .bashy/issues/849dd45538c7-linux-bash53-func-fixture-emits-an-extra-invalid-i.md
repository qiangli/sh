---
id: 849dd45538c7
kind: bug
title: Linux bash53 func fixture emits an extra invalid-identifier diagnostic
status: triaged
stage: code
priority: p0
refs:
    - ../bashy
reporter: qiangli
created: 2026-07-13T18:09:29.550911Z
---

Live GitHub Actions run 29267773644 on bashy c76cbfb: func fails at line 187. Expected output is the literal token from func5.sub, but bashy additionally prefixes a line-numbered not-a-valid-identifier diagnostic. Reproduce the focused fixture on Linux, compare against GNU Bash 5.3, identify the semantic or diagnostic gating error, and add focused Go coverage. Do not suppress diagnostics broadly. Required evidence: focused Linux func pass, sh Go tests, and downstream macOS make test-bash-parallel remains 86 of 86. Commit the change in the isolated sh workspace.
