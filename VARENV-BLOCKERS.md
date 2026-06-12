# varenv blockers — current ledger

Current filtered varenv diff after this pass: **38 lines** via:

```bash
ROOT=$PWD && cd external/bash-5.3/tests &&
THIS_SH=$ROOT/bin/bashy BUILD_DIR=$PWD/.. PATH=$PWD:/usr/bin:/bin:/usr/local/bin \
  $ROOT/bin/bashy ./varenv.tests 2>&1 |
  grep -av '^expect' | diff - <(grep -av '^expect' ./varenv.right) | wc -l
```

Fixed in this pass:

- varenv25 previous-local `readonly`/`export`: these now mutate the nearest
  local in a caller frame without copying it into the callee, and `local -p`
  prints only the current function frame's binding.
- Inline `declare/typeset -r` temp values now persist where bash keeps them,
  and the simple-command `declare/typeset` fallback handles `-r`.
- `${var@A}` now uses bash's single-quoted value form.
- `typeset NAME` inside a function now materializes a local from an inline
  temporary value (`z=y typeset z`).

Previously fixed in this lineage and no longer present in the filtered diff:
`export -n`, `declare -g`, `readonly -p`, `declare -I` / `local -I`, and the
basic `local -p NAME` current-scope check, `set -k` assignment-word handling,
`local -` option snapshot/restore, and most EXIT trap `$FUNCNAME` expansion.

---

## 1. Temporary environment is emulated by set+restore, not a real layer (runner.go)

The inline-assignment path (`v=x cmd`, runner.go ~line 3895) writes tempenv
vars through to the enclosing scope and restores them afterwards. Bash keeps
them in a separate temporary-env layer that:

- merges into a function's local context when a declare-family builtin
  touches the name there; the simple `z=y typeset z` and `declare -r`
  cases are fixed, but the remaining `local`/unset variants still need a real
  layer,
- is what `unset` removes first (`x=temp unset x` inside a function leaves
  the function's `local x=local` intact → `after unset f1: x = local`,
  varenv24),
- has posix-mode propagation rules of its own (varenv12's `foo=abc`,
  `outside: declare -- var="one"`, varenv23's readonly clusters).

This needs first-class tempenv tracking (e.g. a dedicated overlay layer or a
`TempEnv` marker on `expand.Variable` set by the inline path) and is the
biggest remaining varenv cluster (~24 diff lines across varenv12/20/23/24).
Related: the in-scope round inherits `local x` values from *exported*
parents as a tempvar proxy (see setVar in interp/vars.go); once tempenv is
tracked for real, that proxy should be narrowed to actual tempvars, which
also fixes varenv7's `local: abc abc` → `local: unset1 unset2`.

## 2. EXIT trap through eval/heredoc parse path

One varenv22 path still installs `trap 'echo trap:' EXIT` instead of preserving
`$FUNCNAME`:

```bash
${THIS_SH} << \EOF
eval "trap 'echo trap:\$FUNCNAME' EXIT ; trap; f() { exit; } ; f"
EOF
```

Direct heredoc/eval reproductions now pass, so continue from the nested
`${THIS_SH}` invocation in `external/bash-5.3/tests/varenv22.sub`.
