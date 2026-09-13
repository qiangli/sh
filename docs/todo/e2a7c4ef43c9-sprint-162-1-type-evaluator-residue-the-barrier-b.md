---
id: e2a7c4ef43c9
kind: task
title: 'Sprint 162.1: type/evaluator residue — the barrier-b/active-151 roots (511, minus the 26 package roots), by mechanism'
seq: 91
status: assigned
priority: p0
created: 2026-09-13T00:36:40.612037Z
weave: 173
assignee: qiangli
sprint: 162
---

Input: bashpp-tests docs/upstream-harness/barrier-b/active-151-manifest.tsv re-measured by S162.0 leaf run 0 on the published candidate (bashy 548c3a4 / sh e7cd317e / coreutils 4cb658d4). Barrier B first-line classes (interpreted rows, roots): 28 compilation-succeeded-unexpectedly (missing expected checker error), 27 BASHPP-EEXPR-FORM unsupported scalar expression, 23 BASHPP-ECOLLECTION-ELEMENT, 21 BASHPP-ENIL-DEREF, 11 BASHPP-EEXPR-NIL, 10 BASHPP-ESELECTOR-ASSIGN, 9 invalid select case, 9 undefined callable recover, 7 BASHPP-ECONST-EXPR, 7 collection-bounds, 6 EBUILTIN-TYPE, 8+3 typechecker rows (check_test.go: runner parser != gc, recorded in 154 — move to S162.5 only with a diagnostic first cause), the 28 gc-only checks go/types cannot express (permanent — record by ID as a design decision, never relabel), and a long tail; compiled rows (71 roots): gosource unsupported *ast.StructType/InterfaceType/ArrayType expression forms, unsupported call target, range-over-int lang gate. Work by mechanism cluster (nil/pointer path, collections, expression forms, select, recover/panic path, const exprs), the biggest cluster first; each cluster = reproducer + fix + focused tests + subset leaf + full 151 leaf. Exit: every root PASSES in its required modes (compiled for all; interpreted except compiler-artifact recipes) or is moved by manifest with a first cause / recorded design decision. RULES: fixes are general Go mechanisms in sh (gosource / interp / lower), each from an OUTSIDE-CORPUS reproducer under the package testdata with the negative set; never keyed to a fixture path or expected string; never a permissive comparison; never a timeout raise. Verify: go test -count=1 on the affected packages, go test -short ./..., git diff --check (the 8 GoSource* interp tests fail on darwin before and after every change — judge by NEW failures only; never in a weave --verify), then the story leaf on sprint162-leaf (one coordinator; BASHPP_CORPUS_ROOTS=<manifest> tools/upstream-harness/corpus-gate.sh in a fresh /srv/sprint162/leaf-<name>; never the certification host, never the dev box) plus nearby native-PASS canaries. A row whose real first cause belongs to another owner moves by manifest commit, never in place. Commit trailers Sprint: #162 / Story: S162.<n> / Story-ID: <id> as the last paragraph.
