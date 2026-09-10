---
id: 9b0c597132e5
kind: task
title: 'Sprint 118: execute range-function iterator callbacks'
seq: 62
status: done
priority: p0
created: 2026-09-10T02:11:41.793459Z
weave: 112
assignee: qiangli
sprint: 118
closed: 2026-09-10T03:42:01.522671Z
---

Post-Story-61 exact repro on canonical sh 4daa2f7d and freshly built bashy 42d2427: unchanged examples/range-over-iterators/range-over-iterators.go prints 0 through 12, then exits 1 with 'gosource: original callback signature requires scalar parameters and supported results'. Oracle and compiled passed in Candidate024; selector assignment is already repaired. Diagnose and implement the smallest general GoSource/interpreter correction for Go 1.23+ range-over-function iterator callbacks, preserving the original callback body and early-stop bool result. Do not special-case this fixture, edit corpus/evidence/normalizers, forward the whole program to native Go, overlap unrelated generic/selector logic, or claim full Go-by-Example parity. Add negative-first focused tests covering callback parameter/result transport, early termination, and rejection of unsupported callback shapes; keep Classic/Bash++ behavior unchanged. Run focused tests and env PATH=/Users/qiangli/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.0.darwin-arm64/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin GOTOOLCHAIN=local GOMAXPROCS=2 GOFLAGS=-p=2 go test ./gosource ./lower plus the exact unchanged fixture. Commit with Sprint: #118 and this Story/Story-ID trailers. No push, merge, closure, subagents, or broad corpus replay. Parent Story-ID fa07603b71dc.
