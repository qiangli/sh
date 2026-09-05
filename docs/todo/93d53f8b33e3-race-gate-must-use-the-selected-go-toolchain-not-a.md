---
id: 93d53f8b33e3
kind: task
title: Race gate must use the selected Go toolchain, not a distro go on PATH
seq: 15
status: done
priority: p0
created: 2026-09-04T21:05:35.683328Z
assignee: review_run60_ab2_semantics
closed: 2026-09-05T08:26:22Z
sprint: 115
---

scripts/bashpp-race-gate.sh prepended /bin:/usr/bin AHEAD of the resolved toolchain directory. The GitHub ubuntu runner ships its own go in that prefix, so the gate ran go 1.24.13 while setup-go had installed 1.27, and every gate step died on 'go.mod requires go >= 1.26.5 (running go 1.24.13; GOTOOLCHAIN=local)'. Fix: resolve the toolchain once and invoke it by absolute path, and order the toolchain dir ahead of the base userland.
