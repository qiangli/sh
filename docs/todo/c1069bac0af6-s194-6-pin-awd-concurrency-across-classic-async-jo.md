---
id: c1069bac0af6
kind: task
title: S194.6 pin awd concurrency across Classic async jobs and Bash++ go tasks
seq: 124
status: done
priority: p0
created: 2026-09-15T12:05:21.550966Z
weave: 221
assignee: qiangli
sprint: 194
closed: 2026-09-15T15:40:17.884869Z
resolution: fixed
closed_by: codex-gpt5.6-terra-m
---

Add adversarial tests that simultaneous awd calls in supported Classic async lists and Bash++ go tasks each observe their own cwd/PWD/OLDPWD/dir stack and restore the parent. Run under -race and through installed bashy; fix only a demonstrated general defect. Document that Runner-local cwd differs from process-global os.Chdir, foreground cd persistently changes state, isolated-copy cd may be safe, and concurrent caller-driven Run/Subshell on one *Runner remains outside the API contract. Gate: focused -race count=10 plus installed stress loop.
