# Sprint 376 / Story 1502

Published baseline: 966a5453. Work on `s376-issue80188`.

1. Reproduce retained four-int objects with scaled copies of Go 1.27.1's
   issue80188, sampling host live heap at known live counts and recording time.
2. Remove scalar-only per-instance field layout maps. Enumerate struct fields
   from their payload when comparing Bash# values; Go comparisons retain their
   declaration order. Keep non-scalar metadata and copy/alias behavior intact.
3. Gate sparse layout allocation, equality, copies, field mutation, and relevant
   native/unsafe/task paths. Repeat measurements and capture heap profiles.
4. Assess remaining representation costs; verify the untouched acceptance root
   under its existing limit on a claimed memory-capped Linux host. Never run
   that full root on dragon. Report partial results explicitly if still open.

Parent plan: umbrella `docs/sprint-376-master-execution-plan.md`.
Sprint card: `sprint:qiangli/dragon/376`; story `ac47500351df`.

## Measured partial result

Sparse struct field metadata removes the per-instance scalar layout map while
preserving payload fields and Go comparison order. This repairs the comparison
regression that caused the Sprint 374 allocation reduction to be reverted.

On dragon with Go 1.27.1 and GOMAXPROCS=2, the scaled issue80188 live-heap
slope fell from 1,473.4 to 1,217.2 bytes/object (20,000 to 40,000 live objects).
At 40,000 to 80,000 objects, slopes were 1,474.9 and 1,218.9 bytes/object.
Three unprofiled 40,000-object runs had median interpreted elapsed times
1.805 s before and 1.814 s after: no throughput improvement claimed.

The allocation regression fails on the baseline (17 allocations, budget 12)
and passes after the change. Focused default/full-tag gates and a full-tag
race gate pass, including the prior equality regression and distinct-field
concurrency. Raw evidence lives in the separate story store's RESULT.md and
evidence directory.

This is not the complete indexed compact-value representation. Payload maps,
cells, pointers and repeated pointer metadata remain. No memory-capped Linux
host has been designated, and the unchanged full acceptance root has not run.
The story remains open; no full-root memory or time success is claimed.
