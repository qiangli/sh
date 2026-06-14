# history fixture — blockers ledger

**Status (2026-06-11): the `history` fixture PASSES — `diff` against
`history.right` is 0 lines.** Nothing is blocked anymore; this file is
kept as a record of how the remaining clusters were resolved and of the
notes for sibling agents.

History of the work, by round:

- Round 1 (scope: `interp/builtin.go` + new `interp/history*.go`): full
  `history`/`fc` builtins, reader-level recording emulation
  (`histSync` + source timeline), HISTFILE load/save,
  HISTCONTROL/HISTIGNORE/HISTSIZE. 614 → 260 diff lines.
- Round 2 (`cmd/bashy`): `-in` clustered invocation flags +
  forced-interactive noexec loop with prompt echo, history recording,
  and exit-time HISTFILE write/truncation (history7.sub). 260 → 43.
- Round 3 (this round, scope: `interp/builtin.go`, `interp/history*.go`,
  `cmd/bashy/`): the last two clusters, below. 43 → 0.

## Resolved: bare `!!` / `!e` history expansion (was item 3)

With `set -H` + `set -o history`, the AST interpreter used to execute
`!!` as a command lookup and printed `!!: command not found` before the
history engine ran the expansion. Fixed inside the scope wall:
`IsBuiltin` now claims `!`-prefixed words while reader-level history
expansion is armed (`histDesignator` in `interp/history.go`), routing
them into the builtin dispatcher, where the `histSync` call at the top
of the dispatch consumes the line's designator from the reader
timeline, echoes the expansion to stderr, and executes it — bash's
exact ordering, with no command-not-found noise. Regression test:
`TestHistoryExpansionDesignators` in `interp/history_test.go`.

## Resolved: history4.sub readline sections (was item 2)

`history4.sub` pipes `\cR` (reverse-i-search), `\cP`
(previous-history), and `\cO` (operate-and-get-next) control bytes into
`${THIS_SH} --norc -i 2>/dev/null`. Implemented as a self-contained
readline emulation in `cmd/bashy/forced_interactive.go`
(`runForcedInteractiveExec`), driven when `-i` is given and stdin is
not a tty (the noexec `-in` path in `main.go` is unchanged). Key
semantics, verified against the vendored bash 5.3 sources:

- `C-o` stores the **logical** history number of the next entry
  (`where + history_base + 1`, see `rl_operate_and_get_next` in
  `external/bash-5.3/lib/readline/misc.c`), so HISTSIZE-stifle drops
  (which bump `history_base`) don't skew which entry is preloaded.
- HISTFILE is loaded at startup line-per-entry (multi-line entries
  written by `history -w` come back split), so `C-o` walks the split
  lines and the shell's PS2 continuation re-joins them into one
  command.
- Multi-line commands are recorded cmdhist-style: continuation lines
  merge into the previous entry with a literal newline when the break
  is inside quotes, `; ` otherwise.
- HISTIGNORE/HISTCONTROL come from the exported environment —
  history.tests exports `HISTIGNORE='&:history*:fc*'`, which is why
  `history -w` is absent from the sessions' navigable history.
- The exit-time HISTFILE save resolves `$HISTFILE` from the **live
  runner state** (`runnerExpand`, a throwaway `Runner.Subshell()`), so
  the sessions' first `HISTFILE=` line prevents the save from
  clobbering the file that later sections re-read.

Regression tests: `TestForcedInteractiveOperateAndGetNext` and
`TestForcedInteractiveReverseSearch` in `cmd/bashy/main_test.go`.

## Notes for sibling agents

- `interp/runner.go` is not gofmt-clean at the current HEAD
  (pre-existing; `gofmt -s -l interp/` flags it). Not touched here
  since runner.go is outside this item's wall.
- The history engine state is a package-level singleton in
  `interp/history.go` (`shellHist`) because the scope wall did not
  allow adding Runner fields in `interp/api.go`. If a future item
  touches api.go, moving the state onto the Runner (and copying it in
  `subshell()`) would be cleaner. Tests can reset it via `histReset()`.
- `IsBuiltin` consults the singleton via `histDesignator` — it only
  returns true for `!`-words after a script has run both
  `set -o history` and `set -H`, so normal embedders are unaffected.
