---
id: 819ec8b87c26
kind: task
title: S196 restore shell while conditions with assignment prefixes in Bash++
seq: 132
status: assigned
priority: p0
created: 2026-09-16T10:50:26.127905Z
assignee: codex-gpt5.6-sol
sprint: 196
---

Reproduced by final Rust DAG gate on sh a460d2ff/Bashy 3a8e2ce: while IFS= read -r line; do ...; done fails with do can only be used in a loop under --bashpp. Valid shell syntax in uv/Codex/Bun/mise graphs must remain accepted. Fix general parser routing boundary minimally; add exact and outside-corpus positives plus reserved-form negatives; preserve Sprint198 explicit Bash++ contract. Manager verifies focused syntax/classic guards and cold/warm installed DAG gates. No example rewrites to avoid the parser bug.
