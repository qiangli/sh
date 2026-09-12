---
id: 828995f7380b
kind: task
title: S151.1 collection and builtin type-evaluation closure
seq: 72
status: done
priority: p0
created: 2026-09-10T21:18:20.821116Z
sprint: 151
closed: 2026-09-12T04:06:54.882671Z
---

Status: non-dispatchable seed/localization card until Barrier A. Packet 151.1 has 92 baseline roots; manifest SHA-256 `3fb0d60210b19b8b4fa08c3374d8b4e7f85fb56cb311458f4e17bd6889333196`, root-list SHA-256 `3e07142162d33e28b09df6f3cb686e466255685565abbe324b20136b8a5dafe0`. Evidence is `/srv/sprint142/evidence/product-baseline-001-control/{packet-manifests-v4/packet-151.1.json,final-causal-v2.json}`. Representatives `testdir:235.go` and `testdir:abi/map.go` were native PASS but interpreted `run` exit 2 under `type_evaluation.interpreted_rejection`.

Before Barrier A, only authenticate the seed and reduce a representative to an outside-corpus positive/negative reproducer. After the manager binds a versioned active manifest, this card owns that manifest's collection/builtin semantic outcomes, not prescribed filenames. Record the localized implementation boundary before editing; only the named Static Type/Bridge Model Owner may integrate a shared checker/evaluator seam. Verify the minimal repro, a 3–20-root authenticated subset, the full active leaf, `go test -count=1` for affected packages, `go test -short ./...`, and adjacent PASS canaries. Acceptance requires every active root to pass all required interpreted/compiled obligations with no path special case, fallback, exclusion, or relabeling.

Harness boundary: use the exact Sprint 157 upstream Go harness as authority. Harness code and harness tests must be Go or Bash, and unchanged original inputs must run through Bash++ without native tested-source fallback.

Closing artifact (docs/sprint-151-master-execution-plan.md, 2026-09-11): after Barrier A this card is RE-BOUND to one of the five largest decisive-rule clusters of the active Sprint 151 manifest (the family name above is a seed label, not an acceptance criterion). It closes on its active leaf's packet-gate run: exit 0, or exit 3 with every non-green root moved to a named owner (152/153/154) by a reviewed manifest commit — never an in-place relabel. Product fixes land in sh/gosource (converter owner) or sh/interp + one bashy CLI commit (bridge owner); every commit carries Sprint: #151 / Story: / Story-ID: trailers.

CLOSED 2026-09-12 (sprint151-manager). Re-bound at Barrier A to the mechanism that owned 140 of 955 active roots: interpreted execution of a program handed an explicit package map. Fixed by linking the map at lowering time (sh 794653d0) + name mangling across linked packages (sh 180852d0) + the bashy refusal removal (bashy 963ef4b). Evidence: leaf-151 run 1 on the Sprint 151 candidate (bashpp-tests 1a5959f, docs/upstream-harness/leaf-151/): the refusal class is 0; 88 roots PASS both modes (0 before); rundir packet 54/116 PASS both modes (3 at Sprint 150). Remaining failures of the former roots are re-bound to the other cards by first line (rebind.tsv).
