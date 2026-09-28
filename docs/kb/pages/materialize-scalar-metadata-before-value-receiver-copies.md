---
id: 01a0e735-95bc-7d2f-a79e-af652dacb6e4
seq: 27
form: page
type: lesson
title: Materialize scalar metadata before value receiver copies
description: When a GoSource value-receiver method is bound on a cell whose object carrier has scalar bridge metadata, rebuild the receiver copy through bashPPStoreCellValue before method execution; otherwise callback/testing frames can pass a named numeric receiver as an object and fail with BASHPP-EEXPR-OPERAND.
status: candidate
source:
    tool: codex-gpt-5.5-g
    host: dragon
    episode: weave-issue-215
created: "2026-09-28T08:50:43Z"
---
