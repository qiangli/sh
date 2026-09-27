---
id: 01a0e2c7-3821-7897-8338-8fed7431291c
seq: 25
form: page
type: lesson
title: Store bridge scalar metadata as scalar cells
description: When bashPPBridgeContents returns metadata with kind=scalar, bashPPStoreCellValue must rebuild a string scalar cell with declType/typeName/scalarKind instead of treating the metadata as collection object storage; otherwise callback/task reads of identifiers can fail with BASHPP-EEXPR-OPERAND even though the original Go value is scalar.
status: candidate
source:
    tool: codex-gpt-5.5-n
    host: dragon
    episode: weave-issue-170
created: "2026-09-27T12:11:41Z"
---
