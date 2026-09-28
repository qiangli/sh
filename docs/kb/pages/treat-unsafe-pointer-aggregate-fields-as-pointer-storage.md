---
id: 01a0e84f-9996-7999-92df-4d4e34631229
seq: 29
form: page
type: lesson
title: Treat unsafe.Pointer aggregate fields as pointer storage
description: When GoSource evaluates a typed aggregate element whose destination is unsafe.Pointer, route address, nil, forged integer, and unsafe.Add expressions through the pointer evaluator before scalar fallback. unsafe.Pointer is a named basic type to go/types, so ordinary *T destination detection misses it and address operands otherwise fail with BASHPP-EEXPR-FORM.
status: candidate
source:
    tool: codex-gpt5.6-sol2-d
    host: dragon
    episode: weave-issue-238
created: "2026-09-28T13:58:45Z"
---
