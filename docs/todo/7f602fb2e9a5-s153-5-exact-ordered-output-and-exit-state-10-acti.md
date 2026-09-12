---
id: 7f602fb2e9a5
kind: task
title: S153.5 exact ordered output and exit state (10 active roots)
seq: 85
status: done
priority: p0
created: 2026-09-10T21:18:21.184578Z
sprint: 153
closed: 2026-09-12T19:30:42.956886Z
---

Active roots (bashpp-tests/docs/upstream-harness/leaf-153r0/active-153-manifest.tsv (run 0 on candidate 4, partition v7, commit 2fa9eb5). The seed manifest is a canary only. Harness boundary unchanged: exact Sprint 157 upstream Go harness, Go/Bash only, unchanged inputs through Bash++ in both modes, no fallback, no timeout change, no corpus-specific branch.): bug352, issue12577, issue14646, issue73917, issue73920, issue8047b (interpreted 'output should be empty … Instead saw'), issue21879, issue7690, inline_callers, uintptrescapes3 (compiled), maymorestack ('65 != 128'), issue4620, plus issue19467/issue20014 shared with S153.1. Run 147 (merged 1ab52115) found the ordering defect is in the BRIDGE: a native request replies before the child's stdout/stderr drains (reproducer interp/testdata/sprint153/output/interleaving/, test TestGoSourceOrderedOutputInterleaving skips until the barrier lands) — owned by S153.4a; the evaluator-caused output rows are owned by S153.4b; this card closes when the leaf's output rows are byte-exact in both modes twice. Matching policy stays with Sprint 154.
