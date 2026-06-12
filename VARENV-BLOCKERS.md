# varenv blockers — current ledger

Current filtered varenv diff after the 2026-06-12 pass: **88 lines** via:

```bash
ROOT=$PWD && cd external/bash-5.3/tests &&
THIS_SH=$ROOT/bin/bashy BUILD_DIR=$PWD/.. PATH=$PWD:/usr/bin:/bin:/usr/local/bin \
  $ROOT/bin/bashy ./varenv.tests 2>&1 |
  grep -av '^expect' | diff - <(grep -av '^expect' ./varenv.right) | wc -l
```

Fixed in this pass:

- `set -k` assignment-word handling and left-to-right promotion when the
  command word expands away.
- `local -` option snapshot/restore, including `IGNOREEOF` reflection.
- EXIT trap `$FUNCNAME` expansion when `exit` is called from inside a
  function.

Previously fixed in this lineage and no longer present in the filtered diff:
`export -n`, `declare -g`, `readonly -p`, `declare -I` / `local -I`, and the
basic `local -p NAME` current-scope check.

---

## 1. Temporary environment is emulated by set+restore, not a real layer (runner.go)

The inline-assignment path (`v=x cmd`, runner.go ~line 3895) writes tempenv
vars through to the enclosing scope and restores them afterwards. Bash keeps
them in a separate temporary-env layer that:

- merges into a function's local context when a declare-family builtin
  touches the name there (`z=y typeset z` → `|y|`, varenv2 fff5;
  `tempenv=foo declare -r tempenv` → persists as global with value,
  varenv7; `tempvar1=foo declare -r tempvar1` → `declare -rx tempvar1='foo'`),
- is what `unset` removes first (`x=temp unset x` inside a function leaves
  the function's `local x=local` intact → `after unset f1: x = local`,
  varenv24),
- has posix-mode propagation rules of its own (varenv12's `foo=abc`,
  `outside: declare -- var="one"`, varenv23's readonly clusters).

This needs first-class tempenv tracking (e.g. a dedicated overlay layer or a
`TempEnv` marker on `expand.Variable` set by the inline path) and is the
biggest remaining varenv cluster (~30 diff lines across varenv2/7/12/20/23/24).
Related: the in-scope round inherits `local x` values from *exported*
parents as a tempvar proxy (see setVar in interp/vars.go); once tempenv is
tracked for real, that proxy should be narrowed to actual tempvars, which
also fixes varenv7's `local: abc abc` → `local: unset1 unset2`.

## 2. Inline array-variable error wording (syntax/parser.go)

varenv13 expects per-assignment diagnostics
`` `var[0]': not a valid identifier `` / `` `var[@]': not a valid identifier ``
(and a subsequent `declare: var: not found`, plus exit status 1 surfaced as
`1`); bashy prints a single `inline variables cannot be arrays` +
`` `var[0]=X var[@]=Y f' `` pair (~10 diff lines).

This lives in `syntax/parser.go`: the parser rejects inline array assignment
prefixes before `interp` can emit bash-compatible per-assignment diagnostics.

## 3. varenv25 previous-local readonly/export edge

The current filtered diff still shows the varenv25 clusters where
`readonly`/`export` operate on variables from a previous function scope:

- expected `local -p` errors from inside `init_vars`,
- expected readonly local values after returning to `foo`,
- expected `local: int: not found` inside `init_vars2`.

This pass added `setNearestLocal` plumbing and focused synthetic coverage for
outer-local mutation, but the fixture still exercises a different command
shape and remains in the diff. Continue from `external/bash-5.3/tests/varenv25.sub`.
