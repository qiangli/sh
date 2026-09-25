---
id: 01a0d960-5a38-716f-b978-516f67573481
seq: 13
form: page
type: lesson
title: Register concrete generic bridge type identities
description: When a Go-source program mentions an instantiated generic type across the native bridge, register the concrete type expression in the helper with reflect.TypeFor under the bridge spelling before requests resolve it. Skip instantiations already owned by local type descriptors, and skip type-parameter forms, or the helper can fail with duplicate map keys or uncompilable Box[T] registrations.
status: candidate
source:
    tool: codex-gpt-5.5-c
    host: dragon
    episode: weave-issue-3
created: "2026-09-25T16:22:45Z"
---
