---
id: 758fc8974a30
kind: task
title: S153.1 compiled-mode runtime closure (7 active roots)
seq: 81
status: doing
priority: p0
created: 2026-09-10T21:18:21.072692Z
sprint: 153
---

Active roots (compiled rows of bashpp-tests/docs/upstream-harness/leaf-153r0/active-153-manifest.tsv (run 0 on candidate 4, partition v7, commit 2fa9eb5). The seed manifest is a canary only. Harness boundary unchanged: exact Sprint 157 upstream Go harness, Go/Bash only, unchanged inputs through Bash++ in both modes, no fallback, no timeout change, no corpus-specific branch.): fixedbugs/bug367.go and issue52856.go and issue42401.go (both modes fail — the lowered program repeats the interpreted defect), issue29919.go ('panic: missing a.init' — a linked package's init is not run), issue19467.go (runtime.Caller frames report the mangled linked-package name __gosource_pkg_0_… instead of the original path), issue20014.go (runindir output mismatch), rangegen.go (compiled run exceeds 60 s). Reduce each to an outside-corpus program; fix only in lower/ RUNTIME HELPERS (__bpp_rt) or interp/; an emitter/link defect is recorded and moved to 152 by manifest commit, never edited here. Verify: reproducer, both modes twice, exact output/exit, go test -count=1 -timeout 30m on affected packages, go test -short ./..., PASS canaries.
