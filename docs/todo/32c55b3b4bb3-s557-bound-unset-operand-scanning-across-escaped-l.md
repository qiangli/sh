---
id: 32c55b3b4bb3
kind: bug
title: S557 bound unset operand scanning across escaped line endings
seq: 170
status: assigned
priority: p1
created: 2026-11-01T18:17:57.62975Z
assignee: s557-public-tooling
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

Hosted modernish41335570134 verifies EXIT trap repair then exposes panic in scanUnsetOperandSources: physical-line extraction ends at trailing backslash and scanner advances beyond len before slicing. Fix bounds and continuation handling needed for correct once-expanded quoted array operand classification; preserve existing unset/array quote semantics. Add direct scanner truncated/quoted/unquoted/escaped-end tests plus authored multiline shell regressions against Bash reference. No panic suppression, skip or fixture alteration. Bounded local Go tests only; foreignmodernish/fullBash86 hosted. Root owns review,push,pins.

Implementation: let the operand scanner stop at the logical command line,
rather than truncating the source at a physical newline. Skip escaped newlines
between words without creating an operand, retain quoted-token classification,
and bound both ordinary and double-quoted escape advancement at end of input.

Validation: 14 direct scanner boundary/classification cases and 5 authored
multiline interpreter cases pass alongside existing TestQuoteArrayFidelity
(0.403s, 90s process bound). All five multiline cases also match installed Bash
5.3.15, including a quoted associative-array operand before and after another
operand. bash formatter, diff and public scrub checks passed. No foreign
modernish suite or full product build ran locally; hosted gates remain pending.

Review follow-up: scan the borrowed source byte slice directly, avoiding a
whole-script-tail string conversion per unset. Only an individual token is
converted for command-name comparison. An allocation regression exercises the
actual Runner source accessor with empty and 4 MiB irrelevant tails: both use
zero allocations for an operand-free unset. All scanner/continuation/quote
checks still pass (0.473s). No unsafe conversion or second lexer was added.
