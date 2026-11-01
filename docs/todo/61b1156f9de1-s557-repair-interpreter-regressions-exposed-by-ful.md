---
id: 61b1156f9de1
kind: bug
title: S557 repair interpreter regressions exposed by full migrated shell suite
seq: 168
status: assigned
priority: p1
created: 2026-11-01T17:42:07.645828Z
assignee: s557-manager
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

Full hosted shell run41327533961 Linux executes3902 tests with14 top-level failures and19 subtest failures. Repair explicit unsafe.Pointer conversion, native callback refusal and method-value/nil semantics without weakening bridge authorization, assertions, coverage or native differential oracles. Replace stale retained-slice refusal with native differential only where current supported shared-view semantics are independently covered. Preserve existing full/race discovery; manifest maintenance remains story3ea2aafcb341. Bounded focused reproductions on developer host; full and race suites hosted. Manager owns integration, agent test_readiness and repair_review provide coordinated patches.

Reviewed repair evidence:

- b6b0d090 retains the historical nested-slice fixture as a native differential
  after shared-view Reverse support, and refuses allocation observers even with
  a synchronous lifetime proof. Three focused tests pass.
- e8ace498 routes explicit unsafe pointers through the existing pointer evaluator
  before generic imported-type handling. Nine focused tests pass, including
  forged/stale identity and unsafe-view refusal checks. Its manifest repair is
  also tracked by story166; no selector or budget was reduced.
- c5fa9121 compares the mapped-type fixture with native Go and makes the trimpath
  child include full-tag tests instead of silently matching none. Three exact
  regressions pass with the reviewed SDK cached. Hosted isolation fixtures need
  the exact authenticated toolchain module in addition to the runner SDK install.
- The directional-channel compile/runtime fixture was already repaired by
  5f83027e under story167. Nil-interface and resident method-value repairs remain
  under review. Full hosted shell and race acceptance remains required.
