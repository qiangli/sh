---
id: e58cccba74f8
kind: task
title: S153.4 interpreted runtime closure — 4a bridge values (54) + 4b evaluator semantics (25)
seq: 84
status: done
priority: p0
created: 2026-09-10T21:18:21.154283Z
sprint: 153
closed: 2026-09-12T19:30:42.933384Z
---

Split per the plan (D2), one ID, two owners. 4a (bridge owner, interp/bashpp_native_*.go): worker-template build failures (10: abi/idata, blank, chan/powser2, bug517, issue11286, issue20780b, issue45851, issue71932, sizeof, typeparam/struct), unregistered bridge types (16: instantiated generics, arrays, embedded fields, linked-package types), native slice retention by class (28: read-only consumer / in-place mutator / retained-by-native). 4b (evaluator owner, interp/bashpp_{eval,scalar,func,funcvalue,pointer,struct,interface,collection*,range,update,scope}.go): 25 roots — assignment evaluation order, method expression from multi-result call, package-private interface identity, scalar call interrupted (4), function values dispatched as shell commands (issue73917/73920/8047b), zero-size value identity (bug352), negative zero (issue12577), the triage groups G3/G4/G5 (interp/testdata/sprint153/triage/FINDINGS.md). Active roots: bashpp-tests/docs/upstream-harness/leaf-153r0/active-153-manifest.tsv (run 0 on candidate 4, partition v7, commit 2fa9eb5). The seed manifest is a canary only. Harness boundary unchanged: exact Sprint 157 upstream Go harness, Go/Bash only, unchanged inputs through Bash++ in both modes, no fallback, no timeout change, no corpus-specific branch.. One mechanism per commit from an outside-corpus reproducer under interp/testdata/sprint153/. Verify: reproducer, 3–20-root subset, full leaf twice, go test -count=1 -timeout 30m ./interp/..., go test -short ./..., leak checks, 151 + PASS canaries.
