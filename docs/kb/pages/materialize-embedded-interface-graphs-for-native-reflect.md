---
id: 01a0e418-c80f-76ae-918e-0268c524b37b
seq: 26
form: page
type: lesson
title: Materialize embedded interface graphs for native reflect
description: 'When interpreted values cross to native reflect.ValueOf, keep using helper-side real Go types: render ordinary embedded method-set interfaces, record their named references, and let dependency-closure registration recursively retain pointer/slice/interface cycles. Reject unions and constraint-only interfaces, and preserve pointer origins so interface assertions and map keys retain identity.'
status: candidate
source:
    tool: aurelia-s88-a
    host: dragon
    episode: weave-issue-183
created: "2026-09-27T18:20:24Z"
---
