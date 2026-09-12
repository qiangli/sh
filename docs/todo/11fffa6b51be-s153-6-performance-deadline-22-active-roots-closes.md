---
id: 11fffa6b51be
kind: task
title: S153.6 performance deadline (22 active roots; closes on measurement per D3)
seq: 86
status: done
priority: p0
created: 2026-09-10T21:18:21.219445Z
sprint: 153
closed: 2026-09-12T19:30:42.981127Z
---

Active roots (bashpp-tests/docs/upstream-harness/leaf-153r0/active-153-manifest.tsv (run 0 on candidate 4, partition v7, commit 2fa9eb5). The seed manifest is a canary only. Harness boundary unchanged: exact Sprint 157 upstream Go harness, Go/Bash only, unchanged inputs through Bash++ in both modes, no fallback, no timeout change, no corpus-specific branch.): 21 interpreted 'command exceeded time limit' (64bit, abi/fibish, abi/fibish_closure, abi/uglyfib, atomicload, closure, deferfin, issue11256, issue16249, issue22781, issue24419, issue27695, issue5493, issue5963, issue79186, issue8039, issue67255, ken/divconst, ken/modconst, stack, stackobj2) + rangegen.go compiled. D3 (approved): spike P (run 146) measures each root — native time, interpreted time at three scales, extrapolated factor to the unchanged 60 s bound, slow-correct vs nonterminating. Nonterminating → S153.3/S153.4b as semantic defects. Slow-correct at ≤ ~3× → worked on the call path by the evaluator owner (2-day cap); larger factors (fib-shaped ≈ 22×) are recorded with the measurement and stay FAIL with a design note. No timeout extension, no work omission, no special casing.
