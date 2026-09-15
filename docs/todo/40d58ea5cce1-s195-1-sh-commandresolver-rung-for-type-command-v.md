---
id: 40d58ea5cce1
kind: task
title: 'S195.1 sh: CommandResolver rung for type/command -v between shell names and PATH'
seq: 128
status: done
priority: p0
created: 2026-09-15T15:54:00.714524Z
assignee: transom
sprint: 195
closed: 2026-09-15T16:15:45.197914Z
closed_by: transom
---

Embedder-owned resolver consulted by type / command -v / -V after keyword/alias/function/builtin and BEFORE the hash table and PATH — the position the pre-PATH ExecHandler rungs occupy at dispatch. nil by default: no behavior change for any runner that does not set it. type -P stays a pure PATH search; hash stays a PATH cache.
