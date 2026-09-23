---
id: 2b03805c1605
kind: bug
title: Fix printf POSIX timezone fixture on native Windows
seq: 136
status: assigned
priority: p1
labels:
    - windows
created: 2026-09-23T07:37:34.130243Z
assignee: codex-gpt-5.5
sprint: 257
sprint_id: 14e6cca7-6d3d-5712-b474-70aa075ad503
sprint_title: Verify final Sprint 253 Bash 5.3 candidate on Windows
---

Resolve explicit POSIX TZ in printf %(fmt)T on native Windows. The final Sprint 253 candidate maps the fixture rule to an IANA zone that is unavailable without embedded timezone data. Verify focused and full Bash 5.3 fixtures on both Windows builds; keep repository records generic.

Cause: the shell mapped the fixture's `EST5EDT,M3.2.0/2,M11.1.0/2` rule to `America/New_York`, but `time.LoadLocation` could not find that IANA zone on either native Windows build and silently returned the host's local time. The same lookup succeeded on macOS. The repair removes the fixture-specific mapping: an explicitly set POSIX `TZ` rule is evaluated through Go's TZif parser, and embedded timezone data supplies IANA names on hosts without zoneinfo files. An unset `TZ` still selects host-local time.

Independent checks compared Bashy's `printf %(fmt)T` output with GNU Bash 5.3 for US and European DST rules, winter and summer dates, fixed west and half-hour east offsets, a quoted zone name, and two IANA spellings. All matched on both native Windows builds. `go test ./expand` passes the corresponding nine-case regression table. The verified GNU Bash 5.3 corpus then passed all 86 fixtures on Windows builds 10.0.26200.9457 and 10.0.19045.6466 and on native macOS arm64, with 0 failures, skips, or timeouts on each. Linux test-droplet verification is recorded in the linked Bashy story. Raw logs and host-specific configuration remain outside the repositories.
