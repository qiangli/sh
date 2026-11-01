---
id: 3ea2aafcb341
kind: bug
title: S557 update shell race gate bounds for the current package inventory
seq: 166
status: assigned
priority: p1
created: 2026-11-01T16:17:29.537411Z
assignee: s557-manager
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

Migrated full shell validation run41324774379 fails before assertions: race gate discovers23 packages but fixed max_packages20 and derived8400s global budget remain stale. Review exact package inventory at current shell commit; update bounded package ceiling and derived watchdog coherently. Preserve all discovery, per-step caps, race lanes and fail-closed behavior. No test exclusions. Short driver contract checks local; full race execution hosted. Delegated implementation/review support s557-repair-review; manager owns integration.
