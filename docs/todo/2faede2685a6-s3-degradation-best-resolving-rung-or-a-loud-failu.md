---
id: 2faede2685a6
kind: task
title: 'S3 degradation: best resolving rung, or a loud failure'
seq: 70
status: todo
priority: p0
created: 2026-09-10T10:45:21.53684Z
sprint: 146
---

Depends on S1. Replaces the shipped refuse-before-the-body rule.

TODAY runner.go:10646 refuses a marked function called outside scope: an error
and exit 1 BEFORE the body executes. Eight of the thirteen acceptance cases
exist to pin exactly that, and they will flip.

THE NEW RULE. Agentic is enablement of features an action SUPPORTS, not a
permission gate on entering it. With agentic off:

  - a declared action runs its lower deterministic rungs and returns the first
    that resolves;
  - an action whose ONLY implementation is assisted - bashy llm with no
    provider is the canonical case - FAILS LOUDLY.

The failure is reported as no result below the agentic rung, NEVER as a
permission error. The exit status may coincide with today's exit 1; the reason
must not. An action that could have answered deterministically and instead
refused is the defect this story removes.

WHY. The refusal rule reads as arbitrary under the old permission framing and is
derived under the new one: it is simply the special case of an action with no
rung below the assisted one. It also aligns the keyword with the ladder Sprint
141 already designed (rungs 0-2 free, 3+ opt-in) without building that ladder
here - this story specifies the contract such a ladder would consume.

Preserve unchanged: an action that declares no agentic support is untouched by
any activation surface, and calling one from inside an agentic region does not
make its body assisted.

UPDATE tests/agentic/cases.tsv with S2; the outside-value, outside-interface,
outside-shell, ordinary-helper, ordinary-shell-helper, ordinary-closure,
restoration, return-restoration and source-reset cases all encode the old rule
and must be re-derived from the new one rather than edited to pass.

GATE: for each rewritten case, the recorded reason distinguishes degraded-to-a-
lower-rung from failed-with-no-lower-rung. A test that only asserts a nonzero
exit does not gate this story.

Sprint: #146
