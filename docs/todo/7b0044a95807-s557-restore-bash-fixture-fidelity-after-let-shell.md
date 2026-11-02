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


### Retained Bash 5.3 arithmetic/array follow-up (internal helper, s557-manager)

Run 41365164988 retained exactly one Unix arithmetic difference (quoted-space
indexed let assignment incorrectly accepted) and one array difference (expanded
command-shaped indexed let subscript executed twice instead of being refused).
The repair checks indexed subscripts lazily before read/lvalue expansion when
expansion-once is enabled. Associative keys retain their literal-key path;
short-circuited branches remain unevaluated. Exact expanded subscript source is
retained through arithmetic reparse for Bash diagnostics. A typed index error
uses the existing input-line discard mechanism, preserving next-line recovery.
The obsolete eager quote-only special case is removed; it also incorrectly
rejected a valid associative empty-quoted key.

Both option names share one canonical setting, matching GNU Bash alias behavior
and preserving last-toggle semantics. Shopt and BASHOPTS enumeration read that
same setting. No syntax/parser files or foreign fixture bytes were changed.

Fourteen authored cases compared successfully with installed GNU Bash 5.3.15,
including both retained failures, read/write refusal, backticks, literal dollar
operand, no execution in short-circuit/ternary branches, option-off behavior,
associative quoted keys, and unchanged indexed value after next-line recovery.
The prior 12 let differential cases and shell-word/raw-key/array/quote guards
also passed in the focused selection (0.620s). No foreign suite was run locally.
The separate Windows printf one-second start-time mismatch was reported to its
assigned worker and remains outside this repair.

Negative control: an uncommitted Go source overlay restoring23fcb1ec production
files made both authored retained-failure cases fail (0.396s): quoted-space was
accepted silently, and command-shaped subscript emitted BADBAD.

Rebased onto ed938a78. The final focused interpreter selection including
story180 optional-suffix tests passed (0.655s); expand let/arithmetic guards
passed (0.272s). Original assertions and ten-minute hosted gate remain intact.

Independent boundary follow-up adds both alias-toggle orders and listing checks,
plus a recursive arithmetic-value diagnostic. Twenty authored indexed cases
passed against GNU Bash (0.500s). Raw source is bound to exact AST index nodes,
so offsets from a recursive parse cannot address an enclosing expression.
The expanded selection with prior let/array/quote and optional-suffix guards
passed (0.682s); expand arithmetic/let guards passed (0.334s).
