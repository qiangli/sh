---
id: 01a0d9d3-1075-70c7-9c8c-6d0898bd94b4
seq: 15
form: page
type: lesson
title: Resolve imported package pointer targets in the native bridge
description: When GoSource takes the address of an imported package variable, field, or element, do not build an interpreter pointer rooted at the import alias; ask the native worker for var/field/index pointer handles and route deref reads and writes through that handle. This fixes BASHPP-EPOINTER-TARGET undefined import-alias roots while preserving local pointer, callback, and generic behavior.
status: candidate
source:
    tool: codex-gpt-5.5-h
    host: dragon
    episode: weave-issue-8
created: "2026-09-25T18:28:03Z"
---
