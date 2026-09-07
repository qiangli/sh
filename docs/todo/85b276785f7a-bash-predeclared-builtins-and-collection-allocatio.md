---
id: 85b276785f7a
kind: task
title: Bash++ predeclared builtins and collection allocation
seq: 37
status: doing
priority: p0
created: 2026-09-07T02:09:20.211004Z
weave: 29
assignee: qiangli
sprint: 116
---

First bounded Sprint 116 Story 204 slice (97322d0e5504), based on sh 844e90ee. Implement applicable predeclared builtins over the existing single runtime value model: len/cap, append (including variadic slice expansion), copy, delete, clear, make for slices/maps (channel make already exists), new integration, min/max, print/println, and deterministic arity/type/nil diagnostics. Preserve reference/value identity, named/generic collection types, readonly enforcement, positioned BashPPCall AST, Walk/Printer/typed-JSON, buffered/one-byte identity, and Classic/POSIX fallback. Audit close and panic/recover without duplicating their existing owners. Treat complex/real/imag only if the current numeric domain can represent them; otherwise record an explicit evidence-backed standing exclusion for ledger reconciliation. Cover zero/empty/nil distinctions and alias mutation. Exact gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip TestParseConfirm\|TestRunnerRunConfirm -count=1.
