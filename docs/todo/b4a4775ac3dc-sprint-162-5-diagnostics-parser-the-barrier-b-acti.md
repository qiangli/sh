---
id: b4a4775ac3dc
kind: task
title: 'Sprint 162.5: diagnostics/parser — the barrier-b/active-154 roots (7) + unclassified triage (26)'
seq: 95
status: done
priority: p1
created: 2026-09-13T00:37:02.770036Z
assignee: qiangli
sprint: 162
closed: 2026-09-13T10:32:32.998844Z
---

Input: barrier-b/active-154-manifest.tsv (7: issue11362 non-canonical import path wording, import6 empty import path wording, issue23586/issue4468/issue50372 gc-accepts-vs-go/parser-rejects so the expected checker diagnostic never runs, slice3err middle-index wording, nul1 NUL-in-source position reported in the working copy path) + barrier-b/active-unclassified.tsv (26: 3+3 typechecker rows, 'whatis' const-default printing, subdict/equal/issueNa typeparam interface comparison, 'p= q= p==q = false' pointer identity, …) re-measured by S162.0. For the 154 rows: the importer wording rows are checker-owned (go/types importer message vs gc's noder) — fix at the point of divergence with gc's wording only where the corpus regex requires it and a general rule exists; the accept/reject divergences need gc's syntax verdict to be applied before go/parser recovery on those forms (Sprint 154 D3 vendored gcsyntax; extend its use, never a wording map). For unclassified: triage each to a first cause and MOVE it by manifest commit to 162.1/162.3/162.4 — an unclassified root must not be left unclassified at Barrier C. Exit: 154 rows PASS or carry a recorded reason; unclassified is empty. RULES: fixes are general Go mechanisms in sh (gosource / interp / lower), each from an OUTSIDE-CORPUS reproducer with the negative set; never keyed to a fixture path or expected string; never a permissive comparison; never a timeout raise. Verify: focused go test -count=1, go test -short ./..., git diff --check (judge by NEW darwin failures only; never in a weave --verify), then the story leaf on sprint162-leaf (one coordinator; never the certification host, never the dev box) plus nearby native-PASS canaries. Rows move by manifest commit, never in place. Commit trailers Sprint: #162 / Story: S162.<n> / Story-ID: <id> as the last paragraph.
