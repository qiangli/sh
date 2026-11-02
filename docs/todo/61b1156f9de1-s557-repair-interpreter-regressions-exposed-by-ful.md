---
id: 61b1156f9de1
kind: bug
title: S557 repair interpreter regressions exposed by full migrated shell suite
seq: 168
status: wontfix
priority: p1
labels:
    - superseded
created: 2026-11-01T17:42:07.645828Z
assignee: s557-manager
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
closed: 2026-11-02T05:32:10.449906Z
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

Delivery note: separate pre-existing interface limitation

The following diagnostic is separate from the 14 failures observed in hosted
run 41327533961. A typed nil pointer to an imported concrete type can be refused
when assigned to an imported interface containing a package-private method:

```go
package main

import (
    "fmt"
    tree "go/ast"
)

func main() {
    var x tree.Decl = (*tree.GenDecl)(nil)
    fmt.Println(x == nil)
}
```

Native Go accepts this program and prints `false`. The interpreter reports
`BASHPP-EINTERFACE-MISSING: *tree.GenDecl does not implement interface (missing method declNode)`.
The same refusal occurs without the import alias. Focused diagnostic overlays
reproduced it with both the current nil-declaration code and the version before
6c5220fd; that repair did not introduce the limitation. The typed nil has no
dependency-owned handle, so admission falls back to local method lookup, which
cannot resolve this imported private method.

This migration repair preserves the existing fail-closed behavior. No interface
admission policy was broadened, no assertion was removed, and this diagnostic
is not counted as a newly passing gate or an exclusion from the full suite.

Additional macOS evidence from the cancelled obsolete run

Run 41327533961 used shell 6982b2e and was cancelled before macOS completed;
it is not acceptance evidence. Its completed macOS tests exposed three failures
beyond the Linux 14: bridge readiness/cwd, chanlinear deadline, and selector
scaling. These remain distinct from the green 14-test focused rerun.

The bridge fixture published `bridge.ready` before its cwd contents were fully
written. A reader could observe an empty file and resolve it to `.`. The fixture
now publishes the completed payload by rename, rejects empty readiness payloads,
and checks path-resolution errors. Both modes pass three local repetitions with
the original absolute-cwd and package-isolation assertions intact.

The unchanged Go 1.27.2 `chanlinear.go` fixture passed bounded local diagnosis:
oracle build/run took 0.312s, interpreter began at 0.467s and completed at 1.778s,
and the whole three-mode comparison took 2.173s. This proves prerequisites shared
the old execution clock, but does not establish why the obsolete hosted run hit
60s. Only `TestS243OriginalChannelDomainRegressions` now opts into separately
bounded 120s builds and unchanged 60s native/interpreter executions, with phase
timings. Other callers retain their shared deadline. Original fixture bytes and
exact stdout/stderr comparisons are unchanged. Both original channel fixtures
passed three repetitions with exact Go 1.27.2 source; hosted validation is still
required, and the old timeout is not declared resolved by local success.

## Superseded by consolidation — 2026-11-02

Operator requested consolidation of implementation repairs into acceptance owners. Scope, original acceptance, unresolved work and evidence transfer to public umbrella #6, story `bf7624a5c2f2`. This record is closed by decision with label `superseded`; it is NOT a delivery acceptance, test pass, waiver or feature retirement. The parent remains open. Continue corrections under that parent rather than creating another repair story. Original content above is retained unchanged.
