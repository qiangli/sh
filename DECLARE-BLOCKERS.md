# declare/typeset/local/readonly/export/unset fidelity — round status

Scope of this pass was restricted to `interp/builtin.go` (+ the new
`interp/declare2_fidelity_test.go`). Of the 25 cases, only the `unset`
family routes through `builtin.go`; the `declare`/`typeset`/`local`/
`readonly`/`export` *keyword* forms are parsed as `*syntax.DeclClause`
and handled in `interp/runner.go` (`case *syntax.DeclClause:`, ~L5556),
with listing/formatting in `interp/vars.go`. Arithmetic LHS array
assignment lives in `expand/arith.go`. Those are out of scope here and
are documented below with diagnosis + (where reasonable) a verified or
proposed patch.

## FIXED in this pass (interp/builtin.go) — 2 of 25

- **builtin-vars__032** — subscript-unset of a declared-but-unset scalar.
  `unsetBuiltinArrayElem` / `unsetStringArrayElem` only allowed the
  `[0]`-aliases-the-scalar shortcut when the scalar was *set*. Bash treats
  subscript `[0]` (and any index that evaluates to 0, e.g. the unset name
  in `undef["key"]`) as the whole-variable alias and returns success even
  for a declared-but-unset scalar; only a non-zero subscript is the
  "not an array variable" error. Now both helpers test the arithmetic
  value of the (quote-stripped) subscript regardless of set-ness.

- **builtin-vars__026** — `unset %` exits 0. Bash only emits
  "not a valid identifier" for names that *look like* a botched variable
  name (lead char is a letter, digit, or underscore: `1bad`,
  `invalid-name`). A name led by other punctuation (`%`, `@`, …) is not a
  variable-name attempt: bare `unset` falls through to the function
  namespace and exits 0. Added `unsetIdentLikeStart` + a guard in the
  unset `!ValidName` branch. Regression-guarded: `unset '1bad'` still
  errors with exit 2.

## ALREADY MATCHING (no change needed)

- **assign__025** — temp-binding mutate + `unset x` reveals the global.
  Engine already produces `temp-binding / mutated-temp / (empty) / global`.

## BLOCKERS — interp/runner.go (DeclClause keyword path)

### assign-extended__012 — `declare -p name=value` → "name=value: not found"
Engine reports only `foo` ("declare: foo: not found"); bash reports the
full operand `foo=bar`. **Verified patch** (tested via gosh, full
`TestRunnerRun` stays green, then reverted):

In `runner.go`, at the top of the `if declQuery == "-p" {` block (~L5877):
```go
if as.Value != nil {
    r.errf(r.bashErrPrefix(r.curStmtPos)+"%s: %s=%s: not found\n",
        cm.Variant.Value, name, r.literal(as.Value))
    r.exit.code = 1
    continue
}
```
(`foo=bar` is rejected as a query operand before the valid-name path can
strip the `=bar`.)

### assign-extended__035 — `typeset +r r=r2` double-errors
Bash prints `typeset: r: readonly variable` **once** and leaves `r=r1`.
Engine prints the readonly error **twice** (once for the `+r` attribute
flip attempt on a readonly var, once for the rejected assignment). The
DeclClause path should emit the readonly-variable diagnostic a single
time per operand. Diagnosis: both the attribute-change branch and the
assignment branch in DeclClause hit the `readonly variable` errf for the
same `as`. Proposed fix: when the assignment to a readonly var has
already been reported (or the operand carries `+r`), suppress the second
errf. Needs runner.go.

### assign-extended__007 / __008 / __010 / __013 / __016 — listing forms
`declare` / `declare -p` / `declare -pn|-pr|-px` / `declare -pg` /
`local -p`, no-arg and braced-name forms. Multiple runner.go+vars.go gaps:
- bare `declare` (no `-p`) must list **all** variables as plain
  `name=value` (not `declare -- name="value"`), and bare `local` prints
  the function-local set in the `    local name=val;` indented form
  (assign-extended__007).
- `export -p` and `local -p` (no names) currently emit **nothing**; bash
  lists the exported / current-scope-local vars (assign-extended__008,
  the `[export]`/`[local]` sections).
- `declare -p` no-arg output is flushed out of order relative to
  surrounding `echo`s inside a `{ … } | grep` pipeline — the listing
  writer isn't interleaved with builtin `echo` output the way the
  combined stream expects (assign-extended__008).
- `declare -p test_var{0..5}` / `local -p test_var{0..5}`: the
  per-name "not found" diagnostics and the `-rx` flag-merge ordering
  for `local -p` differ (assign-extended__010).
- `declare -pn|-pr|-px` must restrict the listing to vars carrying the
  nameref / readonly / export attribute respectively
  (assign-extended__013).
