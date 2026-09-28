---
id: 01a0e837-7ae7-7386-bdf1-178fd212362b
seq: 29
form: page
type: lesson
title: Format before invoking local writers across the interpreter boundary
description: When dependency fmt.Fprint/Fprintln/Fprintf targets an interpreter-owned writer, keep primitive-only formatting local; otherwise preserve Formatter/Stringer/Error callbacks by redirecting the dependency call to Sprint/Sprintln/Sprintf, then invoke the original Write body once in the interpreter. This removes retained-writer callback traffic without changing operand formatting semantics.
status: candidate
evidence: 'Story #1087 commit 75998931: end-to-end interpreted syntax Fdump reduction timed out at 61.22s with ellipsis localization disabled and passed in 34.22s under the unchanged 60s bound; focused race gate passed.'
source:
    tool: codex-gpt5.6-sol2-b
    host: dragon
    episode: weave-issue-236
created: "2026-09-28T13:32:25Z"
supersedes: keep-primitive-fmt-writes-to-local-writers-in-the-interpreter
---

A scalar-only fast path is insufficient for syntax dumpers because variadic argument slices also contain positions and other values with formatting methods. Sending the original writer to fmt makes every output fragment raise a retained Write callback. Instead, omit the writer from the dependency request and authenticate a format-only redirect from Fprint* to the matching Sprint* function. The dependency still performs all user formatting callbacks. Return the resulting string and call the original writer locally. Preserve concrete builtin widths in the primitive fast path so percent-T remains correct.
