---
id: e74ad50874cf
kind: task
title: S174.2 recover and replan Sprint 170 under-investigated evaluator residue
seq: 105
status: todo
priority: p0
created: 2026-09-14T14:25:17.275536Z
sprint: 174
---

Entry gate
Begin only after Sprints 169-173 are complete, as required by Sprint 174 acceptance. Sprint 174 is a union/deduplication and replanning sprint; implementation belongs in bounded successor stories unless the Sprint 174 manager explicitly changes that acceptance.

Evidence sources
- umbrella docs/sprint-170-leaf-failures.tsv (exact 69-row residual ledger)
- umbrella docs/sprint-170-master-execution-plan.md, Closure section
- Sprint 170 card/thread and worker artifacts
- measured candidate: umbrella 7fea5a5; sh be8a1cf7; bashy 2cdcf77; coreutils 24644e9; harness f54715f; evaluator digest d9c8d7d247...

What went wrong in Sprint 170
1. The 74-root scope was frozen from leaf-165r0, but later Sprint 165 subset credits were not subtracted before execution.
2. integ-170-r1 reported 5 PASS / 69 FAIL. All five PASS roots were already credited: bug341, issue17752, issue59411, typeparam/issue42758, and typeparam/issue48453. Sprint 170 therefore earned zero new closures; official truth remains 91/559 closed and 468 remaining.
3. Address worker #208 was cost-stopped at 4m04 with no diff. Collection worker #209 was cost-stopped at 4m17 with no production diff and only partial test work. This left 25 address and 13 collection rows effectively under-investigated.
4. Scalar worker #210 ran 7m18 and landed the unsigned named-scalar bridge at sh be8a1cf7 with its focused test passing, but it closed none of the frozen roots. The 31 scalar rows remain mixed and under-investigated.
5. The attempted local combined gate is invalid as implementation evidence: it ran before the cherry-pick, timed out at 10 minutes under saturation, and included pre-existing syntax-oracle deltas. Do not reuse it as proof and do not rerun integ-170-r1 unchanged.

Required Sprint 174 work
1. Union and deduplicate all 69 rows against the exact S169, S171, S172, S173 ledgers and all prior Sprint 165 credits before computing any denominator.
2. Reclassify each row by its final observed first cause, not its original bucket. Several original collection-key rows now expose later causes such as composite literals, divide-by-zero, interface conversion, selector, or panic behavior.
3. Trace representative rows to concrete code paths and split the residue into evidence-backed mechanisms. Expected areas to test, not assume:
   - address-expression residue (many of the 25 still identify BashPPAddressExpr);
   - collection residue split between array-key representation and later defects such as panic/interface/selector/divide-by-zero;
   - scalar residue split across conversions (float, named generic, unsafe.Pointer, []byte), comparisons, and builtin/bridge behavior.
4. Create bounded successor stories. Each must carry an exact target TSV excluding every prior credit and every overlap with concurrent ledgers; target 3-6 roots or one coherent mechanism; require an outside-corpus reproducer, strict negatives, and a focused gate. Allow enough investigation time to identify a mechanism; do not stop solely after two polling ticks.
5. Record a limitation only after citing the observed evidence and code path showing no bounded implementation mechanism.

Acceptance
- Produce an exact deduplicated ledger/count with all five precredited PASS rows excluded from new credit.
- Assign every one of the 69 rows exactly once to a confirmed mechanism, a testable hypothesis, or an evidence-backed limitation.
- Create non-overlapping bounded successor stories with exact target files and verification gates.
- No row may be rolled forward solely because Sprint 170 stopped early, and no duplicate may receive closure credit.