- `declare -pg` inside a function must print the global-or-current
  binding as `declare -- test_var1="local"` (assign-extended__016).
All in runner.go (DeclClause query path) + vars.go (`printReadonlyVars`,
`printExportVars`, `formatDeclareVar`). Not reachable from builtin.go.

### assign__045 — `readonly -a`/`readonly -A` on an unset array
Bash prints `declare -r arr2` / `declare -r dict2` (the `-a`/`-A` flag is
dropped for a readonly array that was never assigned), while `declare -a
arr1` keeps `-a`. Engine prints `declare -ar` / `declare -Ar`. Fix is in
the attribute application (runner.go DeclClause for `readonly -a`) and/or
`formatDeclareVar` (vars.go) suppressing the array flag when the array is
readonly, unset, and empty. Out of scope.

### assign__047 — `readonly -a a` then `a+=(4)` / nameref `r+=(4)`
Appending to a readonly array (directly or through a nameref) must fail
with `a: readonly variable` / `r: readonly variable` and leave the array
unchanged. Engine path for `+=` array append and nameref-target readonly
enforcement is in runner.go. Out of scope. (Also uses Oils `argv.py`.)

### assign__014 — declaration-builtin RHS does not see the temp-env prefix
`FOO=foo readonly v2=$FOO` → bash expands `$FOO` against the **outer**
(unset) FOO, so `v2=` is empty; engine yields `v2=foo`. The temp
assignment-prefix env must not be visible while expanding a declaration
builtin's own assignment RHS. runner.go (temp-env + DeclClause). The
first half (`v=$(...)` → empty on command-not-found) already matches.
(Uses `printenv.py`.)

### builtin-vars__016 — `eval 'x=bar'` on a readonly local is non-fatal
Inside the function the readonly-violating assignment (via `eval`) must
return 1 and let the function continue (`status=1` prints). Engine treats
the readonly-assignment error as fatal and aborts the function before the
`echo status=$?`. The fatal-ness of a plain assignment to a readonly var
is decided in runner.go. Out of scope.

### builtin-vars__017 — readonly assignment + `set -e`
`readonly foo=bar; foo=eggs` under `errexit` must make the shell exit
non-zero immediately (the trailing `echo` never runs). Engine prints the
error but continues (runs `echo`, exits 0). The assignment-to-readonly
failure must propagate as an errexit trigger. runner.go. Out of scope.

## BLOCKERS — expand/arith.go

### builtin-vars__039 — `(( a[-1] = 42 ))`
The read paths (`unset a[-1]`, `${a[-1]}`, `(( last = a[-1] ))`) already
match. Only the arithmetic **LHS** assignment to a negative index fails
with "bad array subscript". In `expand/arith.go` `resolveAritLvalue`
(~L1017) a negative computed index is rejected outright; bash wraps it
to `len + index`. Proposed: when `index < 0`, resolve against the array
length (`index += len(list)`) and error only if still negative. Out of
scope (expand/arith.go).

### assign-extended__029 — `b[0+]=y` arithmetic syntax error
Bash reports `0+: arithmetic syntax error: operand expected (error token
is "+")` and still assigns `a=x`. This is an array-subscript arithmetic
parse error surfaced during a simple-command assignment word; lives in
expand/arith.go + runner.go assignment handling. Out of scope.

## BLOCKERS — syntax parser (explicitly off-limits per CLAUDE.md)

### assign__009 — `FOO=bar for i in a b; do …`
Bash is a syntax error (`syntax error near unexpected token 'do'`, exit
2). Engine accepts/handles it differently. This is a parser change in
`syntax/` which README §Caveats / CLAUDE.md says not to alter without
care. Out of scope.

## UNFIXABLE IN SANDBOX (no bash / helpers / self-exec)

- **assign-extended__004** — needs `$REPO_ROOT/spec/testdata/bash-source-2.sh`,
  `expr`, and `shopt -s extdebug` + `declare -F` source-line introspection.
- **assign-extended__005** — `$SH … extdebug.sh` self-exec; `$SH` unset here.
- **assign-extended__009** — `case $SH in bash*) exit` makes bash exit
  early (output empty); also writes/sources `tmp.bash` and re-execs `bash`.
- **assign__029** — `argv.py` (Oils helper). Engine `export`/`readonly`
  via alias should set `ex`/`ro` to `a b c`; can't assert without argv.py.
- **assign__038 / assign__040** — `stdout_stderr.py` helper; both exercise
  the "command-sub evaluated before `2>/dev/null` redirection" ordering.
  Engine prints a different command-not-found message but the
  redirect-order behavior cannot be confirmed without the helper.
