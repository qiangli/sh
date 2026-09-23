---
id: 6404343b27f2
kind: enhancement
title: S250 Windows callback mailbox parity for original Tour image deadline
seq: 147
status: todo
priority: p0
labels:
    - windows
    - callback
    - tour
created: 2026-09-23T17:04:45.094173Z
sprint: 250
sprint_id: c912e608-edfe-59b8-bd36-a98f6dad1634
sprint_title: Validate Go by Example, Go Tour and BashSharp Tour on three hosts
---

Story #141/#143 dependency. Linux authenticated original Tour image row passed at 40.425s with Unix shared-memory callback mailbox; Windows currently uses the socket fallback because bashpp_native_mailbox_other.go returns nil, and interpreted image.go still misses the unchanged 60s bound. Implement Windows-native bounded mailbox transport or another measured equivalent without skipping/compiling original callbacks, preserving output-before-callback order, oversized replies, ordinary request blocking, exactly-once effects, panic/cancel and reference identity. Use isolated branch, focused Windows runtime controls and exact authenticated original-source 60s row; do not change fixture/comparator/deadline. Coordinate noviwin1 named claim with Story #145/#142 work. Linux product integration may proceed independently; full Tour gate remains manager-owned.
