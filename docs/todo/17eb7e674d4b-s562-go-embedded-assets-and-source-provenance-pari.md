---
id: 17eb7e674d4b
kind: bug
title: S562 Go embedded assets and source provenance parity
seq: 169
status: done
priority: p0
created: 2026-11-05T05:54:51.331773Z
weave: 25
assignee: s562-go-assets
sprint: 562
sprint_id: 2e8945a5-d199-539c-aaea-321a738ab9df
sprint_title: 'Fence corpora: complete required coverage and repair current failures'
closed: 2026-11-05T06:42:05.249475Z
closed_by: codex-gpt6-sol
---

Manager: codex-gpt6-sol instance95835ea8; delegated supervisor regression_prep. Isolated sh base73f532c527e452c68f0c80bed367b93f672070e9. Eight points/max30m. Repair exact Go build/source-provenance defects from fenced357 gate: embed-directive both modes cannot find folder/*.hash although all three original assets are declared and copied; logging both modes emit fence.go:40 rather than authenticated logging.bsh:40. AnalyzeArtifact currently builds virtual .bashpp-overlay-<source-length>/fence.go with only fence+worker overlay mappings. Design bounded declared asset/source-origin binding; preserve embed relative hierarchy/glob semantics and original authored line positions. No arbitrary recursive copying, broad filename normalization, ambient access, dependency pin bump or source/comparator rewrite. No undeclared traversal/symlink escape; deleted-source compiled artifacts must retain exactly needed embedded bytes. Prefer separate helper files, own Go AnalyzeArtifact/build overlay section and provenance helper/tests only. Parallel command-lifecycle owner owns callGo/goWorkerSource and interp/lower invocation context; coordinate go.go hunk boundary before edits. Propose source-origin API before costly changes if ambiguous. Remote parent executes focused exact embed/logging run+compiled red/green and unchanged Go alias/scalar/module import regressions, negative missing/tampered/path-escape assets. Worker prepares bounded gates, no full/heavy dev suite or remote execution. Commit named files with DCO and Sprint/Story/Story-ID; preserve raw private receipts and public scrub. No claim of corpus completion from submission.
