---
id: 85d8d1058383
kind: bug
title: S250 render native Go error values in Windows interpreted panic
seq: 151
status: todo
priority: p0
labels:
    - windows
    - gosource
created: 2026-09-23T18:42:40.921525Z
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
---

Final Windows GBE row 23 execing-processes and row 61 spawning-processes expose a Go-source interpreter product gap: when the native Go SDK returns *os/exec.Error through the Windows bridge, panic(error) prints a serialized native handle JSON instead of the error string produced by the pinned native oracle. Keep the error value and native identity intact; stringify it only at the observable panic boundary using the same Go error contract. Add focused Windows controls for *os/exec.Error panic and ordinary error/control panics, preserve source and deadlines, then replay the authenticated GBE rows. Harness stack-path equivalence is separately tracked in bashsharp-tests #98.
