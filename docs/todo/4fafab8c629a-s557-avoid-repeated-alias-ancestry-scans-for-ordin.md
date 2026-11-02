---
id: 4fafab8c629a
kind: bug
title: S557 avoid repeated alias ancestry scans for ordinary sourced command words
seq: 185
status: assigned
priority: p1
created: 2026-11-02T02:38:26.863329Z
assignee: s557-manager
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

Root-delegated performance repair after c0e71e66: unchanged TestSourceGrammarAliases/many-independent 1030alias race case exceeds its existing5s deadline. Compare preclosing-alias source via overlay; preserve deadline/count/assertions and lexer-owned source parsing, no foreign execution. Suspected ordinary nonalias words scan growing ancestry ranges on every full reparse. Implement only evidence-backed no-op fast path, bounded authored race tests; parent reviews/integrates, no push. Internal helper repair_review.


Delivery evidence (internal Codex repair_review helper, manager owns review):

- Current c0e71e66 unchanged authored many-independent case failed under race in6.92s; genuine pre184 source_alias.go overlay at a007e5be failed5.29s. This performance defect predates closing-alias support. An initial overlay used noncanonical macOS /tmp keys and did not apply; that observation was discarded and rerun with resolved /private/tmp keys.
- Source reparses after each grammar alias. Every previously expanded ordinary command word scanned every accumulated alias-ancestry span, adding avoidable cubic work to quadratic reparsing. Parsing does not execute alias definitions/removals; ancestry names are populated only from the fixed alias map. An immediate nonmember return safely bypasses both ancestry suppression and resolution for ordinary words.
- Production change is eight lines including rationale; no parser rewrite, alias timing changes, new concurrency, test deadline/count/assertion changes, or grammar broadening. Remaining reparsing is quadratic; this is not an asymptotic linearity claim.
- Initial fast-path observations included one5.43s timeout; timing profile sampled1.45CPU seconds over5.57wall and does not establish a universal host-load bound. Subsequent timing-only overlay measured GNU oracle6.6ms, Runner ready7.1ms and interpreter completion1.208s. Temporary overlay/profile artifacts remain outside source delivery.
- Exact root focused race selection PASS2.989s: GOWORK=off GOMAXPROCS=2 GOFLAGS=-p=2 SH_TEST_BASH_REFERENCE=<installed GNU Bash5.3.15> go test -race ./interp -run 'TestSourceGrammarAlias|TestSourceAliasReferenceIdentity|TestCommandSubstitutionExitTrap(Cancellation|Panic)|TestPhaseDiagnosticCanceledBeforeSnapshot' -count=1 -timeout=60s. Existing alias recursion, literal/heredoc, read-before-execute and clause guards remain intact.
- Final unchanged many-independent race check count3 PASS4.886s total, each case1.15s against its original5s deadline and1030aliases. No new test mirrors this simple fast path; existing authored case is the regression and old-source negative control fails.
- No full local build/suite or foreign test execution. Parent independently reviews/integrates; no push by helper.
