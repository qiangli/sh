---
id: 88106d43e123
kind: task
title: Bash++ named composite types and Story 201 closure audit
seq: 31
status: done
priority: p1
created: 2026-09-06T22:57:32.590866Z
assignee: codex-gpt5.6-sol
sprint: 116
closed: 2026-09-06T23:36:23.817882Z
---

Final bounded closure slice for Sprint 116 Story 201 (393c10564e10), on fea9a161. Audit and close source-reachable named and alias composite/pointer semantics: definitions and aliases whose underlying types are arrays, slices, maps, structs, or pointers; assignment/identity and comparability rules; zero values; new(T); pointer selector/index/slice auto-dereference; and conversions or deterministic rejection where Go requires distinct defined-type identity. Support constant-expression array lengths and validate inferred-array compatibility. Extend slicing/range/equality coverage to dereferenced, selected, indexed, and composite-literal operands wherever the committed typed grammar owns the source; reject unsupported forms deterministically without weakening Class-E fallback. Produce an explicit Story 201 closure matrix and transfer only builtin-owned make/append/copy/delete/len/cap work to Story 204. Preserve readonly paths, task/subshell snapshots, positioned lowering-ready AST, Walk/Printer/typed-JSON exact round trips, buffered/one-byte identity, and Bash/POSIX/classic behavior. Gate: PATH=/bin:/usr/bin:/opt/homebrew/bin go test ./syntax ./syntax/typedjson ./interp -skip 'TestParseConfirm|TestRunnerRunConfirm' -count=1. Required trailers: Sprint: #116; Story: #201; Story-ID: 393c10564e10.

## Story 201 Audit Matrix

Closed in this slice:

- Parser ownership: `type T [N]E`, `type T []E`, `type T map[K]V`, `type T *E`, aliases over those forms, literal arithmetic array lengths, and declared scalar constant length names are classified into positioned typed AST and retain Printer/Walk/typed-JSON round-trip coverage through the existing typed nodes.
- Named and alias composites: defined and alias types over arrays, slices, maps, and structs resolve to their underlying operational shape while retaining named identity for assignment and comparison checks.
- Named and alias pointers: defined and alias types over pointers resolve to pointer storage for source-owned declarations, typed initialization, dereference, mutation, nil comparison, and pointer-to-pointer zero values while retaining defined-type identity for assignability checks.
- Zero values: named arrays, slices, maps, structs, and pointers preserve Go zero-value behavior for source-owned typed forms.
- `new(T)`: named arrays, slices, maps, structs, and pointer types allocate addressable zero-valued storage through the positioned `BashPPNewExpr` path.
- Assignment and identity: unnamed-to-named and named-to-unnamed composite and pointer assignment is accepted where Go assignability permits it; distinct defined composite or pointer types are rejected deterministically unless a supported explicit conversion is added in a later conversion slice.
- Operations: pointer selector/index/slice auto-dereference for direct pointer expressions, selected/indexed/sliced operands, composite-literal indexing, array/struct equality, slice/map nil-only equality, and typed range over named, selected, and dereferenced collections are covered.
- Inferred arrays: `[...]T` is accepted when its inferred length matches an expected `[N]T` and rejected with a deterministic length diagnostic when it does not.

Transferred to Story 204 only:

- Builtin-owned allocation and mutation helpers: `make`, `append`, `copy`, `delete`, `len`, and `cap`.

Explicitly outside this task's committed source surface:

- Shifted and bitwise constant declarations whose shell-token spelling is not yet owned by the typed declaration grammar remain outside source ownership.
- Explicit conversions between distinct defined composite types are not implemented in this source slice; unsupported conversion call shapes continue through the existing fallback surface unless and until a committed typed conversion grammar owns them.
