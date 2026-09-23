---
id: 76f982359c65
kind: bug
title: Normalize Windows CRLF in text-fence verb values
seq: 144
status: done
priority: p1
labels:
    - windows
    - fences
created: 2026-09-23T16:30:38.277229Z
assignee: codex-s250
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
closed: 2026-09-23T17:31:30.897864Z
closed_by: codex-s250
---

Windows BashSharp Tour manifests/pyproject and manifests/package retain CR from CRLF processor stdout because polyglot.Text callText trims LF only. Preserve original transcripts and time limits. Normalize text-fence value line endings at the capture boundary without changing binary/raw captures, add a focused CRLF control, then rerun exact Windows Tour cases and full 40. Coordinate Tour Story #7 b93c0b653928; remove only proved stale Windows markers there.
