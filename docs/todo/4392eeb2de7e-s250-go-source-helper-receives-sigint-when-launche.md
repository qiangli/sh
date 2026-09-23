---
id: 4392eeb2de7e
kind: bug
title: S250 Go-source helper receives SIGINT when launcher inherited ignore
seq: 146
status: assigned
priority: p0
labels:
    - macos
    - go-source
created: 2026-09-23T16:54:22.387887Z
weave: 240
assignee: qiangli
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
---

Mac GBE signals.go interpreted timed out after readiness at 20s while oracle and compiled pass. Reproduced on novidesign with the exact candidate binary: Python parent sets SIGINT to SIG_IGN, launches Bashy Go-source in a new process group, reads 'awaiting signal', sends SIGINT to group; Bashy times out. Same launch of oracle exits 0 and prints interrupt signal received. The Go-source native helper is put in a separate group at interp/bashpp_native_bridge.go and parent forwardExecReplacementSignalsWithReport skips SIGINT when osSignalIgnored, so helper misses group SIGINT. Preserve original example/harness deadlines; fix narrowly for Go-source signal forwarding with inherited ignore, add focused regression, run exact Mac row/full gate. Coordinate with tests Story #90.
