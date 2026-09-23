---
id: a9ef656fb93e
kind: bug
title: Adopt inherited Unix file descriptors in pure bash startup
seq: 135
status: done
priority: p1
labels:
    - sh
    - fd
    - macos
created: 2026-09-23T02:21:40.826691Z
assignee: codex-gpt5.6-sol
sprint: 253
sprint_id: d25e93b0-ad03-56f2-831f-1e9f626e609c
sprint_title: 'Windows fixtures 76/86 to done: one regression, four found causes, one probe, one provisioning'
closed: 2026-09-23T02:27:14.758101Z
closed_by: sprint253-manager
---

Pure cmd/bash on macOS loses a small unbridged inherited descriptor across exec: exec 9</etc/hosts; exec bash -c "read -u 9 line" reports invalid file descriptor even while OS fd 9 is open and BASHY_INHERITED_FDS=9. Acceptance: child bash lazily adopts open inherited fd 9 on first use before read/mapfile, reads a line, preserves close/redirect precedence, and focused regression passes on macOS. Sprint #253 late-discovered blocker for S253.7 owned argv/env handoff proof.
