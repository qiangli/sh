---
id: a436defd2124
kind: task
title: 'Bash++ P2B: grouped, blank, and dot Go imports'
seq: 12
status: done
priority: p1
created: 2026-09-03T13:01:58.221454Z
assignee: qiangli
sprint: 98
closed: 2026-09-03T14:37:14.855972Z
---

Extend accepted P2A at sh 3ce02928. Parse exact Go grouped import blocks plus blank and dot import forms under Bash++ only; preserve exact Classic/POSIX and malformed/local-module fallback; evaluate standard-library aliases with Go semantics and atomic namespace collision behavior. Typed node must print/walk/typedjson, one-byte input and lifecycle/race tests. Do not implement local/module resolution in this story. Commit exact Sprint/Story trailers and submit.
