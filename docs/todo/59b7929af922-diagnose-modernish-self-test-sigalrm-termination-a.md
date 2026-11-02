---
id: 59b7929af922
kind: task
title: Diagnose Modernish self-test SIGALRM termination after successful initialization
seq: 188
status: assigned
created: 2026-11-02T03:02:03.436993Z
assignee: s557-test-readiness
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

Hosted Modernish run 41380385436 at umbrella ed2b89b4, Bashy c9cf91cb, Sh13838f82 completes init/version with exit0 but self-test exits142 with no summary. Preserve the foreign fixture, all assertions and timeout. Read-only pinned-source analysis and bounded authored reproductions first; distinguish measured zero counters from unknown partial execution. No local foreign suite/full builds. Manager owns integration and hosted acceptance.

Delivery: hosted FD9 trace 41384053400 identifies BUG_TRAPFNEXI capability
checking. The command substitution installs an ALRM trap, sends ALRM to its
advertised BASHPID, resets the trap, then installs an exiting trap and sends
again. Top-level identity105 and virtual child106 show the child identity
escaping to kernel kill: only $$ was recognized as local. The first callback
arrives after reset; the second send terminates the product. Initialization
passes separately, but the original self-test remains exit142 with no summary.

The kill builtin now recognizes the current runner's advertised virtual
BASHPID before OS fallback. It queues the locally installed trap synchronously,
retains inherited ignores, defaults inherited caught traps to local termination,
and leaves $$ ancestor routing intact. SIGKILL bypasses callback/ignore state
and removes local EXIT cleanup. Zero probes succeed without kernel delivery.
Foreign fixtures, suite commands, deadlines and result counters are unchanged.

Authored GNU5.3 differential coverage exercises trap replacement and function
exit, default termination, catchable EXIT cleanup, uncatchable KILL, inherited
ignore/caught reset, ancestor $$ and nested command substitutions. Direct
routing guards never signal foreign targets; differential runners use a high
synthetic identity range rather than adjacent real Linux/macOS process IDs.
Per-case OS signal disposition restoration prevents the ignore fixture from
contaminating repeated runs. Earlier repeated validation exposed that test
cleanup omission; the fixture was corrected without changing runtime policy.

Scope limits: this does not allocate unique kernel-backed subshell identities,
make external kill interoperable with synthetic BASHPID, or complete process
emulation. Existing STOP default no-op and CHLD/URG subscription limitations
remain. Safe identity-only diagnostics exposed sibling synthetic-ID reuse as
separate pre-existing debt. Hosted unchanged Modernish rerun remains necessary.

Final bounded validation: the eight authored comparisons plus direct routing
guards and existing real-signal/existence/zero-target/queued-callback/
uncatchable/ignore-to-trap guards passed with the race detector, count3,
60-second package limit (20.523 seconds). No local foreign suite or full
product build ran. Independent review required KILL and EXIT edge corrections;
those are included and covered in this final result.
