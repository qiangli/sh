---
id: 825f8083451e
kind: task
title: Implement unchanged Go channel expression and frame semantics
seq: 51
status: done
priority: p0
created: 2026-09-09T06:37:55.650583Z
assignee: qiangli
sprint: 118
closed: 2026-09-09T18:26:30.702157Z
---

Resolved by the reviewed typed-receive/capture path already published in candidate017 at sh 037aaf8687e0aa049efb4f7a2dceb3a4941e9bbc. Candidate017 durable evidence records Tour buffered-channels, channels, range-and-close, and select PASS in baseline/interpreted/compiled, and Go by Example channel-buffering, channel-directions, and closing-channels pass in oracle/interpreted/compiled. The Sprint 118 Gemini investigation reproduced goroutine function literals, fibonacci(cap(ch), ch), computed send arguments, and receive expressions as passing on current baseline; its scratch-only branch was rejected and no additional code was needed. Candidate018 retains these commits.
