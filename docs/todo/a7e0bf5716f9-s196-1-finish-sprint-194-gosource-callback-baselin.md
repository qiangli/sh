---
id: a7e0bf5716f9
kind: task
title: S196.1 finish Sprint 194 GoSource callback baseline
seq: 129
status: todo
priority: p0
created: 2026-09-15T18:28:20.824422Z
sprint: 196
---

Inherit published sh master 85cecf417fb0. One reproduced failure remains: TestGoSourceUnwrapThreeModes/pointer_state_reentry prints true 0 / true 0 versus native true 1 / true 2. Preserved diagnostic weave sh#231 must not be merged with DBG instrumentation. Fix original pointer-receiver state reentry generally, rerun the exact callback ledger, then cover the complete short inventory with non-overlapping bounded shards (monolithic interp/lower exceed native 10m under package-wide parallel barriers; do not raise timeouts or hide failures). Publish sh.
