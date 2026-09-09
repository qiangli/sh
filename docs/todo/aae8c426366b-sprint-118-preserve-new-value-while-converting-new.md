---
id: aae8c426366b
kind: task
title: 'Sprint 118: preserve new(value) while converting new(T) short declarations'
seq: 57
status: done
priority: p0
created: 2026-09-09T21:39:43.800205Z
weave: 103
assignee: qiangli
sprint: 118
closed: 2026-09-09T22:35:22.668829Z
---

Candidate022 exact regression: unchanged examples/pointers/pointers.go line 45 p := new(42) no longer transpiles (gosource: unsupported type *ast.BasicLit), while new(Vertex) must retain the Candidate022 fix. Root cause is sh 47cce082 short-decl new branch in gosource/convert.go treating every argument as a type. Implement the smallest sound guard using type info so only new(type) takes the type path and new(value) falls through to the prior generic call path. Scope only gosource/convert.go and focused gosource tests; do not touch interp/native/lower/harness/frozen candidates. Reproduce both new(42) and new(int)/new(Vertex), run GOMAXPROCS=2 GOFLAGS=-p=2 go test ./gosource ./lower, preserve Classic/Bash++. Read CLAUDE.md. No subagents, push, merge, story closure, or corpus claims. Commit named files with Sprint: #118, Story and Story-ID trailers. Parent Story-ID 6f0c4d9a31be.

Run 100 preserved the reviewed implementation but timed out, then broadened scope during resume and was stopped. Replacement run 101 must import only commit 3866fe9e8503c19985fb501943c69a319f4183d9, correct test traceability, and exclude zz_repro_test.go. Manager independently passed `go test ./gosource ./lower` on the preserved implementation (22.731s / 408.611s).

Run 101's Codex wrapper remained idle without modifying the clean workspace and was stopped. Replacement run 102 carries the same exact two-file integration scope.

Run 102 produced the correct two-file bytes at 88a777053f7a542cc3e64b385f706fd71f96ce3e, but its wrapper used the host Go 1.26.0 rather than the pinned Go 1.27 path and its commit message collapsed the required trailers onto one line. Replacement run 103 imports those bytes without that commit metadata and uses the pinned verifier.

Closed after Candidate023 authenticated sh 70ec295a837dbe9feb6dde193d5517c95032fda0 and the complete 255-attempt replay restored compiled parity to 85/85 with zero missing observations. Pinned Go 1.27 gosource and full gosource/lower manager gates passed before publication.
