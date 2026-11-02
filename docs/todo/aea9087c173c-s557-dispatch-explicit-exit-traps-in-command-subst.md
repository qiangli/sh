---
id: aea9087c173c
kind: bug
title: S557 dispatch explicit EXIT traps in command substitution for modernish initialization
seq: 169
status: wontfix
priority: p1
labels:
    - superseded
created: 2026-11-01T18:07:39.126008Z
assignee: s557-public-tooling
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
closed: 2026-11-02T05:32:10.543856Z
---

Contained pinned modernish diagnostic41334633731 identifies finalFTL_SUBSHTRAP probe: command substitution installs EXIT trap producing PPID marker; Bash5.3 emits marker, Bashy omits it and loader exits128. Reproduce minimal direct and sourced-file substitutions against Bash5.3; repair explicit trap dispatch without changing inherited EXIT semantics, status, output capture or cancellation ownership. Preserve86Bash fixture gate, add focusedpositive/negative regressions, boundedlocalfocusedtests only. Foreignmodernish execution hosted/container only. Root owns push and final pins/full gates. Coordinate runner files with runtime168 workers.

Implementation: dispatch a locally installed EXIT trap after command-substitution
statements finish, before closing captured output or clearing subshell exit flags.
Inherited parent traps stay inactive. Cancellation and panic unwinding do not
start trap commands; the existing trap evaluator preserves status unless the
trap explicitly exits with a new status.

Validation: nine authored snippets match installed Bash 5.3.15 (no foreign suite
execution). Before the fix, seven focused cases reproduced missing trap output.
After the fix, natural/explicit exit, entry status, status preservation/override,
inherited/reset traps, nested and sourced substitutions pass. Cancellation and
panic ownership tests pass. Nearby existing trap, alias and redirect tests pass
in the same bounded interp invocation (0.427s). Full Bash86 and modernish checks
remain hosted and manager-owned; no full local suite or product build ran.

## Superseded by consolidation — 2026-11-02

Operator requested consolidation of implementation repairs into acceptance owners. Scope, original acceptance, unresolved work and evidence transfer to public umbrella #6, story `bf7624a5c2f2`. This record is closed by decision with label `superseded`; it is NOT a delivery acceptance, test pass, waiver or feature retirement. The parent remains open. Continue corrections under that parent rather than creating another repair story. Original content above is retained unchanged.
