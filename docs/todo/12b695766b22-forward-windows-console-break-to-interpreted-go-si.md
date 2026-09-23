---
id: 12b695766b22
kind: bug
title: Forward Windows console break to interpreted Go signal handler
seq: 149
status: done
priority: p0
labels:
    - windows
    - go-by-example
created: 2026-09-23T18:33:48.784711Z
assignee: codex-s250
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
closed: 2026-09-23T20:26:56.743825Z
closed_by: codex-s250
---

Final public-head Windows Go by Example 255 gate (Bashy f93816e/sh69eda1b3, tests 6d293a6, candidate manifest f5795676) executes all 255. examples/signals/signals.go oracle and compiled print awaiting signal, interrupt signal received, exiting and exit 0; interpreted prints only awaiting signal then exits 3221225786 after the unchanged readiness-triggered CTRL_BREAK_EVENT. Raw evidence C:\Users\noviadmin\s250-327-accept\evidence\gbe-final-255.jsonl.fail, SHA-256 776266c4c5b5bc07a53084a4f1c9a2eba24225176aa2e6b306a4f3ac5abf7b70. Diagnose Windows console signal delivery/forwarding in the Go-source interpreter. Acceptance: unchanged original source, signal injector and deadlines; interpreted transcript/exit/effects match oracle and compiled in focused exact row, then full 255 gate. Keep context adapter race separate (tests #95).

Final acceptance, 2026-09-23: the focused unchanged Windows `signals.go` row passed oracle/interpreted/compiled, and the authenticated Bashy `e48bd7df29a496692f22ebb72520f7f32c932697` / sh `0736c52ec82d39922359548f0496e44ec583c6ba` full Go by Example gate passed **255/255** with independent validator PASS. Gate-time bashsharp-tests `eda9a22856288ebc239cb177c2d206cf69d51da4`; semantic root `6867eacc716b234cd30f830d9ae01b93c7e9ea3c1b8b55d3a06aaa9ce36e154e`; ledger SHA-256 `7614c6adb597b2cb2a5024bc88ce5e00f158e8a18ede198584ab7574c872148f`. Original source, signal injector and limits were unchanged.
