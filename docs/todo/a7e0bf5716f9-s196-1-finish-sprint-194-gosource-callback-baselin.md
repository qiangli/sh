---
id: a7e0bf5716f9
kind: task
title: S196.1 finish Sprint 194 GoSource callback baseline
seq: 129
status: done
priority: p0
created: 2026-09-15T18:28:20.824422Z
assignee: codex-gpt5.6-sol
sprint: 196
closed: 2026-09-16T10:29:19.507044Z
closed_by: codex-gpt5.6-sol
---

Reviewed 2026-09-16: implementation fixed by published commit f78d724626ffe5d026e00e1b870203ff6638ae26 (Sprint 197), inherited by current 7f58f691. The native pointer snapshot compares pointer identity rather than ephemeral transport handles; original and replacement-error regressions are retained. Fresh verification on 7f58f691: go test ./interp -run ^TestGoSourceUnwrapThreeModes$ -count=1 -timeout=10m PASS (8.681s). Close the duplicate repair work; do not merge diagnostic weave #231. The original complete bounded short-inventory and exact callback-ledger verification remains required and transfers to Sprint 196 integration story 94c8aa9a5955. Closure of this repair does not claim that broader gate passed.
