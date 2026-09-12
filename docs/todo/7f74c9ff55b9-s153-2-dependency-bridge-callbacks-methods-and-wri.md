---
id: 7f74c9ff55b9
kind: task
title: S153.2 dependency-bridge callbacks, methods and writers (28 active roots)
seq: 82
status: done
priority: p0
created: 2026-09-10T21:18:21.102117Z
sprint: 153
closed: 2026-09-12T19:30:42.886627Z
---

Active roots (bashpp-tests/docs/upstream-harness/leaf-153r0/active-153-manifest.tsv (run 0 on candidate 4, partition v7, commit 2fa9eb5). The seed manifest is a canary only. Harness boundary unchanged: exact Sprint 157 upstream Go harness, Go/Bash only, unchanged inputs through Bash++ in both modes, no fallback, no timeout change, no corpus-specific branch.): 23 'asynchronous or retained original function callbacks are unsupported' / 'original callback signature requires value-semantics parameters' / 'fmt.Fprintf requires a dependency-owned writer' rows and 5 'original method X is not supported by dependency transport' rows. The bridge (interp/bashpp_native_*.go, persistent Go child + JSON transport) must let native code call back into interpreted functions and methods on interpreted types, and accept an interpreted io.Writer, by MECHANISM CLASS (D4) — never by function name. Starts after S153.4's type registration lands (same owner, same seam). Positive control: sort.Slice with an interpreted less; negative: a callback retained past the call returns a refusal, never a hang. Verify as S153.4.
