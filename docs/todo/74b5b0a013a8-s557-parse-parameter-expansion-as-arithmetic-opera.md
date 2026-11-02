---
id: 74b5b0a013a8
kind: bug
title: S557 parse parameter expansion as arithmetic operands in sourced conditions
seq: 180
status: assigned
priority: p1
created: 2026-11-02T00:14:27.329307Z
assignee: s557-public-tooling
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

Modernish41365178630 at48af783e progresses beyondVARPREFIX but stillinitFAIL var/arith/cmp.mm76:78 notvalidarithmeticoperator `${`. Inspectpinnedfixturecde49fa37f93e943b9cb9b9536ce83836c4864ae read-only; minimalauthoredGNU5.3 differential, preservePOSIX/Bashgrammarerrors andlazyexpansion. Nofullforeignsuite/localproductbuild. Evidence/tmp/s557-modernish-41365178630. Isolatedcurrentmaster, commitnopush; rootreviewandhostedrerun.


## Delivery evidence

Arithmetic parsing rejected a shell expansion immediately after a grouped
operand, before runtime could remove an empty optional suffix. Preserve the
parenthesized arithmetic and following expansion as existing Word parts. Shell
parameter/command/arithmetic expansions remain AST nodes; static arithmetic is
literal text. The existing runtime's dynamic arithmetic reparse and parameter
snapshot logic handle evaluation, including lazy alternate bodies. No new AST
node, runtime evaluator change, or foreign fixture change was introduced.

Bounded GOWORK=off, GOMAXPROCS=2, GOFLAGS=-p=2 checks passed:
- Ten authored success cases, each also compared against GNU Bash 5.3.15:
  arithmetic expansion/command, optional empty/operator suffix, function
  comparator, nounset, arithmetic short circuit, command substitution exactly
  once and absent-branch laziness, indexed operand, and unary spacing.
- Three authored error cases also checked against Bash: nonempty invalid
  arithmetic, quoted numeric operand, and the present nested error branch.
  Each rejects without executing the following marker command.
- Sixteen parser/printer roundtrips across Bash, Bats, BashPP and POSIX retain
  explicit parameter nodes; invalid static arithmetic remains rejected.
- Existing arithmetic fidelity tests, parser success/error tables, and typed-JSON
  roundtrip passed. Focused runtime/parser checks completed in 0.857s/0.322s;
  parser tables/typed-JSON in 0.719s/0.335s.

No full suite, foreign suite, or product build ran locally. Hosted Modernish,
strict POSIX and Bash 5.3 confirmation remain parent-owned release gates.
