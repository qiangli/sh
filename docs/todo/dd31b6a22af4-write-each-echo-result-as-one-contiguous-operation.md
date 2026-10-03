---
id: dd31b6a22af4
kind: bug
title: Write each echo result as one contiguous operation for VSC kill capture
seq: 162
status: assigned
priority: p0
created: 2026-10-03T02:43:40.080331Z
assignee: codex-gpt6-sol
sprint: 355
sprint_id: 3a83ff48-7f8b-5be4-b0e6-e146762b2573
sprint_title: Profile D residual blocker triage and targeted closure
---

Traced kill_NE IC3 TP9 delivered SIGSTOP and SigWait wrote the expected line, but Bashy echo wrote exitstatus, space, 0, newline as four writes to shared stderr; SigWait interleaved its line between them, so VSC grep missed both exact lines. Assemble echo output and write once while preserving -n, -e/-E, XSI and backslash-c behavior and write-error status. Verify via concurrency regression and focused Profile D kill replay under exact new approval.
