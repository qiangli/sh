---
id: 48044ef0cf4a
kind: task
title: Bash++ Go regions reject empty brace bodies
seq: 19
status: todo
priority: p2
created: 2026-09-06T10:20:50.637195Z
sprint: 115
---

Observed on sh master 586f87b6 (parse-only; no interpreter involvement).

    func f() {}              FAIL  '{' must be followed by a statement list
    go func() {}()           FAIL  '{' must be followed by a statement list
    for v := range ch {}     FAIL  bashppRange rolls back, forClause then errors
    select {}                ok    (already special-cased)

ROOT CAUSE (one, three surfaces): a committed Go region delegates its brace
body to the shell block parser, which requires a non-empty statement list.
Shell has no empty compound command; Go does. bashppRange calls p.block()
directly, so an empty body makes the whole recognizer roll back and the input
falls through to forClause, which is why the range diagnostic names 'in/do/;'
rather than the block.

'select {}' passes only because it was special-cased, so the surface is
currently inconsistent with itself.

SUGGESTED FIX: admit the empty block once at the Go-region block parser rather
than special-casing each form; that covers func bodies, go-closure bodies and
range bodies together.

COVERAGE GAP: no master test exercises an empty Go body. The malformed-input
list in syntax/bashpp_chan_test.go contains 'for x, y := range ch {\n\t}' but
that is the deliberately-rejected TWO-NAME form, and that test asserts only
termination, not a verdict.

PROVENANCE: found while checking whether killed weave run sh#51 had been
superseded. It had, except for this. Do not restore that workspace: its
bashppRange also accepted 'for v, ok := range ch', which does not compile in
Go for a channel and which master rejects deliberately.
