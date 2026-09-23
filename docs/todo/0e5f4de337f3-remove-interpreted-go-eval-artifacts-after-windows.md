---
id: 0e5f4de337f3
kind: bug
title: Remove interpreted Go eval artifacts after Windows console cancellation
seq: 150
status: done
priority: p0
labels:
    - windows
    - go-by-example
created: 2026-09-23T18:34:01.336639Z
assignee: codex-s250
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
closed: 2026-09-23T20:26:57.017351Z
closed_by: codex-s250
---

Final Windows Go by Example 255 gate (Bashy f93816e/sh69eda1b3, tests 6d293a6, manifest f5795676) has two otherwise matching server rows: examples/http-server/http-server.go and examples/tcp-server/tcp-server.go oracle/compiled pass with empty effects, but interpreted records fail_effects. CTRL_BREAK_EVENT ends each server with exit 3221225786; interpreted leaves TMPDIR/.bashpp-eval-*/bashpp-session-*.go.bin plus its directory in the observed run effects. Retained evidence C:\Users\noviadmin\s250-327-accept\evidence\gbe-final-255.jsonl.fail, SHA-256 776266c4c5b5bc07a53084a4f1c9a2eba24225176aa2e6b306a4f3ac5abf7b70, rows 031 and 068. Diagnose temporary artifact lifetime and Windows console cancellation; fix without excluding effects, changing original source, adapter, or deadline. Acceptance: both exact rows byte/effect-match native oracle and compiled with no surviving children, then full 255 gate. Coordinate with sh #149 because signal handling may be shared.

Focused private candidate sh 0ec8342e forwards the console break and removes the scratch effects; `signals.go` becomes green. Both server rows then expose a second, exact Windows exit propagation defect: the native helper exits `0xC000013A` (3221225786) as do oracle/compiled, but `bashPPNativeExitStatus` rejects statuses above 255 and prints `gosource: program exited with status 3221225786`, causing interpreted exit 1. Acceptance additionally requires the interpreted server process to report the same full 32-bit Windows exit with no diagnostic stderr after bridge output and scratch cleanup. Keep this case limited to a forwarded Windows console break; ordinary shell exit codes and Unix signal behavior retain their current paths.

Final acceptance, 2026-09-23: focused unchanged Windows `http-server.go` and `tcp-server.go` rows passed all three modes with no interpreted eval artifacts in the compared effects. The authenticated Bashy `e48bd7df29a496692f22ebb72520f7f32c932697` / sh `0736c52ec82d39922359548f0496e44ec583c6ba` full Go by Example gate passed **255/255** with independent validator PASS. Gate-time bashsharp-tests `eda9a22856288ebc239cb177c2d206cf69d51da4`; semantic root `6867eacc716b234cd30f830d9ae01b93c7e9ea3c1b8b55d3a06aaa9ce36e154e`; ledger SHA-256 `7614c6adb597b2cb2a5024bc88ce5e00f158e8a18ede198584ab7574c872148f`. Original sources, effects comparison and limits were unchanged.
