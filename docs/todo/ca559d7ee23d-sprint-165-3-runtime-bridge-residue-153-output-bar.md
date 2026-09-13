---
id: ca559d7ee23d
kind: task
title: 'Sprint 165.3: runtime/bridge residue (153) — output barrier, callback signatures, next-defect rows behind runtime panics'
seq: 99
status: doing
priority: p0
created: 2026-09-13T09:07:05.090479Z
sprint: 165
---

Input: Barrier C active-153. Landed in 162: generic callback receivers, native field/package-variable writes, byte-exact bridge strings, runtime errors as recoverable panics, deadline family by ID (D2). Remaining: exact/stray output 12 ('output should be empty when expected-output absent'), 'original callback signature requires value-semantics parameters' 14 (decide per shape: general mechanism vs by-ID), 'asynchronous or retained original function callbacks' 4 (testing.AllocsPerRun/reflect.MakeFunc — by ID unless general), unregistered bridge type 5, the NEXT defect behind the new runtime panics (index/nil/'panic: FAIL' ~17 — each is an evaluator semantics bug reached only now), makeslice 2, runtime.Caller/reflect frame rows 2, error %v rendering of an errors.New value recovered through recover (prints the transport struct). Ledger: interp/testdata/sprint162/runtime-bridge/FINDINGS.md. RULES: exact upstream Go 1.27 harness is the authority; harness/tests Go or Bash only; product fixes = general Go mechanisms in sh/{gosource,interp,lower} from outside-corpus reproducers under <seam>/testdata/sprint165/<mechanism>/ with a DRIVING TEST and the negative set; never keyed to a fixture/expected string; never permissive; never a timeout raise; never edit sh/gosource/internal/gcsyntax; keep Options.CheckAfterSyntaxErrors. Rows recorded by ID in #162 (D1–D7) are never relabeled. Verify: focused go test -count=1, then the FULL sh gate go test -short ./interp/ ./lower/ ./gosource/ ./syntax/ (lower's parity tests are the classic-isolation gate; 8 GoSource* interp tests are pre-existing darwin failures), gofmt, git diff --check; then a subset leaf — workers ship a bundle + root TSV, ONLY the manager submits (leaf-submit.sh on sprint162-leaf). Trailers Sprint: #165 / Story: #<seq> / Story-ID as the last paragraph.
