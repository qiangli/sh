---
id: 12b695766b22
kind: bug
title: Forward Windows console break to interpreted Go signal handler
seq: 149
status: todo
priority: p0
labels:
    - windows
    - go-by-example
created: 2026-09-23T18:33:48.784711Z
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
---

Final public-head Windows Go by Example 255 gate (Bashy f93816e/sh69eda1b3, tests 6d293a6, candidate manifest f5795676) executes all 255. examples/signals/signals.go oracle and compiled print awaiting signal, interrupt signal received, exiting and exit 0; interpreted prints only awaiting signal then exits 3221225786 after the unchanged readiness-triggered CTRL_BREAK_EVENT. Raw evidence C:\Users\noviadmin\s250-327-accept\evidence\gbe-final-255.jsonl.fail, SHA-256 776266c4c5b5bc07a53084a4f1c9a2eba24225176aa2e6b306a4f3ac5abf7b70. Diagnose Windows console signal delivery/forwarding in the Go-source interpreter. Acceptance: unchanged original source, signal injector and deadlines; interpreted transcript/exit/effects match oracle and compiled in focused exact row, then full 255 gate. Keep context adapter race separate (tests #95).
