---
id: 01a0e7c2-b0b4-7367-8e55-ab429f1595e3
seq: 27
form: page
type: lesson
title: Scope pointer writeback to mutable or retained origins
description: 'When an interpreted value is traversed through dependency reflect.Value or reflect.Type, do not snapshot every live origin after each read-only request: large AST walks become repeated whole-graph JSON encodes. Classify only contractually read-only reflect/fmt operations, track retained origins separately, and keep one-shot reconciliation for mutating reflect.Value operations so Set and out-parameter writeback remain sound.'
status: candidate
source:
    tool: codex-gpt5.6-sol2-p
    host: dragon
    episode: weave-issue-224
created: "2026-09-28T11:24:51Z"
---
