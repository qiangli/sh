---
id: 3ea2aafcb341
kind: bug
title: S557 update shell race gate bounds for the current package inventory
seq: 166
status: wontfix
priority: p1
labels:
    - superseded
created: 2026-11-01T16:17:29.537411Z
assignee: s557-manager
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
closed: 2026-11-02T05:32:09.306027Z
---

Migrated full shell validation run41324774379 fails before assertions: race gate discovers23 packages but fixed max_packages20 and derived8400s global budget remain stale. Review exact package inventory at current shell commit; update bounded package ceiling and derived watchdog coherently. Preserve all discovery, per-step caps, race lanes and fail-closed behavior. No test exclusions. Short driver contract checks local; full race execution hosted. Delegated implementation/review support s557-repair-review; manager owns integration.

## Superseded by consolidation — 2026-11-02

Operator requested consolidation of implementation repairs into acceptance owners. Scope, original acceptance, unresolved work and evidence transfer to public umbrella #6, story `bf7624a5c2f2`. This record is closed by decision with label `superseded`; it is NOT a delivery acceptance, test pass, waiver or feature retirement. The parent remains open. Continue corrections under that parent rather than creating another repair story. Original content above is retained unchanged.
