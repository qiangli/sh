---
id: bc2580329a94
kind: bug
title: S557 preserve variable-prefix expansion under nounset for Modernish
seq: 178
status: wontfix
priority: p1
labels:
    - superseded
created: 2026-11-01T23:52:56.251191Z
assignee: s557-manager
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
closed: 2026-11-02T05:32:09.018663Z
---

Modernish41362682731 atSh8d8ce12b progressed pastforevergrammar but initstops VARPREFIX.tline19 _Msh_test_ unboundvariable,exit1,0tests. Diagnose minimal parameterprefix query undernounset againstBash5.3; preserve real unseterrors. No localforeignsuite/productbuild. Evidence /tmp/s557-modernish-41362682731 retained. Boundedauthoredtests and hostedModernishrerun required. Existing175sourcealiasdelivered; distinctparameterexpansionrootcause.


## Delivery evidence

Implemented by s557-public-tooling as the manager-authorized internal helper;
story ownership remains s557-manager. Name enumeration no longer checks the
prefix variable value under nounset. Invalid forms continue through existing
bad-substitution checks; ordinary direct/indirect unset failures are preserved.
Quoted @ with zero matches now returns a non-nil empty field list, preventing
fallback to one empty scalar. Prefix * assignment values use the first IFS
character, matching the existing indexed/associative key-list behavior.

Bounded tests passed with GOWORK=off, GOMAXPROCS=2, GOFLAGS=-p=2:
- TestPrefixNamesNounset: 16 unconditional authored cases, also compared against
  GNU Bash 5.3.15 using SH_TEST_BASH_REFERENCE. Covers @/* quoted/unquoted,
  zero/multiple matches, assignments, custom/empty IFS, nameref prefixes with
  unset targets, and irrelevant prefix variable values.
- TestPrefixNounsetErrorBoundaries: direct/indirect unset errors and invalid
  prefix substitutions remain distinct failures.
- Existing TestFieldsUnsetIndirectArrayKeys and
  TestBashSourceTopLevelAndNounsetDefault passed.

No foreign fixture or full product build ran locally. Hosted Modernish and
release conformance gates remain required; parent owns review/publication.


### Review follow-up: invalid quoted names

Parent review identified that the non-nil zero-field result must not bypass
validation for `${!1@}`. Added explicit name validation before both quoted prefix
paths. Four authored quoted-invalid cases (@/*, nounset on/off) now fail with bad
substitution before subsequent commands, also confirmed against GNU Bash 5.3.15.
All 20 prefix differential cases and existing error-boundary tests pass.

## Superseded by consolidation — 2026-11-02

Operator requested consolidation of implementation repairs into acceptance owners. Scope, original acceptance, unresolved work and evidence transfer to public umbrella #6, story `bf7624a5c2f2`. This record is closed by decision with label `superseded`; it is NOT a delivery acceptance, test pass, waiver or feature retirement. The parent remains open. Continue corrections under that parent rather than creating another repair story. Original content above is retained unchanged.
