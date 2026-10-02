---
id: 391ee0c6b3f6
kind: bug
title: Preserve caller-owned inherited file descriptors in sh runner
seq: 158
status: todo
priority: p0
labels:
    - macos
    - ci
created: 2026-10-02T06:05:38.739199Z
sprint: 355
sprint_id: 3a83ff48-7f8b-5be4-b0e6-e146762b2573
sprint_title: Profile D residual blocker triage and targeted closure
---

Baseline af489df Darwin P–T group intermittently fails before kill-zero fix; candidate dddd3fe also fails. Runner inheritedFd wraps caller-owned raw descriptor with os.NewFile and may close it via runner cleanup or finalization, yielding unrelated EBADF. Acceptance: focused ownership regression fails before repair and passes after; inherited descriptors are atomically duplicated with CLOEXEC; caller descriptor remains usable after runner close/reset; relevant Unix tests and macOS CI pass. Do not attribute all P–T flakes without evidence.

Continuity 2026-10-02: the focused caller-descriptor ownership test fails on dddd3fe and passes with an atomic CLOEXEC duplicate; existing inherited-descriptor and redirect tests pass 10 repetitions. Darwin P–T passes 10 repetitions normally and 5 verbose repetitions with the repair. A full quick run stalled in P–T after earlier groups passed; it was interrupted after more than three minutes for a stack capture and is not counted as a pass. P–T also intermittently fails on parent af489df. Fresh CI remains the release gate; the ownership repair alone does not establish that every P–T flake is fixed.
