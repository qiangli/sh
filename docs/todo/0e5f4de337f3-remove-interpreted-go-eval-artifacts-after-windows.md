---
id: 0e5f4de337f3
kind: bug
title: Remove interpreted Go eval artifacts after Windows console cancellation
seq: 150
status: todo
priority: p0
labels:
    - windows
    - go-by-example
created: 2026-09-23T18:34:01.336639Z
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
---

Final Windows Go by Example 255 gate (Bashy f93816e/sh69eda1b3, tests 6d293a6, manifest f5795676) has two otherwise matching server rows: examples/http-server/http-server.go and examples/tcp-server/tcp-server.go oracle/compiled pass with empty effects, but interpreted records fail_effects. CTRL_BREAK_EVENT ends each server with exit 3221225786; interpreted leaves TMPDIR/.bashpp-eval-*/bashpp-session-*.go.bin plus its directory in the observed run effects. Retained evidence C:\Users\noviadmin\s250-327-accept\evidence\gbe-final-255.jsonl.fail, SHA-256 776266c4c5b5bc07a53084a4f1c9a2eba24225176aa2e6b306a4f3ac5abf7b70, rows 031 and 068. Diagnose temporary artifact lifetime and Windows console cancellation; fix without excluding effects, changing original source, adapter, or deadline. Acceptance: both exact rows byte/effect-match native oracle and compiled with no surviving children, then full 255 gate. Coordinate with sh #149 because signal handling may be shared.
