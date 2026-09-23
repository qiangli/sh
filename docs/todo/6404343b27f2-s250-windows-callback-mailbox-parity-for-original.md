---
id: 6404343b27f2
kind: enhancement
title: S250 Windows callback mailbox parity for original Tour image deadline
seq: 147
status: assigned
priority: p0
labels:
    - windows
    - callback
    - tour
created: 2026-09-23T17:04:45.094173Z
weave: 241
assignee: qiangli
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
---

Story #141/#143 dependency. Linux authenticated original Tour image row passed at 40.425s with Unix shared-memory callback mailbox; Windows currently uses the socket fallback because bashpp_native_mailbox_other.go returns nil, and interpreted image.go still misses the unchanged 60s bound. Implement Windows-native bounded mailbox transport or another measured equivalent without skipping/compiling original callbacks, preserving output-before-callback order, oversized replies, ordinary request blocking, exactly-once effects, panic/cancel and reference identity. Use isolated branch, focused Windows runtime controls and exact authenticated original-source 60s row; do not change fixture/comparator/deadline. Coordinate noviwin1 named claim with Story #145/#142 work. Linux product integration may proceed independently; full Tour gate remains manager-owned.

## Evidence, 2026-09-23

- Product commit `81025b96` maps the bounded callback mailbox on Windows while retaining the authenticated control socket for fallback and session lifetime. The worker's original `image.go` callback source and the 60-second limit are unchanged.
- On an authenticated Windows diagnostic candidate built from Bashy `8961419`, sh patch `9bccbc0c` (same product diff as `81025b96`), and Tour harness `409f71a`, the exact one-row run passed baseline, interpreted and compiled. Interpreted stage: **38,588 ms**. Candidate binary SHA-256: `cac69aadd11b702d737aa812ce786049c5c3db48d6ca20258f421e41821ebfc9`; manifest SHA-256: `5df6f2ca26e90d9a511c2998bfc3efc32bd2c7868910ece254eb5f081c968391`; ledger SHA-256: `a25aca9a83010724e56cc467882019ff1082517849ad193fc5b6e8e9c1264282`. The partial-run aggregate exits nonzero because the other 290 rows were deliberately excluded.
- Focused callback effects, reference boundary, body failure, lazy output barrier and Unix mailbox tests passed on macOS (`go test -tags full ./interp`, 15.503s). Windows full-tag `interp` test cross-build passed, and `TestBashPPCallbackMailboxWindowsRoundTripAndCloseLease` passed on the Windows host. Existing Windows native-oracle test helpers fail to launch their temporary oracle executable without a `.exe` suffix; they do not provide a comparative result for this patch.
- Full authenticated Windows Go Tour gate and final public-head rerun remain separate acceptance work.
