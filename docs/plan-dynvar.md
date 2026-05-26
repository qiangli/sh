# Plan: flip `dynvar.tests` in the bash 5.3 suite

Bash 5.3 `dynvar.tests` exercises bash's dynamic shell variables
(`BASHPID`, `BASH_ARGV0`, `BASH_COMMAND`, `SECONDS`, `EPOCHSECONDS`,
`LINENO`, `FUNCNAME`, `GROUPS`). Bashy currently fails on **four** sub-tests
inside the file; each one is independent and small. This plan covers all
four. Effort cap: ~2 hours for the batch.

## Current 15-line diff

```
1,2c1,3
< BASH_ARGV0 mismatch: ./dynvar.tests (./dynvar.tests)
< BASH_ARGV0 mismatch: ./dynvar.tests (./dynvar.tests)
---
> BASHPID ok
> BASH_ARGV0 ok
> BASH_ARGV0 ok
6c7
< 
---
> echo $BASH_COMMAND
12c13
< division by zero
---
> ./dynvar.tests: line 115: ((: LINENO / 0 : division by 0 (error token is "0 ")
```

## Sub-fixes (all in `interp/`)

### 1. `BASHPID` differs in subshells *(S, ~10 min)*

**What the test does:**
```bash
bpid=$BASHPID
subpid=$( (echo $BASHPID) )
if [ "$bpid" -ne "$subpid" ]; then echo BASHPID ok; fi
```

**Why it fails today:** real bash returns the OS PID, which differs in a
forked subshell. Bashy's subshells are goroutines, so `os.Getpid()` is the
same number everywhere. The test sees `bpid == subpid` and emits no
`BASHPID ok` line.

**Fix:** in `interp/vars.go` `BASHPID` case, return
`os.Getpid() + r.subshellLevel`. The parent shell is at level 0,
`$( ... )` increments by one, and the inner `( ... )` increments again,
so `subpid` differs from `bpid` by at least 1.

Trade-off: the absolute value isn't a "real" PID anymore, but bash never
treats `$BASHPID` as an OS handle inside scripts anyway — only as an
identity-differentiator across subshell boundaries.

### 2. `BASH_ARGV0` is writable *(S–M, ~20 min)*

**What the test does:**
```bash
BASH_ARGV0=hello
case $0 in
hello)	echo BASH_ARGV0 ok ;;
*)	echo "BASH_ARGV0 mismatch: $BASH_ARGV0 ($0)" >&2 ;;
esac
```

Plus an in-function assignment that should affect the caller.

**Why it fails today:** `BASH_ARGV0` is dispatched dynamically in
`lookupVar` (`r.filename` or `"bashy"`), so user assignments via
`r.writeEnv.Set` are stored but never observed. `$0` independently reads
`r.filename`, so it never updates either.

**Fix:** in `interp/vars.go` `setVar`, special-case the name
`"BASH_ARGV0"` to also update `r.filename = vr.Str`. That makes both
`$BASH_ARGV0` and `$0` (both read `r.filename`) reflect the assignment
without changing the `lookupVar` dispatch.

### 3. `$BASH_COMMAND` is the pre-expansion source text *(M, ~25 min)*

**What the test does:**
```bash
${THIS_SH} -c 'echo $BASH_COMMAND'
```

Expected stdout: `echo $BASH_COMMAND` (the literal pre-expansion source).

**Why it fails today:** `BASH_COMMAND` is set in `r.call` **after**
expansion via `strings.Join(args, " ")`. By the time `echo`'s `$BASH_COMMAND`
is expanded, the variable holds the previous command's text (or empty for
the first command), so `echo` prints nothing useful.

**Fix:** in `runner.go` `case *syntax.CallExpr:`, before alias rewriting
and expansion, run `syntax.NewPrinter().Print(&buf, cm)` on the
**original** `CallExpr` node and store that string via
`r.setVarString("BASH_COMMAND", text)`. Then the subsequent
`r.fields(args...)` call will see the new value when expanding
`$BASH_COMMAND`. The existing `r.call`-side `setVarString` becomes
redundant (still correct, just overwriting with the expanded form for the
benefit of `DEBUG` traps that fire after expansion); leave it for now.

### 4. Bash-format arithmetic error *(M, ~30 min)*

**What the test does:**
```bash
arith_lineno() {
    ...
    (( LINENO / 0 ))
}
arith_lineno
```

Expected on stderr:
`./dynvar.tests: line 115: ((: LINENO / 0 : division by 0 (error token is "0 ")`

**Why it fails today:** bashy emits a bare `"division by zero"` from
`expand/arith.go`. No file:line prefix, no offending-token context.

**Fix:** in `interp/runner.go` `arithm()`, when `expand.Arithm` returns an
error AND `r.bashCompatErrors` is on, reformat:
- detect "division by zero" / "division by 0" via `strings.Contains`
- get the expression text via `syntax.NewPrinter().Print(buf, expr)`
- if `expr` is a `*syntax.BinaryArithm` with `Op` in `{Quo, Rem, QuoAssgn,
  RemAssgn}`, format `expr.Y` for the offending-token field
- emit `"<file>: line N: ((: <exprText> : division by 0 (error token is \"<token> \")"`

The trailing space after the token (and inside the quotes) is bash's
output verbatim — match it.

Legacy `TestRunnerRun` tests still expect bare `"division by zero"`
because they don't set `WithBashCompatErrors(true)` — gated by the flag,
so they continue to pass.

## Order

1. BASHPID (smallest, lowest risk, no behavior change for other tests)
2. BASH_ARGV0 (setVar intercept; isolated)
3. BASH_COMMAND (touches CallExpr path; verify no regressions)
4. Arithmetic error format (last, most invasive; gated by bashCompatErrors so regressions are localized)

After each step: build, run `go test -short ./interp/ ./expand/`, run
`make test-bash 2>&1 | tail -3`, confirm we're still at ≥6 passing and
that dynvar's diff is shrinking.

## Expected outcome

dynvar flips from FAIL → PASS. Bash suite: 6 → 7.

Side-benefit improvements (from infrastructure changes):
- BASH_COMMAND pre-expansion fix should improve `set-x` (xtrace),
  `trap`, and `dbg-support` test diffs (though probably not flip them).
- BASH_ARGV0 settable helps any script that uses `$0` reassignment.
- Bash-format arithmetic errors get us partway toward the broader G0
  error-format pass.
