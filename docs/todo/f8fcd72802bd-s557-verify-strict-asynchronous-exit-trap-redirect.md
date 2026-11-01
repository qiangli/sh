---
id: f8fcd72802bd
kind: bug
title: S557 verify strict asynchronous EXIT trap redirection lifetime
seq: 172
status: assigned
priority: p1
created: 2026-11-01T18:26:27.416844Z
assignee: s557-public-tooling
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

Strict cmd/sh Yash41335572423 measures1831/1833; trap-p166 background trap builtin with command-local stdout redirection then wait losesEXIToutput. Verify pinnedfixture and primaryPOSIX redirection/trap semantics beforebehaviorchange. Distinguish strict profile fromBashcompatibilityreference (which sharesfailure). Preservecommand-lifetimeFDscope, inheritedtrapreset, process/cancellationownership and Bash86compatibility; add directasync/simplecommand/subshell/redirect/nestedtrap cases. No fixturealteration/score reclassification. BoundedlocalGo tests only, foreignsuite hosted. Rootownsreview/push.

Normative basis: POSIX.1-2024 trap requires the EXIT action to use the shell
state after the preceding command, and its asynchronous-list section executes asynchronous
AND-OR lists in a subshell. Primary source:
https://pubs.opengroup.org/onlinepubs/9799919799/utilities/V3_chap02.html#trap
https://pubs.opengroup.org/onlinepubs/9799919799/utilities/V3_chap02.html#tag_19_09_03_02
Pinned Yash trap-p:166 expects the EXIT output after a background trap builtin
finishes its command-local stdout redirection; ordinary Bash 5.3 suppresses it.

Implementation: restrict the existing explicit Bash POSIX-mode /dev/null
compatibility workaround to the non-strict profile. Strict mode uses the
already-restored command descriptors. No descriptor restoration, asynchronous
completion, trap inheritance, process ownership, or cancellation code changed.

Validation: three strict cases failed before the change and pass afterwards.
Nine cases run in both profiles cover foreground/simple/function, asynchronous
simple, outer subshell redirection, persistent exec redirection, inherited/reset
traps, nested command-substitution traps, and parent output. All nine Bash-profile
cases match installed Bash 5.3.15. These plus existing command-substitution,
cancellation/panic, and strict trap-status regressions pass in 0.430s under a
90-second bound. Foreign Yash/modernish/full Bash86 remain hosted-only.
