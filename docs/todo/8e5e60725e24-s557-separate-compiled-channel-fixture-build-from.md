---
id: 8e5e60725e24
kind: bug
title: S557 separate compiled channel fixture build from execution
seq: 167
status: assigned
priority: p1
created: 2026-11-01T17:05:41.977348Z
assignee: s557-manager
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

TestGoSourceNestedDirectionalChannel pins a cold Go1.27.1 toolchain while CI installs1.27.2, includes compiler download stderr in expected output and shares10second execution deadline. Separate build diagnostics and runtime output, select existing supported toolchain without weakening semantic assertion, verify bounded targeted tests and hosted native lane.
