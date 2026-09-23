---
id: 4be7e9713d70
kind: bug
title: S250 compare native Windows syscall error with nil in Go source
seq: 152
status: done
priority: p0
labels:
    - windows
    - gosource
created: 2026-09-23T19:13:58.071924Z
assignee: codex-s250
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
closed: 2026-09-23T20:26:57.565526Z
closed_by: codex-s250
---

Final Windows GBE execing-processes row 23 on Bashy809/sh8f reaches syscall.Exec, which native Go returns EWINDOWS. Oracle and compiled execute `if execErr != nil` then panic at line 48 with `not supported by windows`; interpreted stops at line 47 with BASHPP-EEXPR-NIL: nil is not a scalar. Repair only the native error-versus-nil comparison while preserving handle identity and existing nil semantics; add focused Go1.27.1 native oracle controls on Windows, then replay the unchanged GBE row at original 60s. Harness panic stack equivalence is tests Story #98.

Final acceptance, 2026-09-23: focused Windows native-oracle controls for `syscall.Exec` EWINDOWS and `os.Stat` nil/non-nil returned values passed. Authenticated `execing-processes.go` then passed oracle/interpreted/compiled with exit 2, unchanged source and 60s limit. The Bashy `e48bd7df29a496692f22ebb72520f7f32c932697` / sh `0736c52ec82d39922359548f0496e44ec583c6ba` full Go by Example gate passed **255/255**, independent validator PASS, at bashsharp-tests `eda9a22856288ebc239cb177c2d206cf69d51da4`. Semantic root `6867eacc716b234cd30f830d9ae01b93c7e9ea3c1b8b55d3a06aaa9ce36e154e`; ledger SHA-256 `7614c6adb597b2cb2a5024bc88ce5e00f158e8a18ede198584ab7574c872148f`.
