---
type: lesson
title: Recover instantiated generic type at mirrored callback receiver
description: When a dependency helper materializes a local generic type under a generated helper name but callback selectors retain the generic declaration name, reconstruct the interpreter receiver from the descriptor's original instantiated wire spelling before decoding fields. Keep the base name for method lookup and use the generated identity only to select the instantiation; otherwise callbacks fail with no interpreter representation even though registration succeeded.
status: validated
evidence: Outside-corpus generic callback differential reproducer passes with exact native output after descriptor-based receiver reconstruction; reference-bearing negative remains refused.
source:
    tool: claude-t
    host: dragon
    episode: weave-issue-176
created: "2026-09-13T03:55:48Z"
updated: "2026-09-13T03:55:52Z"
---
