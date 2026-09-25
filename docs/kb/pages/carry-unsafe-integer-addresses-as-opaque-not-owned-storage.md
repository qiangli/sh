---
id: 01a0d616-06da-72bb-9c8c-529ec6849ae8
seq: 9
form: page
type: lesson
title: Carry unsafe integer addresses as opaque, not owned storage
description: 'When GoSource converts uintptr or reflect.Value.Pointer/UnsafeAddr results to unsafe.Pointer, nonzero integers should be represented as opaque forged addresses: they may be stored, discarded, compared, or used for arithmetic, but dereference/slice/string reads must still refuse because no interpreter-owned storage backs the raw address. Reinterpreted typed pointers may likewise be retained opaquely until dereference; validate blank-field views eagerly only for actual blank targets so raw layout structs like reflect.SliceHeader remain unsupported.'
status: candidate
source:
    tool: codex-gpt-5.5-r
    host: dragon
    episode: weave-issue-18
created: "2026-09-25T01:02:42Z"
---
