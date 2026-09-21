---
id: 01a0c204-3429-7182-8b5a-037c9533e0e1
seq: 2
form: page
type: lesson
title: go.error adapts only the public callable surface
description: When implementing Bash++ @go.error(), keep the marker out of decorator rungs and settle body/decorator results against the original signature; append the synthetic trailing error only at the public call boundary. Existing explicit return-value lowering resets status to success, so tests that need a nonzero converted error should mark c.Status in a decorator rung instead of relying on a preceding failing shell command.
status: candidate
source:
    tool: codex-gpt-5.5-c
    host: dragon
    episode: weave-issue-3
created: "2026-09-21T03:30:50Z"
---

## The adapter only converts a completed call — never an infrastructure failure

`@go.error()` mints the trailing error from a *settled* call's status. A
decorator-machinery failure — an undefined native decorator (`Decorate` false),
or a rung that rewrites `Args`/`Results` into a shape the target rejects
(`DecoratedResults`/`DecoratedResult` false) — is not a completed call, so it
must stay a hard failure in both engines: the interpreter returns `nil,false`
and the lowered entry returns the declared zero values, including a **nil**
trailing error. Do NOT synthesize `GoError(name, Status())` on the failure
branch; that mints an ordinary `(zero, "exit status N")` pair and masks an
infrastructure failure as a normal call outcome. The result frame's absence
(the caller's `MissingResults`) already carries the failure. Guard this at the
generated-source level and with interpreted/lowered parity fixtures for the
undefined-native and invalid-Args/Results cases.

## Normalize the decorator-set status to 8-bit in both engines

`$?` is an 8-bit value. The interpreter already truncates the settled status to
`uint8`; the lowered `Decorate` must wrap `c.Status` the same way
(`shellrt.NormalStatus`) before it becomes `$?`, and the minted `@go.error`
message must use the same normalized value. Otherwise an out-of-range
`c.Status` (256, 257, negative) diverges: `$?` disagrees between engines, and a
status that wraps to zero would still read as a non-nil error. Pin 0, 255, and
out-of-range boundaries in both engines.

## A nil interface renders empty — the general rule, not an error-only seam

The interpreter interpolates every nil interface value (an unset interface
binding, a nil error) as the empty string. `shellrt.Word` must therefore render
an untyped nil interface as `""`, not a narrower error-only special case — a
narrower seam would diverge from the interpreter. A typed nil (a nil pointer,
map or slice boxed in an interface) is a non-nil interface and keeps
`fmt.Sprint`'s spelling; only the untyped nil interface is empty.
