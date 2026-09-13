---
id: 984c9550abe1
kind: task
title: 'Sprint 162.3: runtime/output/perf residue — the barrier-b/active-153 roots (105) incl. the deadline family (DESIGN: interpreter per-call cost, a number before code)'
seq: 93
status: todo
priority: p0
created: 2026-09-13T00:36:40.661549Z
assignee: qiangli
sprint: 162
---

Input: barrier-b/active-153-manifest.tsv re-measured by S162.0. Barrier B classes (interpreted): 17 'command ex time limit' (the fib-shaped family 22-256x over the 60 s bound; PERF.md: ~14.5 KB per interpreted call level, tree-walker cost — DESIGN-LEVEL, never a timeout raise), 14 'output should be empty when expected-output absent' (exact output / stray stderr), 6 'original callback retained' + 4 'asynchronous or retained original func' + 5 'unregistered bridge type' (bridge lifecycle, 153 S153.2/4 residue), 3 panic: N, 3 panic: FAIL (self-check panics), 2 'scalar call interrupted', 2 goroutine stack exceeds limit (peano/closure — same per-call cost), makeslice out of range 2, plus 7 compiled rows. Two tracks: (A) mechanisms — output barrier/stray output, bridge callbacks/types, self-check panics: reproducer + fix + leaf; (B) the per-call-cost DESIGN: a note with a measured profile of one fib-shaped root (where the 14.5 KB/level and the per-node cost go), the candidate designs (frame reuse / environment representation / call-path fast paths / a compiled fallback that is honestly declared), and the number each buys on the 17+2 roots — the manager records the decision before any code lands. Exit: track A roots PASS; track B has a recorded decision with a number. RULES: fixes are general Go mechanisms in sh (gosource / interp / lower), each from an OUTSIDE-CORPUS reproducer under the package testdata with the negative set; never keyed to a fixture path or expected string; never a permissive comparison; never a timeout raise. Verify: go test -count=1 on the affected packages, go test -short ./..., git diff --check (the 8 GoSource* interp tests fail on darwin before and after every change — judge by NEW failures only; never in a weave --verify), then the story leaf on sprint162-leaf (one coordinator; BASHPP_CORPUS_ROOTS=<manifest> tools/upstream-harness/corpus-gate.sh in a fresh /srv/sprint162/leaf-<name>; never the certification host, never the dev box) plus nearby native-PASS canaries. A row whose real first cause belongs to another owner moves by manifest commit, never in place. Commit trailers Sprint: #162 / Story: S162.<n> / Story-ID: <id> as the last paragraph.
