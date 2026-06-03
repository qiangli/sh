# Plan — Bash 5.3 error-format pass (Batch A / G0)

Goal: prefix every user-visible error message in `bashCompatErrors` mode
with the bash 5.3 `<file>: line N: …` shape and swap our error vocabulary
to match bash's exact wording. Expected payoff per the gap analysis is
~60 of 67 currently-failing bash 5.3 tests, with no new features added.

Baseline (2026-06-03): **8 passed, 67 failed, 10 skipped, 2 timed out**.

## What "bash format" means concretely

Bash prefixes script-level errors with the script name and a 1-based
line number, then the offending command-or-token noun, then the
condition. Three representative samples from the failing tests:

```
./read.tests: line 101: b: readonly variable
./exportfunc.tests: line 37: cve7169-bad: No such file or directory
./exportfunc.tests: eval: line 44: syntax error: unexpected end of file from `{' command on line 42
```

Note the punctuation: noun first, then condition; capital-N
`No such file…`; bash quotes with `` ` `` opening and `'` closing
(asymmetric). Eval-time parser errors squeeze `eval:` between the
filename and `line N:` rather than after it.

## Existing scaffolding (already in tree)

- `RunnerOption WithBashCompatErrors(true)` is on by default for
  `cmd/bashy`.
- `interp.Runner.bashErrPrefix(pos)` returns `<filename>: line N: ` (or
  empty when the option is off) — `interp/runner.go:393`.
- `failf` in `interp.builtin` already applies the prefix.
- `bashUsage` map provides per-builtin usage lines —
  `interp/builtin.go:1929`.

What is missing is the rest of the surface:

1. `setVar` errors (`readonly variable`) emit at `vars.go:501` via direct
   `r.errf("%s: %v\n", name, err)` — no prefix, no pos.
2. `r.redir` / `source` file-open errors print the raw Go error (`open
   /abs/path: no such file or directory`) — wrong wording, wrong shape.
3. `syntax.ParseError.Error()` returns `<file>:<L>:<C>: <text>` (`syntax/
   parser.go:972`). Bash prints `<file>: line N: <text>` followed by a
   `<file>: line N: \`<offending line>'` echo.
4. Eval-time parse errors fold the message into our generic
   `<file>: line N: eval: <ParseError>` — bash uses `<file>: eval: line N: …`.
5. ~24 direct `r.errf(...)` sites inside `interp/builtin.go` bypass the
   prefix path entirely.
6. Quote style: our messages use `"foo"` or `'foo'`; bash uses
   `` `foo' `` everywhere.

## Work plan (commit-by-commit, each tested via `make test-bash`)

The order is calibrated for ROI per commit. We commit after each step,
re-baseline, and only move to the next when the previous is stable.

1. **`curStmt` pos tracking + setVar prefix.** Add `r.curStmtPos` written
   in `stmtSync` and read by `setVar` so `readonly variable`,
   `cannot assign to readonly variable`, and friends carry the bash
   prefix. *Expected unblocks:* read, appendop, varenv (subset).

2. **Parser-error reformat in `cmd/bashy`.** Catch `syntax.ParseError`
   at the top-level `Parse` call, reprint as
   `<file>: line N: <text>` + `<file>: line N: \`<line>'`. Also handle
   the `-c` flavor (`bashy: -c: line N: …`). *Expected unblocks:*
   parser, posix2, exportfunc.

3. **File-not-found wording.** `source`, redirection, and exec
   file-open paths emit `<file>: line N: <name>: No such file or
   directory` (bash phrasing) when in compat mode. *Expected
   unblocks:* exportfunc, vredir, redir (subset).

4. **`bq()` quote helper + apply to `failf` strings.** Introduce
   `bq(s string) string` returning `` `<s>' `` in compat mode and
   `"<s>"` otherwise. Replace `%q` in the canonical error messages
   (`not a valid identifier`, `invalid signal specification`, etc.).

5. **Audit direct `r.errf` in `builtin.go`.** Convert each to
   `failf`-style (prefix + code) or to a thin wrapper that applies the
   prefix. Eliminates the bypasses.

6. **Eval-time prefix shape.** Change `eval`/`source` parse-error
   reporting from `<file>: line N: eval: …` to
   `<file>: eval: line N: …` to match bash.

7. **Wording sweep.** Walk through the bash messages we still differ
   on: `command not found`, `bad substitution`, `cannot create temp
   file`, `arithmetic syntax error`. One commit per cluster.

After step 7 we expect the bash pass count to land in the 40-60 range.
Re-baseline and update `docs/TODO.md` once we plateau.

## Out of scope (deferred to other batches)

- `${ cmd; }` funsub (Batch B).
- declare -p / declare -f formatting (P2 in TODO).
- printf %q quoting style.
- All non-error wording behaviour (semantic changes).
