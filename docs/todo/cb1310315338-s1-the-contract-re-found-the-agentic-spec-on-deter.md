---
id: cb1310315338
kind: task
title: 'S1 the contract: re-found the agentic spec on determinism, not permission'
seq: 68
status: todo
priority: p0
created: 2026-09-10T10:45:00.486687Z
sprint: 146
---

The normative artifact. Every other Sprint 146 story gates against this text, so it lands first.

REWRITE sh/docs/bashpp-agentic.md and publish a bashy-facing copy where users
of the shipped binary will see it - today the only spec lives in an OSS library
nobody installs.

REPLACE the opening frame. The current text says the modifier permits assistance
and does not require a model call. That is a statement about what the runtime
MAY do; it is untestable, and while nothing consumes the bit it leaves the
keyword meaning nothing. The new frame is a guarantee to the CALLER:

  agentic off - the action's observable result is a function of its inputs,
                including implicit ones (clock, RANDOM, environment,
                filesystem). Fix every input and the result is identical.
  agentic on  - no such guarantee, even with every input fixed.

The guarantee covers stdout, stderr AND exit status. Classic variability such as
RANDOM or date is IMPLICIT INPUT, not non-determinism of the action - say so in
those words, because it is the distinction that makes the contract falsifiable.
No stream is exempt: bashy's advisor and hints are rungs 0-2 (tables, edit
distance, failure counters) and are therefore deterministic given fixed inputs,
so they need no carve-out.

ALSO SPECIFY, each with the reasoning, not just the rule:
- the three equivalent activation surfaces and their precedence (S2);
- declaration per action, by its author, across function, method, shell
  function, block, script, compiled command, utility, tool and builtin - and
  that Bash++ compiles a script into a command, so no rule may distinguish them;
- that enabling agentic enables features in actions that SUPPORT them, and an
  action declaring none is untouched, so no caller and no environment revokes a
  determinism promise the action makes;
- degradation and loud failure (S3);
- the anti-laundering domain split (opted-in versus not);
- the recording requirement (S5) and WHY it is load-bearing;
- the defensive-authoring convention (S7);
- the command-substitution edge: an opted-in action may return model-corrected
  data straight into a script's dataflow. This is sanctioned by the opt-in and
  is the sharpest edge in the design. Write it down rather than leaving it
  implicit.

DELETE the sentence stating that BASHY_AGENTIC does not supply the source
opt-in. It is reversed by S2.

Declare the stability tier for v1.0.0. The keyword is a LANGUAGE form, so it
falls outside the per-command tier frame of the v1.0.0 checklist item 0.3 and
needs its own statement.

GATE: the doc is reviewed and every claim in it is traceable to a test that S2,
S3, S4 or S6 delivers. A claim with no test is not a specification.

Sprint: #146
