---
id: 01a0da63-587a-702e-aa05-86953e6968c8
seq: 20
form: page
type: lesson
title: Materialize reassigned native slice values before range
description: When a Bash++/GoSource variable already has a declared, non-native Go type (e.g. []string) and is reassigned via plain = from a dependency call result (strings.Split), the reuse path must materialize the value into interpreter-owned storage with a properly kinded collection meta, not just an assignability check. A fresh := declaration and struct-field assignment already materialize; only the reused-target = path skipped it, so range (and any other collection consumer keyed on meta.kind) silently walked zero elements while len() still read correctly through a different path.
tags:
    - gosource
    - interp
    - range
    - native-bridge
    - collection-meta
status: candidate
evidence: 'cmd/compile/internal/inline/inlheur TestDumpCallSiteScoreDump (Go 1.27.1 GOROOT) reproduced interpreted: var lines []string; lines = strings.Split(...); for _, line := range lines {...} counted 0 for every bucket. Fixed in bashPPValidateReusedShortValue (interp/bashpp_p1.go) and bashPPRangeCollection (interp/bashpp_range.go); regression: interp/gosource_s281_native_reassign_range_test.go (go test -tags full ./interp -run TestGoSourceS281NativeReassign)'
source:
    tool: claude-sonnet5-q
    host: dragon
    episode: weave-issue-17
created: "2026-09-25T21:05:38Z"
---
