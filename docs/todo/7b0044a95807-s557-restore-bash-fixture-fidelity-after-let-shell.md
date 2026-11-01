---
id: 7b0044a95807
kind: bug
title: S557 restore Bash fixture fidelity after let shell-word parsing
seq: 176
status: assigned
priority: p1
created: 2026-11-01T22:43:49.398523Z
assignee: s557-public-tooling
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

HostedBash5341356620602 atSh74697208 nowfails arith,array,assoc onLinux/macOS vs previous86/86. Introducedby173letWordAST change likelydiagnostic/line+assoc semantics; preservecorrectshellwordgrammar. Logs/tmp/s557-bash53-41356620602.log; downloadretainedrawartifactsforfulldiffs. Exactfixturesmustremainunchanged, nofailthresholdskips. ValidateauthoredminimalreprosagainstBash5.3andfocused existingguards, nofullforeignsuite/productbuildlocal. Workisolated74697208, coordinatealiasworkerd59 touchingbuiltin/source; ownletarithm runner/errorpaths. Selectivecommitnopush. StrictPOSIX1833/1833bothpanels nowPASSmustremain.


## Delivery evidence

Restored Bash's assignment-shaped parenthesized let operands through the existing
shell array-argument scanner; arbitrary arithmetic grouping remains outside that
shell grammar. Restored the original unquoted interpreter and typed-JSON fixtures
that story 173 had incorrectly quoted.

Preserved the original source position in let diagnostics and reject surviving
literal dollar operands without another shell expansion. Expanded associative
keys under assoc_expand_once are isolated from arithmetic syntax, then bound by
rewritten byte offset and array name to exact AST index nodes before evaluation.
Reads and writes share literal keys, including namerefs. Missing or mismatched
placeholder bindings fail closed. Other arithmetic contexts and option-unset
behavior retain their existing paths. All operands still expand before evaluation.

Bounded checks passed on the development host, GOWORK=off, GOMAXPROCS=2, GOFLAGS=-p=2:
- 12 authored comparisons against installed GNU Bash 5.3.15, including raw quotes,
  backslash, dollar/command-shaped text, UTF-8, multiple keys, nameref, short circuit,
  and the raw opening-bracket followed by a top-level dollar operand edge.
- Unconditional expectations for those 12 cases; three rejection/no-mutation cases;
  scoped raw-key/binding guards; all existing shell-word/expansion-once guards.
- 18 selected existing TestRunnerRun let cases and the same 18 canonical
  TestRunnerRunConfirm Bash comparison rows.
- Arithmetic fidelity guards, parser AST tables and let dialect/negative grammar
  guards, and typed-JSON roundtrip (regenerated golden for restored source).

No foreign fixture was executed locally. No vendor fixture was changed. Full
hosted Bash 5.3 gates remain required; this is not a conformance pass claim.
Known pre-existing diagnostic differences outside this fix: nested indexed/assoc
malformed-key wording and BashCompat let division-by-zero wording. Rejection and
no-mutation are asserted for the nested case; existing division assertions remain.


### Hosted full-shell follow-up

The prior full-shell run also exposed shfmt's flags fixture expecting `let a+`
to fail Bash parsing. GNU Bash 5.3.15 accepts authored `let n+` under -n and
reports the malformed arithmetic only on execution (status 1). Updated the
default/explicit Bash formatter expectations to success with exact output;
retained mksh rejection and POSIX successes. Focused TestScript/flags passed.

The unchanged interp Example now passes on 84cfca80. Its parenthesized assignment
also produced the documented abc123 5/global_value output in an authored Bash
comparison. No Example edits are needed. Separate hosted SDK-download stderr and
macOS chanlinear interpreter deadline failures were reported for other workers.
