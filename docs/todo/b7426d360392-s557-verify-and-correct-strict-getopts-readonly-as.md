---
id: b7426d360392
kind: bug
title: S557 verify and correct strict getopts readonly assignment status
seq: 171
status: wontfix
priority: p1
labels:
    - superseded
created: 2026-11-01T18:26:27.231455Z
assignee: s557-manager
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
closed: 2026-11-02T05:32:10.253973Z
---

Strict cmd/sh Yash41335572423 measures1831/1833 in bothLinuxcontainerpanels. getopts-p287 readonlyOPTARG emitsdiagnostic butreturns0 wherefixtureexpectsnonzero. Verify pinnedfixture and primaryPOSIX requirements beforebehaviorchange; retainBashcompatibilityprofile and unrelatedgetopts/end-of-options semantics. Add strict/default authored regressions with readonlyOPTARG/OPTIND/erroroptions asrelevant. No score reclassification or weakenedassertions. BoundedGo checks local; foreignsuite onlyhosted. Rootreviews/pushes.

Normative verification: POSIX.1-2024 XBD 8.1 explicitly lists OPTARG and OPTIND
among variables manipulated by builtins. If readonly accepts them, a builtin
must either continue modifying them or fail when modification is refused:
https://pubs.opengroup.org/onlinepubs/9799919799/basedefs/V1_chap08.html
The getopts exit-status contract assigns 0 to an option found, 1 to end of
options, and greater than 1 to errors:
https://pubs.opengroup.org/onlinepubs/9799919799/utilities/getopts.html
Pinned Yash 0105ae707bce655034522372f8b2a20072819c28 getopts-p.tst:287 tests
these alternatives. Current strict output diagnosed refusal but returned 0.

Delivery preserves ordinary Bash compatibility while returning 2 in strict
mode for rejected readonly output-variable writes. It includes a required but
empty OPTARG assignment and a rejected clear of a previously set OPTARG;
unset unused readonly OPTARG is left alone. Ordinary end-of-options remains 1.
The authored Go regression covers 13 cases in strict and compatibility profiles
(26 subtests), including OPTIND, destination variable, silent option errors,
retained readonly values, and prior-status isolation. Focused test passed in
0.401s. No local foreign suite or full suite ran; hosted Yash acceptance remains
pending manager review and dispatch. No Bash reference score is reclassified.

## Superseded by consolidation — 2026-11-02

Operator requested consolidation of implementation repairs into acceptance owners. Scope, original acceptance, unresolved work and evidence transfer to public umbrella #6, story `bf7624a5c2f2`. This record is closed by decision with label `superseded`; it is NOT a delivery acceptance, test pass, waiver or feature retirement. The parent remains open. Continue corrections under that parent rather than creating another repair story. Original content above is retained unchanged.
