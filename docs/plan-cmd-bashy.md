# Plan: `cmd/bashy` — Bash 5.3 Compatible Shell (All Phases Complete)

## Context

The project (`mvdan.cc/sh/v3`) provides a Go library for parsing, formatting, and interpreting shell programs. It includes `cmd/gosh`, a ~100-line proof-of-concept shell. The goal is to create a new `cmd/bashy` command that progressively implements Bash 5.3 features, using `cmd/gosh` as the starting point. The Bash 5.3 reference manual is upstream at `https://www.gnu.org/software/bash/manual/bash.html`; the local Bash 5.3 source tree is ignored at `external/bash-5.3/`.

## References

- **Bash 5.3 documentation**: `docs/bash.md` links to the upstream GNU Bash Reference Manual
- **Bash 5.3 source code**: `external/bash-5.3/` (C source for reference)
- **Starting point**: `cmd/gosh/main.go` (~100 lines, proof-of-concept shell)

## Gap Analysis Summary

The current interpreter (`interp/`) supports ~60 builtins but many are stubs. The `expand/` package covers most parameter expansions but misses several `@`-operators. Key gaps vs Bash 5.3:

| Category | Gap |
|----------|-----|
| **Expansions** | `${var@U}`, `${var@u}`, `${var@L}`, `${var@P}`, `${var@K}`, `${var@k}`, `pe.Width`, `pe.IsSet` |
| **Builtins (stubs)** | `bg`, `fg`, `kill`, `jobs`, `hash`, `history`, `help`, `bind`, `caller`, `enable`, `suspend`, `logout`, `fc`, `compgen`, `complete`, `compopt` |
| **Signals/Traps** | DEBUG trap, RETURN trap, full signal names, `trap -l`, errtrace propagation |
| **Shell Variables** | `BASH`, `BASHPID`, `BASH_VERSION`, `BASH_VERSINFO`, `BASH_REMATCH`, `BASH_SOURCE`, `BASH_LINENO`, `FUNCNAME`, `BASH_COMMAND`, `BASH_SUBSHELL`, `SECONDS`, `EPOCHSECONDS`, `EPOCHREALTIME`, `COMP_*` |
| **Shopt options** | 30+ tracked but non-functional (autocd, lastpipe, failglob, nocasematch, inherit_errexit, etc.) |
| **Language** | Coproc execution, named FD redirections `{varname}>`, alternate command substitution `${ cmd; }` |
| **Interactive** | Readline, persistent history, programmable completion, prompt expansion (PS1 `\u`, `\h`, `\w`, etc.) |

## Architecture

**Library-first approach**: Language semantics go into `interp/`, `expand/`, `syntax/`. The `cmd/bashy/` package handles only interactive UI (readline, prompt, history file I/O, completion UI). This keeps `cmd/gosh` working and lets other embedders benefit.

```
cmd/bashy/
  main.go          — Entry point, flag parsing, mode dispatch
  interactive.go   — Interactive mode: readline, history, prompt
  prompt.go        — PS1/PS2/PS3/PS4 prompt expansion (\u, \h, \w, etc.)
  history.go       — Command history (~/.bashy_history)
  completion.go    — Programmable completion UI
  version.go       — BASH_VERSION, BASH_VERSINFO constants
```

## Phased Implementation

### Phase 1: Foundation — Standalone `cmd/bashy`

**Create `cmd/bashy/` starting from `cmd/gosh/main.go`:**

- **`cmd/bashy/main.go`**: Copy `cmd/gosh/main.go`, add `--version`, `--norc`, `--noprofile`, `--posix` flags. Set up Runner with bashy-specific variable initialization.
- **`cmd/bashy/version.go`**: Define `BASH_VERSION="5.3.0(1)-bashy"`, `BASH_VERSINFO` array, `BASH` path.
- **`cmd/bashy/prompt.go`**: PS1 expansion (`\u`, `\h`, `\w`, `\W`, `\$`, `\n`, `\t`, `\d`, `\j`, `\[`, `\]`, etc.). Default PS1: `\u@\h:\w\$ `.
- **`cmd/bashy/interactive.go`**: Replace hardcoded `"$ "` prompt with PS1 rendering. Use `golang.org/x/term` or `github.com/chzyer/readline`.
- **`interp/vars.go`**: Add dynamic variables — `BASHPID`, `SECONDS` (elapsed since start), `EPOCHSECONDS`, `EPOCHREALTIME`, `BASH_SUBSHELL` (increment in `Subshell()`).

**Files to create**: `cmd/bashy/main.go`, `version.go`, `prompt.go`, `interactive.go`
**Files to modify**: `interp/vars.go`

### Phase 2: Expansion Gaps — `@U`, `@u`, `@L`, `@K`, `@k`, `@P`

- **`expand/param.go`**: Add handlers in `OtherParamOps` case:
  - `"U"` → `strings.ToUpper(str)`
  - `"u"` → capitalize first rune
  - `"L"` → `strings.ToLower(str)`
  - `"K"` / `"k"` → array key-value pair output
  - `"P"` → prompt expansion (reuse `cmd/bashy/prompt.go` logic, extract to shared location)
  - Fix the `default: panic(...)` to return an error instead
