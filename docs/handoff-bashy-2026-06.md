# Handoff — bashy bash-5.3 compatibility push

**Last touched:** 2026-06-06 by an Opus 4.7 session  
**Branch:** `master`, 122 commits ahead of `origin/master`, **DO NOT PUSH** without explicit user authorization (see `~/.claude/projects/-Users-qiangli-projects-poc-dhnt-sh/memory/feedback_bashy_goal.md`)  
**Score at handoff:** `make test-bash` → **34 passed, 40 failed, 13 skipped, 0 timed out** (74 runnable)

## The single goal

100% bash 5.3 compliance, measured by `make test-bash` PASS count. The user's standing direction (stored in memory as `feedback_bashy_goal.md`):

- No 1/2/3/4 option menus — pick an approach and execute.
- Prefer deep fixes that flip a test FAIL → PASS over wide-but-shallow improvements.
- Ask only for **system access** (sudo, credentials). For everything else, just do it.
- Don't push commits — local commits ahead of origin are fine; pushing isn't.
- Verify against the `.right` file before assuming a TODO describes a real feature gap (some TODOs are bash-incorrect).

## How to measure progress

```sh
make test-bash | tail -1     # the one number that matters
```

For per-test investigation, the bash-flavored loop I used:

```sh
cd external/bash-5.3/tests
BASHY=/Users/qiangli/projects/poc/dhnt/sh/bin/bashy
export THIS_SH=$BASHY
export PATH=$PWD:/usr/bin:/bin
$BASHY ./TEST.tests > /tmp/raw 2>&1
# Then apply the same post-processing the Makefile does:
#   - for tests in BASH_TEST_FILTER_EXPECT: grep -av '^expect' < /tmp/raw
#   - for tests in BASH_TEST_CAT_V (currently just printf): cat -v
diff /tmp/raw TEST.right
```

The standalone diff-size sort I used a few times gives **misleading** results vs `make test-bash` because the test environment differs slightly — always confirm via `make test-bash` before claiming a flip.

## Where to start (ordered by leverage)

### 1. `func.tests` — 4 lines from PASS

```
< ./func5.sub: line 92: `break': is a special builtin
> ./func5.sub: line 94: `break': is a special builtin
```

Single line-number mismatch. Source context:

```
88: ( set -o posix
89: break()
90: {
91:         echo FUNCNAME: $FUNCNAME
92: }                     <- bashy reports here (cm.End() of FuncDecl)
93: echo after
94: )                     <- bash reports here (closing ) of enclosing subshell)
```

Bash uses the enclosing subshell's `)` position. The FuncDecl's runtime handler (`interp/runner.go:3506+`) currently uses `cm.End()`. Need to propagate the enclosing statement's end position somehow, or report at a later point in execution. Note: I already use `cm.End()` for both the "not a valid identifier" path and the "special builtin" path — pattern matches between them.

### 2. Several tests close-to-passing (look at diffs first)

These were the smallest unfixed diffs from my last sort that aren't environment-bound:

- `braces.tests` (1 content line) — `${a#aaaa'$(aaaa'aaa)aaa\'}`. Bashy parses cleanly and emits `$(\'`; bash errors with `unexpected EOF`. Parser-level: bash is stricter about quotes inside `$(...)` inside `${...#pattern}`.
- `comsub-eof.tests` (44 lines) — heredocs inside `$(...)` with unclosed delimiters. Parser warning placement differs.
- `nquote3.tests` (63 lines, after filter) — `$'uv\001\001wx'` + `set $e $e` + `${e%%??}`. Suspected IFS-related word splitting on `\001`. Worth a half-day investigation.

### 3. Printer / `declare -f` shape work

Multiple tests fail on `declare -f` output formatting because the bashy printer doesn't exactly replicate bash's print style. Concrete known issues:

- **arith-for**: `for ((i=0; i < 3; 1))` (bashy) vs `for ((i=0; i<3; 1))` (bash, compact). I tried `compact=true` in the CStyleLoop printer path; it broke shfmt formatter tests. The correct fix needs a printer option (e.g., `BashCompatPrint`) used by `interp/runner.go::printFuncDecl`. **Reverted in working tree**, see `syntax/printer.go` around line 859.

