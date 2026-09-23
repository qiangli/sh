---
id: 85d8d1058383
kind: bug
title: S250 render native Go error values in Windows interpreted panic
seq: 151
status: done
priority: p0
labels:
    - windows
    - gosource
created: 2026-09-23T18:42:40.921525Z
assignee: codex-s250
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
closed: 2026-09-23T20:26:57.288392Z
closed_by: codex-s250
---

Final Windows GBE row 23 execing-processes and row 61 spawning-processes expose a Go-source interpreter product gap: when the native Go SDK returns *os/exec.Error through the Windows bridge, panic(error) prints a serialized native handle JSON instead of the error string produced by the pinned native oracle. Keep the error value and native identity intact; stringify it only at the observable panic boundary using the same Go error contract. Add focused Windows controls for *os/exec.Error panic and ordinary error/control panics, preserve source and deadlines, then replay the authenticated GBE rows. Harness stack-path equivalence is separately tracked in bashsharp-tests #98.

Final acceptance, 2026-09-23: the focused Windows native-oracle panic control passed, and authenticated `execing-processes.go` passed oracle/interpreted/compiled with matching native Windows panic body and exit 2. The full Go by Example gate on Bashy `e48bd7df29a496692f22ebb72520f7f32c932697`, sh `0736c52ec82d39922359548f0496e44ec583c6ba`, and gate-time bashsharp-tests `eda9a22856288ebc239cb177c2d206cf69d51da4` passed **255/255**, independent validator PASS. Semantic root `6867eacc716b234cd30f830d9ae01b93c7e9ea3c1b8b55d3a06aaa9ce36e154e`; ledger SHA-256 `7614c6adb597b2cb2a5024bc88ce5e00f158e8a18ede198584ab7574c872148f`.
