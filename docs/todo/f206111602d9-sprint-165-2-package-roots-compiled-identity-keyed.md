---
id: f206111602d9
kind: task
title: 'Sprint 165.2: package roots compiled — identity-keyed internal visibility (USER DECISION) + generated-package fidelity; 3/26 → 26/26'
seq: 98
status: todo
priority: p0
created: 2026-09-13T09:07:05.065407Z
sprint: 165
---

D1 of #162 stands: compiled via the -overlay route (lower library emission 8c91e4e0, bashy transpile --go-library 41f100f, bashpp-tests one-library-per-package backend + go test -overlay); 3/26 PASS compiled on integ-2 (abt, compare, devirtualize). Remaining compiled failures: (a) the CHECKER refuses internal imports at the wrong identity — 'could not import internal/testenv|internal/buildcfg (use of internal package not allowed)' — the identity-keyed internal visibility of sh/docs/bashpp-multi-package-execution.md §4.1 (BashPPStdlibImportAllowed widened ONLY for an explicit --go-import-path inside std/cmd; a user identity never qualifies): THIS WIDENS A REVIEWED SECURITY BOUNDARY — get the user's decision before implementing; (b) generated-package test failures ('exit status 1') per root = lowering fidelity rows: read the go-test json per package and fix the emitter (152 D1 identity). Interpreted stays recorded by ID (26). RULES: exact upstream Go 1.27 harness is the authority; harness/tests Go or Bash only; product fixes = general Go mechanisms in sh/{gosource,interp,lower} from outside-corpus reproducers under <seam>/testdata/sprint165/<mechanism>/ with a DRIVING TEST and the negative set; never keyed to a fixture/expected string; never permissive; never a timeout raise; never edit sh/gosource/internal/gcsyntax; keep Options.CheckAfterSyntaxErrors. Rows recorded by ID in #162 (D1–D7) are never relabeled. Verify: focused go test -count=1, then the FULL sh gate go test -short ./interp/ ./lower/ ./gosource/ ./syntax/ (lower's parity tests are the classic-isolation gate; 8 GoSource* interp tests are pre-existing darwin failures), gofmt, git diff --check; then a subset leaf — workers ship a bundle + root TSV, ONLY the manager submits (leaf-submit.sh on sprint162-leaf). Trailers Sprint: #165 / Story: #<seq> / Story-ID as the last paragraph.