- **coproc** (`type.tests`): bash prints `coproc COPROC ( b cat <<EOF...);`, bashy prints `coproc (;\n    b cat <<EOF...)`. Missing default `COPROC` name and the `(`/`;` placement is wrong. `syntax/printer.go::CoprocClause` around line 1369.

### 4. `printf` deeper work (148 lines diff remaining)

I fixed the easy printf wins (`%F`, `%n`, `%q` precision, `\x` warning, octal `\081`). Remaining:

- Output buffering / stderr ordering — bashy prints stderr-then-stdout where bash buffers stdout and flushes after stderr (line 105-106 in diff).
- `%b` escape handling for `\a` / `\b` / `\015` — emits wrong/empty bytes.
- extglob in printf format string — bashy keeps `@(hugo)` literal; bash treats it as a (failing) glob substitution and emits nothing.
- `%q` for safe strings — bash 5.3 changed to always single-quote even for safe strings; bashy still emits bare.
- strftime locale handling for `%(fmt)T` with non-C locale.

### 5. Long-tail (deeper, lower payoff per test)

Tests with 100+ line diffs (`heredoc`, `quote`, `quotearray`, `redir`, `more-exp`, `array`, `assoc`, `cond`, `extglob`) need targeted investigation. I did not open these up. The diff sizes don't necessarily correlate with how many discrete fixes are needed.

## Pitfalls I hit

- **Pre-commit hooks**: none in this repo, but `git push` is forbidden by standing direction.
- **`function NAME` parser change** affects POSIX/mksh/zsh tests — I gated it with `p.lang.in(LangBash)`. If you touch this code, **run `go test ./syntax/`** before declaring victory.
- **`syntax.Quote(LangBash)`** changed shape for strings-with-single-quotes; I updated the `ExampleQuote` golden but didn't sweep all tests that depend on @Q output. Run `go test ./...` to catch fallout.
- **Stuck `bashy` test processes** can accumulate when `make test-bash` is interrupted mid-test (especially `redir.tests` and `builtins.tests`). They consume CPU and confuse future runs. Use `ps -ef | grep bashy.tests` and `kill -9` before re-running.
- **`/dev/tty` and `chmod a-w` tests** (`test.tests` lines 60, 142) are environment-dependent on macOS — unfixable without test infra changes. Skip.
- **Test environment differences**: my standalone bash-driver loop produces different diff sizes than `make test-bash`. Trust `make test-bash`, not the standalone tool.
- **Memory cache (5min)**: when context is reset and the conversation jumps back into bashy work, re-read `feedback_bashy_goal.md` first.

## Verification checklist before any commit

```sh
PATH=/bin:/usr/bin:$(dirname $(which go)) go test ./...      # Go unit tests
make test-bash 2>&1 | tail -1                                # bash 5.3 PASS count
```

If `make test-bash` PASS count went **down**, find the regression before committing. If it stayed flat, only commit if the diff size of some failing test shrunk meaningfully (i.e., you made tangible progress toward a future flip).

## Recent commit lineage (this session)

```
803ac2ee interp: FuncDecl bash-compat errors report at end of declaration
53cd5e08 parser+interp: defer non-identifier func name errors to runtime (bash 5.3)
d30d554c printf+quote: bash 5.3 fixes (%F, %n, %q precision, %x warn, octal, @Q)
f76237c1 expand: globstar matches bash 5.3 — collapse adjacent **, no symlink recursion
```

Read these commit messages for context on the architectural shape of the fixes.

## What NOT to do

- Don't revert without first understanding why a fix was buggy — the user got visibly frustrated when an earlier session reverted a buggy commit instead of fixing the root cause. (Caveat: the original revert turned out to be correct because the commit was attempting bash-incorrect behavior; **verify against bash 5.3 / the `.right` file** before assuming a TODO entry describes a real gap.)
- Don't make 100 small commits without flipping a test. The user noticed when 114 commits moved PASS only 31→33. Aim for deep fixes that actually flip tests.
- Don't push to origin without "push" authorization.
- Don't introduce GPL/LGPL deps or CGo (see CLAUDE.md Third-Party Libraries section).
