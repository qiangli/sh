---
id: 01a0c25d-7a1e-7b08-8d6e-067b52ee1373
seq: 3
form: page
type: lesson
title: Foreign error opt-in belongs in generated wrappers
description: When a typed foreign Bash# call explicitly asks for a trailing error result, emit a generated adapter beside the legacy wrapper and have call sites invoke it. The adapter is where the transport/domain split lives; only polyglot.ForeignErrorDetail failures become the status-0 error result. The undefined __bpp*_foreign* module under the execution runtime was not a declaration-order problem: the lexical storage pass sweeps every package-level var into script globals, so engine-owned module bindings must be excluded from that sweep.
status: candidate
source:
    tool: codex-gpt-5.5-j
    host: dragon
    episode: weave-issue-10
created: "2026-09-21T05:08:21Z"
---

## What happened

The first cut inlined `__bpp0_foreign0.Call(...)` at call sites and, when
that failed lexical-value checking under the execution runtime, moved the
call into a generated helper — which failed the same way, `undefined:
__bpp0_foreign0`, and so did the pre-existing one-value wrapper once any
subshell or channel activated the runtime.

## Cause

`lexicalStorage` treats every package-level `var` of the generated program
as script storage: it records the type, removes the declaration and rewrites
uses inside program-scoped bodies to lexical cells. The polyglot module
bindings (`__bpp0_foreign%d`, `__bpp0_python%d`, alias receivers) are engine
storage referenced from plain Go functions that hold no program scope, so
their uses were never rewritten while their declarations were removed.

## Fix

`foreignDeclarations` records the names it declares in `emitter.foreignGlobals`
and the storage sweep skips them. Nothing about declaration order changed.

## Contract the adapter keeps

- `value, err := f()` (one binding more than the export's results) and
  `err := f()` for a zero-result export (Python `-> None`, Rust
  `Result<(), E>`) are the only opt-in forms; the one-value form and the
  dynamic two-value form are untouched.
- Only an error carrying `polyglot.ForeignErrorDetail` is the function's own:
  it becomes the trailing error with status 0 and zero-valued results.
- EOF from a dead worker, cancellation, a response ID mismatch, a decode or
  annotation violation and launch failure stay infrastructure failures:
  `bash++: foreign call NAME failed: ERR` on stderr, failure status, zero
  results, nil error — identical in the interpreter and the lowered program.
- Under the execution runtime every result slot is recorded on every
  outcome, so the short declaration's presence check cannot report a missing
  result for a call that completed.
