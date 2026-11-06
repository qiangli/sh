---
id: fc03f80cf2ba
kind: task
title: Reuse validated foreign preparation across lower inference passes
seq: 172
status: assigned
created: 2026-11-05T07:19:05.366983Z
weave: 28
assignee: s562-cpp-prepare
sprint: 578
sprint_id: 70ba48be-af89-5ab1-8a8d-42a15fc1ddbe
sprint_title: 'S562 follow-up: remaining fence corpus work'
---

Exact and-L27 compiled diagnostic retains two native Prepare cycles in one lower.Compile: compilePass prepares foreign plans at entry then calls compilePass again after global type inference (compile.go669; library.go110). Native build1.707s, interpretedPASS, transpile19.084s (AST5.064+4.406s, nativecompile0.882+0.518), compiled20s deadline. Isolate current d079711c. Design-first bounded per-compilation immutable prepared plan reuse across inference passes, not global/disk cache. Preserve source/provider/environment identity, diagnostics, cancellation and custom runner behavior. Exact and/bitand unchanged; invalid-source/provider drift/cancellation controls and existing lower coverage. Parent executes remote gates, no deadline increase, corpus exclusions or floor edits. Manager codex-gpt6-sol instance95835ea8-80f4-42ea-8edb-9b05dd29d655. Sprint562.

Carried from S562 (operator-approved early close 2026-11-06); deferral is not PASS. Run/branch/evidence references: weave run 28, d079711c, 9b05dd29d655.
