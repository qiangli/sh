---
id: 4be7e9713d70
kind: bug
title: S250 compare native Windows syscall error with nil in Go source
seq: 152
status: todo
priority: p0
labels:
    - windows
    - gosource
created: 2026-09-23T19:13:58.071924Z
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
---

Final Windows GBE execing-processes row 23 on Bashy809/sh8f reaches syscall.Exec, which native Go returns EWINDOWS. Oracle and compiled execute `if execErr != nil` then panic at line 48 with `not supported by windows`; interpreted stops at line 47 with BASHPP-EEXPR-NIL: nil is not a scalar. Repair only the native error-versus-nil comparison while preserving handle identity and existing nil semantics; add focused Go1.27.1 native oracle controls on Windows, then replay the unchanged GBE row at original 60s. Harness panic stack equivalence is tests Story #98.
