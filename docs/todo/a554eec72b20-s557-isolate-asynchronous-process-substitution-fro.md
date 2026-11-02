---
id: a554eec72b20
kind: bug
title: S557 isolate asynchronous process substitution from history reader replay
seq: 183
status: wontfix
priority: p1
labels:
    - superseded
created: 2026-11-02T01:22:28.912441Z
assignee: s557-test-readiness
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
closed: 2026-11-02T05:32:09.6859Z
---

Hosted Bash 5.3 run 41370659557 macOS reports histexpand failure (85/86; Linux 86/86). Retained raw difference begins with missing process-substitution output and subsequent literal history events. Diagnose with bounded authored differential and deterministic scheduling tests; repair only proven reader ownership defect. Preserve exact upstream fixtures and assertions. Full validation stays hosted; no local foreign suite or product build. Parent owns integration and frozen candidate pins.

## Delivery evidence

The held-child authored regression proves a reader-ownership defect independently
of platform: a process substitution enters a nested command substitution and
waits on a channel while the parent reads its next history event. The original
process-global suppression counter causes the parent to print literal `!!`, then
replay that command late when the child finishes. The corrected implementation
keeps replay/substitution depths on each Runner and inherits them by value into
children; process-substitution bodies never consume the enclosing reader timeline.
History storage, event syntax and fixture policy are unchanged.

Three authored checks cover ordinary process substitution, a producer forced to
finish before parent dispatch, and a blocked nested substitution overlapping the
parent reader. Exact combined output assertions reject missing, duplicate and
late parent execution. The ordinary authored example matches GNU Bash 5.3.15.
The first two examples also passed before the repair (including 100 ordinary
repetitions); the held-child regression failed before the repair.

Validation: `GOWORK=off GOMAXPROCS=2 GOFLAGS=-p=2 go test -race ./interp -run
'^(TestHistory|TestFc|TestInteractiveHistory|TestProcSubstReadOnce)' -count=3
-timeout=60s` PASS (1.805s). Full suites and the foreign Bash suite remain hosted.

The original macOS run 41370659557 remains a recorded 85/86 failure. Its plain
process-substitution failure has not been reproduced locally, so attribution of
that exact hosted difference to this proven ownership defect remains a hypothesis
pending the unchanged hosted rerun. A subsequent green run would not erase that
historical evidence. This repair does not redesign the singleton history store
or move history expansion ahead of all argument/redirection evaluation.

## Superseded by consolidation — 2026-11-02

Operator requested consolidation of implementation repairs into acceptance owners. Scope, original acceptance, unresolved work and evidence transfer to public umbrella #6, story `bf7624a5c2f2`. This record is closed by decision with label `superseded`; it is NOT a delivery acceptance, test pass, waiver or feature retirement. The parent remains open. Continue corrections under that parent rather than creating another repair story. Original content above is retained unchanged.
