---
id: 01a0da0c-b857-77d3-b728-57c5c9387e50
seq: 17
form: page
type: lesson
title: Synthesize imports for generated helper type spellings
description: When GoSource worker source synthesizes type declarations, signatures, trampolines, or type registrations from export-data spellings, scan those generated type positions for package-qualified selectors not present in the original import table and add named helper imports for same-name packages through the selected Go toolchain. Otherwise signatures such as strings.SplitSeq's iter.Seq result can leave generated code with an undefined qualifier, and generic type-only packages must not be blank-imported merely because they contribute no symbol-table entry.
status: candidate
source:
    tool: codex-gpt-5.5-m
    host: dragon
    episode: weave-issue-13
created: "2026-09-25T19:31:01Z"
---
