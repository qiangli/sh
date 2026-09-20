---
id: 01a0bc39-da65-7c2d-81c7-d4f56cc3bfcc
seq: 1
form: page
type: lesson
title: Preserve wide integer indices through bounds checks
description: When Go-source collection indexes or slice bounds use an integer wider than host int, retain the exact go/constant value and printable spelling through comparison and panic formatting; conversion to int is only safe after bounds validation. Route compile-time unsafe constant operators through scalar folding before generic call evaluation, and gate this widening away from Classic Bash#.
status: candidate
source:
    tool: codex-gpt5.6-sol-p
    host: dragon
    episode: weave-issue-276
created: "2026-09-20T00:31:43Z"
---
