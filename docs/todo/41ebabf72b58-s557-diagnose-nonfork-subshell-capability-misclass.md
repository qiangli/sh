---
id: 41ebabf72b58
kind: task
title: S557 diagnose nonfork subshell capability misclassification
seq: 190
status: assigned
priority: p1
created: 2026-11-02T04:22:01.021969Z
assignee: s557-test-readiness
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

Modernish41387486509 at umbrella6b423d63, Bashy69c1bfe9/Shdc51034c passes prior signal and export-alias blockers, but now exits128 in sanitycheck003: NONFORKSUBSH on non-ksh93. Preserve all fixtures and verdicts. Read exact capability discovery and sanity predicate before attribution. Earlier safe identity probe showed sibling command substitutions share depth-based BASHPID; investigate whether this causes current classification. Use authored GNU5.3 differential reproduction, no local foreign suite or unsafe OS signals. If identity repair is necessary, keep it bounded, preserve $$ ancestor semantics, deterministic mode, self-signal routing and existing process ownership. Do not introduce a broad process registry without evidence. Root owns integration and hosted acceptance; new story separate from188/189 already resolved mechanisms.

Diagnosis: the classification is accurate, not a synthetic BASHPID collision.
Pinned Modernish NONFORKSUBSH compares OS parent identities observed by an
external known-good shell launched from a command substitution and a nested
parenthesized subshell. It never reads BASHPID. Both children have the same
real parent in the interpreter's existing goroutine subshell model. Sanity
check003 rejects this capability without the ksh93 .sh.subshell extension.
No fake ksh93 compatibility, fixture change or process-registry rewrite was
introduced.

Baseline comparison: frozen legacy3e7ffaafd87a833663df198164a8d83597bdfd0b
pins Sh6663c93a2d3cd3528a8d270080cfb10ead828135. An identical authored, safe
external-parent differential probe ran against that commit in an isolated
public checkout and current Shdc51034c. Both report same OS parent, while
GNU5.3 reports distinct parents. Baseline fails the differential in0.449s;
current fails identically in0.560s. This establishes unchanged behavior for
this architectural limitation, not a POSIX acceptance result. No signals,
foreign suite or full product build ran locally; no frozen files were changed.

Gate classification is also unchanged: frozen Bashy558ff064b2fdb31b4972629dde931007e03f8c4f
and the current runner both list @modernish as INFO in posix-certdryrun.sh,
and modernish-suite.sh explicitly describes informational relative results
rather than a zero-failure gate. This was not newly reclassified to hide a
failure. Shell-only POSIX acceptance remains an independent requirement.

Hosted41387486509 original self-test remains exit128 with no complete summary;
initialization/version exits0. The separate trace records eleven sanity cases:
nine okay, one failure NONFORKSUBSH on non-ksh93, one missing-tput warning.
There is no full389-case result or suite-pass claim. Previously repaired signal
and export-alias checks progressed; this later pre-existing host-model limit
is disclosed and retained. Further process-parent emulation would require a
separate architectural scope and lifecycle validation.