- **`expand/param.go`**: Handle `pe.Width` (display width) and `pe.IsSet` (variable existence check)

**Files to modify**: `expand/param.go`

### Phase 3: Signal and Trap Enhancements

- **`interp/runner.go`**: Add `callbackDebug`, `callbackReturn` fields. Add signal channel with `os.signal.Notify`. Fire DEBUG trap before each `call()`, RETURN trap on function/source return.
- **`interp/builtin.go`**: Enhance `trap` — support all signal names (SIGINT, SIGTERM, etc.), signal numbers, `trap -l`, `trap -p`. ERR trap with errtrace (`-E`) propagation.
- **`interp/vars.go`**: Add `BASH_COMMAND` (set before each simple command).

**Files to modify**: `interp/runner.go`, `interp/builtin.go`, `interp/vars.go`

### Phase 4: Builtin Implementations

**4a — `caller`, `hash`, `help`, `enable`:**
- Add call stack tracking (`callStack []callFrame`) to Runner → wire `BASH_SOURCE`, `BASH_LINENO`, `FUNCNAME` arrays
- `hash`: command path cache (`cmdCache map[string]string`), integrate with exec
- `help`: embedded help text via `//go:embed`
- `enable`: `disabledBuiltins` map, check before dispatch

**4b — `history`, `fc`, `bind` (interactive-only):**
- `cmd/bashy/history.go`: In-memory + `~/.bashy_history` persistence, HISTSIZE/HISTFILESIZE/HISTCONTROL
- `fc`: list/edit/re-execute history entries
- `bind`: basic key binding management (depends on readline library)

**4c — Job Control — `bg`, `fg`, `jobs`, `kill`:**
- Enhanced job table in Runner (job ID, status, command text, process group)
- SIGTSTP (Ctrl-Z) handling to stop foreground jobs
- `jobs`, `fg %n`, `bg %n`, `kill -signal %n/pid`
- `JobControl(bool)` RunnerOption

**Files to modify**: `interp/builtin.go`, `interp/runner.go`, `interp/api.go`, `interp/vars.go`
**Files to create**: `cmd/bashy/history.go`

### Phase 5: Shopt Options — Make Tracked Options Functional

Priority order: `lastpipe`, `nocasematch`, `inherit_errexit`, `autocd`, `failglob`, `globasciiranges`, `checkjobs`, `huponexit`, `sourcepath`, `xpg_echo`

**Files to modify**: `interp/runner.go`, `interp/builtin.go`, `expand/expand.go`

### Phase 6: Programmable Completion

- **`interp/completion.go`** (new): Completion engine, `completionSpec`, `completionRegistry`, `compgen`/`complete`/`compopt` logic
- **`interp/vars.go`**: COMP_WORDS, COMP_CWORD, COMP_LINE, COMP_POINT, COMPREPLY
- **`cmd/bashy/completion.go`**: Wire to readline tab completion

**Files to create**: `interp/completion.go`, `cmd/bashy/completion.go`

### Phase 7: Advanced Language Features

- **Coproc execution**: Wire `*syntax.CoprocClause` in `cmd()` dispatch, set `COPROC` array with read/write FDs
- **Named FD redirections**: `{varname}>file` — allocate FD, store number in variable
- **`BASH_REMATCH`**: Populate on `[[ str =~ regex ]]` match with capture groups
- **Alternate command substitution** `${ cmd; }`: Requires `syntax/parser.go` changes + expand wiring

**Files to modify**: `interp/runner.go`, `syntax/parser.go`, `expand/expand.go`

### Phase 8: Enhanced `wait`, Call Stack Arrays

- `wait -n` (next job), `wait -p var` (store PID)
- `BASH_SOURCE`/`BASH_LINENO`/`FUNCNAME` as proper call stack arrays (builds on Phase 4a)

## Dependency Graph

```
Phase 1 (Foundation)  ← MUST DO FIRST
  ├── Phase 2 (Expansions)
  ├── Phase 3 (Signals/Traps)
  ├── Phase 4a (caller/hash/help/enable)
  │     └── Phase 8 (call stack, wait -n)
  ├── Phase 4b (history/fc/bind)
  │     └── Phase 6 (Programmable Completion)
  ├── Phase 4c (Job Control)
  │     └── Phase 5 (shopt options needing job control)
  └── Phase 7 (Coproc, named FD, BASH_REMATCH)
```

Phases 2, 3, 4a, 4b, 4c, 7 can be developed **in parallel** after Phase 1.

## Verification

- `go build ./cmd/bashy` compiles successfully
- `go test ./...` passes (no regressions)
- `./bashy -c 'echo $BASH_VERSION'` → `5.3.0(1)-bashy`
- `./bashy -c 'x=hello; echo ${x@U}'` → `HELLO`
- `./bashy script.sh` runs scripts correctly
- Interactive mode shows PS1 prompt with `\u@\h:\w\$ ` expansion
- Confirmation tests against real Bash 5.3: `CGO_ENABLED=0 go test -run TestRunnerRunConfirm -exec 'dockexec bash:5.3' ./interp`
