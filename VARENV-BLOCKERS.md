# varenv blockers — verified fixes outside interp/vars.go + expand/environ.go

Scope for this round was `interp/vars.go`, `expand/environ.go`, and their
tests. The items below were diagnosed against `varenv` (and reproduced with
`bin/bashy` directly) but live in out-of-scope files. Diff-format patches are
included where the fix was verified by hand; larger items carry a diagnosis
and pointers.

Current varenv diff after the in-scope round: 199 lines via the raw repro
(`$BASHY ./varenv.tests 2>&1 | diff - ./varenv.right | wc -l`), 161 via the
suite's own `run-varenv` (which pipes through `grep -v '^expect'`).

---

## 1. `export -n NAME` converts the variable into a nameref (runner.go)

**Repro:** `bin/bashy -c 'export var=10; export -n var; declare -p var'`
prints `declare -nx var="10"`. Bash prints `declare -- var="10"`: `-n` for
`export` means "remove the export attribute", but the shared declare-family
flag parser maps every `-n` to the nameref valType.

This is the root cause of the varenv1 mismatch
(`declare -nx var="10"` vs expected `declare -x var="60"`): after the bogus
nameref conversion, the later `var=60 export var` retargets the nameref
instead of assigning var.

**Patch (interp/runner.go, flag switch in the DeclClause handler, ~line 4518):**

```diff
@@ func (r *Runner) cmd … DeclClause flag parsing @@
 				case "-a", "-A", "-n":
+					// `export -n NAME` removes the export
+					// attribute; it is not a nameref flag.
+					if cm.Variant.Value == "export" && flag == "-n" {
+						modes = append(modes, "+x")
+						break
+					}
 					valType = flag
```

`modes` already understands `+x` (`vr.Exported = false`).

## 2. `declare -g` writes to the nearest local, not the global scope (runner.go)

varenv4: `f(){ local -a v w; g; }; g(){ declare -ga w=(asdf fdsa); }` must
assign the *global* w, leaving f's locals empty (`g: , ` / `f: , ` /
`FIN: asdf fdsa, asdf fdsa`). bashy's DeclClause path only does
`vr.Local = false` when `-g` is given, so `overlayEnviron.Set` write-through
stops at f's local and modifies it instead of the global scope
(12 diff lines: the `g:`/`f:`/`FIN:` cluster).

**Fix sketch:** when `global == true`, bypass intermediate overlays the same
way `Runner.setGlobalVarString` (interp/vars.go) already does — walk
`overlayEnviron.parent` until the outermost overlay and `Set` there. The
value side also needs `prev` to be looked up in the global scope (not via
`r.lookupVar`, which sees the dynamic-scope local) so `declare -ga w+=(…)`
appends to the global array. A `setGlobalVar(name string, vr expand.Variable)`
helper next to `setGlobalVarString` plus a `global`-gated call in the
DeclClause tail (`r.setVar(name, vr)` site, ~line 4815) is enough for the
varenv4 cases.

## 3. `local -p NAME` must only see locals of the *current* scope (runner.go)

varenv25 expects `./varenv25.sub: line 26: local: string: not found` when
`local -p string` is run in a function where `string` is local to the
*caller*, and `local: int: not found` when it is global. bashy's
`declQuery == "-p"` branch prints any variable via `r.lookupVar`.

**Patch (interp/runner.go, declQuery == "-p" branch, ~line 4698):**

```diff
@@ 			if declQuery == "-p" {
+				// `local -p NAME` only prints variables that are
+				// local to the current function scope; bash errors
+				// with `local: NAME: not found` otherwise.
+				if cm.Variant.Value == "local" {
+					if ol, ok := r.writeEnv.(*overlayEnviron); !ok || !ol.holdsLocally(name) {
+						r.errf(r.bashErrPrefix(r.curStmtPos)+"local: %s: not found\n", name)
+						r.exit.code = 1
+						continue
+					}
+				}
 				// declare -p name: print variable with attributes.
 				vr := r.lookupVar(name)
```

(`overlayEnviron.holdsLocally` was added to interp/vars.go in this round.)
Note: bash checks "local at current scope", not just "present in overlay" —
tempenv-merged vars in the overlay would also pass `holdsLocally`; good
enough for varenv25's shapes. Fixes ~12 diff lines (the
`local: …: not found` clusters plus the reordered `declare -ir int` lines).

## 4. `readonly -p` prints nothing (runner.go / builtin.go)

`bin/bashy -c 'readonly foo=1; readonly -p'` prints nothing. Bash lists all
readonly variables as `declare -r name="value"`, including the dynamic
`SHELLOPTS` (varenv expects
`declare -r SHELLOPTS="braceexpand:hashall:interactive-comments:physical"`
after `shopt -so physical`; the SHELLOPTS value side is already correct in
this round via `lookupVar`). The DeclClause handler's
`!declHadNames && declQuery == "-p"` tail only handles `--json`; it needs a
text listing: iterate `r.writeEnv.Each`, filter `vr.ReadOnly`, sort by name,
print `formatDeclareVar(name, vr, false)`, and additionally include the
dynamic readonly vars (`SHELLOPTS`, `BASHOPTS`, `BASH_VERSINFO`, `GROUPS`…)
that never live in `writeEnv` — `r.lookupVar` reports `.ReadOnly` for them.

## 5. Temporary environment is emulated by set+restore, not a real layer (runner.go)

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

## 6. `set -k` and assignment-word processing order (runner.go)

First varenv cluster (8 diff lines):

- `HOME=/a/b/c /bin/echo $HOME c=9` with `set -k` must move `c=9` out of the
  argv and into the command's environment (bashy passes it as an argument).
- When the command word expands to nothing (`HOME=/a/b/c $ECHO a=$HOME c=9`),
  the promoted assignments must be processed left-to-right *after* the
  earlier ones take effect: `a=$HOME` should see the new `/a/b/c`, but the
  promote branch (runner.go ~line 3821) reuses `fields`, which were expanded
  before `HOME` was assigned, yielding `/usr/chet`.

## 7. Inline array-variable error wording (runner.go)

varenv13 expects per-assignment diagnostics
`` `var[0]': not a valid identifier `` / `` `var[@]': not a valid identifier ``
(and a subsequent `declare: var: not found`, plus exit status 1 surfaced as
`1`); bashy prints a single `inline variables cannot be arrays` +
`` `var[0]=X var[@]=Y f' `` pair (~10 diff lines).

## 8. `declare -I` / `local -I` unsupported (runner.go flag switch)

varenv17 (~10 diff lines). `-I` makes the new local inherit the attributes
and value of a variable with the same name at a previous scope. Accepting
the flag and reusing the existing KeepValue-inherit machinery (skipping the
fresh-unset path added to setVar this round) covers the fixture's cases.

## 9. `local -` (varenv21) and EXIT-trap FUNCNAME (varenv22)

- `local -` saves/restores `set` option state on function return; also
  `set -o ignoreeof` must set `IGNOREEOF=10` and `shopt -o ignoreeof` listing
  must reflect on/off accordingly (~16 diff lines).
- varenv22 expects function-level EXIT traps to expand `$FUNCNAME` at trap
  run time (`trap:f`) and `trap -- 'echo trap:$FUNCNAME' EXIT` listings; the
  trap machinery currently prints `trap:` (~12 diff lines).
