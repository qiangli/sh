---
id: ca1e33f51032
kind: bug
title: S557 restore Bash let shell-word and redirection parsing for Modernish
seq: 173
status: assigned
priority: p1
created: 2026-11-01T19:29:25.125635Z
assignee: s557-public-tooling
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

Sprint557 migration repair. Work only in assigned isolated workspace. Read local CLAUDE/AGENTS. Preserve all test assertions, coverage, thresholds and deadlines; diagnose before modifying. No release/push/parent pin changes. No heavy/full local builds/suites: bounded targeted tests only GOMAXPROCS=2 GOFLAGS=-p=2, full validation hosted by manager. Commit named files with Sprint: #557 and correct Story/Story-ID trailers and injected BASHY_AGENT. Report exact evidence and limitations. Modernish41336782875 BLOCKED beforetests. Sh07f84bbf syntax/parser.go letClause eagerly uses arithmExprLet: let n 1>&2 and let n >&2 and let n 2>/dev/null reject normal redirects; if false; then let .sh.subshell; fi rejects unreachable operand. Bash5.3 accepts all shell syntax. Implement correct Bash shell-word/redirection handling and defer operand arithmetic validation to execution while preserving LetClause AST/printer/runtime and mksh unquoted-parenthesis contracts. Add dialect-sensitive focused parser/printer/runtime guards. Scope parser/let runtime only; other strictgetopts/EXIT fixes alreadycommitted229299df+07f84bbf mustremain. Readonlyevidence/tmp/s557-modernish-41336782875. No foreignsuite locally.

Implementation plan and delivery evidence:

- Split only Bash-family let parsing into ordinary shell words and redirects;
  retain LetClause/ArithmExpr APIs, printer support, and mksh/Zsh arithmetic ASTs.
- Expand Bash operands as command arguments before arithmetic, retaining quote
  removal, splitting, expansion order, and single evaluation of substitutions.
  Stop at the first arithmetic error and retain the existing array diagnostic.
- Keep argument tracing faithful without evaluating operands again. Update
  Bash-only arithmetic syntax expectations to deferred runtime validation;
  retain other dialect assertions. Quote the authored parenthesized operands
  in the interpreter and serialization fixtures, as required by Bash syntax.
- Focused parser/error/printer tables, typed JSON roundtrip, 48 new Bash-family
  parser/print checks, six dialect guards, 22 runtime cases and three tracing
  cases pass. The 18 existing let interpreter cases and corresponding Bash
  comparator cases pass without changing their expected outputs. The quoted
  parenthesized input retains its original arithmetic result assertion.

Validation: bounded Go tests with GOMAXPROCS=2, GOFLAGS=-p=2, GOWORK=off and
60-second test deadlines. Final package timings: syntax 0.873s, typedjson
0.355s, interp 0.715s. The 25 new authored runtime/tracing scripts independently
match installed Bash 5.3.15. No product build or external conformance suite ran
locally; the manager must rerun Modernish and the full Bash gate on hosted
runners before acceptance. The measured Modernish failure is unchanged until
that fresh run supplies results.
