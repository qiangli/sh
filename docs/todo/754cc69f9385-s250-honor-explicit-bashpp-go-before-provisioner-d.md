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
