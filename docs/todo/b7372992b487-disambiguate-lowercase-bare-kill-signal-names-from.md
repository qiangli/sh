---
id: b7372992b487
kind: bug
title: Disambiguate lowercase bare kill signal names from attached -s option
seq: 161
status: assigned
priority: p0
created: 2026-10-03T02:36:25.821564Z
assignee: codex-gpt6-sol
sprint: 355
sprint_id: 3a83ff48-7f8b-5be4-b0e6-e146762b2573
sprint_title: Profile D residual blocker triage and targeted closure
---

The shell builtin kill parses any token beginning -s and longer than two bytes as attached -s<spec>, so -stop becomes invalid top although GNU Bash accepts lowercase bare -SIGNAME. Investigate exact VSC kill_NE TP9 command and fix only with confirmed behavior; preserve -sHUP and -s STOP, negative process-group PID semantics, and POSIX signal-name rules. Replay focused kill on one-file static Linux candidate.
