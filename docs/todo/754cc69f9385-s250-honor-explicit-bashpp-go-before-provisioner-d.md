---
id: 754cc69f9385
kind: bug
title: S250 honor explicit BASHPP_GO before provisioner discovery
seq: 148
status: doing
priority: p0
labels:
    - windows
    - go-by-example
    - go-sdk
created: 2026-09-23T17:35:33.31081Z
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
---

Windows Go by Example arrays compiled transpile enters sh lower.goSDKCandidates with an explicit, authenticated BASHPP_GO absolute SDK binary. It adds that candidate, then still invokes polyglot.ToolResolver("go"), which calls Bashy gotoolchain.Ensure and unpacks the managed Go ZIP. A diagnostic goroutine stack at 15s shows binmgr.extractTreeZip blocked in Windows CreateFile; unchanged 60s arrays transpile times out. If the valid explicit BASHPP_GO candidate is present, return it before resolver discovery. Preserve fallback behavior when the injected path is absent/invalid, SDK version validation, original fixture, and deadlines. Focused sh tests plus exact Windows arrays gate, then full Go by Example after final candidate.

Focused Windows control on noviwin1: isolated patched Bashy SHA-256 `e1deb7be579da9223d7b672f83e8454213908918d4c09b742fe4064db032abdb` transpiled unchanged arrays source SHA-256 `9b23202e95c943d209c6a60c61f2581c3b77137164f776fcfab2d5fe28f411cc` with SYSTEMROOT present in 12.374s, exit 0, generated Go and map present. Raw receipt: `C:\Users\noviadmin\s250-327\diag\stack-systemroot-patched\gate\result.json`. Matched unpatched diagnostic reached the original 60s timeout; its 15s goroutine stack at `C:\Users\noviadmin\s250-327\diag\goroutines-systemroot.txt` names `goSDKCandidates → islandToolResolver → gotoolchain.Ensure → binmgr.extractTreeZip → syscall.CreateFile`. An authenticated final-head Go by Example row/full ledger remains pending; keep this story doing until that evidence lands.
