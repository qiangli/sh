---
id: e0b008e71503
kind: task
title: 'S2 activation: three equivalent surfaces with one precedence rule'
seq: 69
status: todo
priority: p0
created: 2026-09-10T10:45:00.509126Z
sprint: 146
---

Depends on S1. Reverses a shipped rule, deliberately.

TODAY BASHY_AGENTIC is a master KILL only (see agentos.advisorEnabled, which
calls agenticDisabled first), and the spec states outright that it does not
supply the source opt-in. That makes it the one control in bashy shaped
differently from BASHY_ADVISOR, BASHY_HINTS and BASHY_OUTPUT_REDUCE, each of
which accepts environment, flag and explicit forms as spellings of one control.
The inconsistency is unjustified and is removed.

DELIVER three equivalent activation surfaces seeding ONE scope value:
  environment  BASHY_AGENTIC
  command line --agentic
  source       the agentic keyword, including a whole file whose body is one
               agentic block

PRECEDENCE, following the advisor's existing shape: nearest explicit setting
wins; environment is the outermost default; an explicit off beats an ambient on.
Keep a hard off reachable.

The environment surface is what lets a script author enable assisted behavior
without prefixing every command, which was the ergonomic complaint the block
form only partly answers. Its blast radius stays bounded by DECLARATION (S3):
an action that declares no agentic support is untouched no matter what the
environment says.

UPDATE tests/agentic/cases.tsv in bashpp-tests together with this change - the
current cases pin the reversed rule. Coordinate the two commits; do not land one
half.

GATE: a negative test proving an explicit source off is not overridden by
BASHY_AGENTIC=true, and a positive test proving the environment alone enables a
declared action with no source marking. Both under --posix must be inert.

Sprint: #146
