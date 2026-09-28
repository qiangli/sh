---
id: 01a0e854-e051-70e6-9674-e4b02f38c0f6
seq: 30
form: page
type: lesson
title: Materialize dependency collection carriers on every typed read path
description: When a dependency-owned slice or array is assigned into interpreter-owned typed storage, selector, index, slice, and identifier reads must all materialize the native carrier after an authenticated assignability check. Handling only identifier temporaries leaves selector fields with nil collection metadata and falsely reports BASHPP-ECOLLECTION-ELEMENT for identical imported collection types; never reconstruct under the destination type before checking the carrier's actual native type.
status: candidate
source:
    tool: codex-gpt5.6-sol-w242-h
    host: dragon
    episode: weave-issue-242
created: "2026-09-28T14:04:31Z"
---
