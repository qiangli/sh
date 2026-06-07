# Plan: `cmd/bashy` Drop-In Replacement for Bash 5.3

## Context

Phases 1-8 built bashy into a solid Bash-compatible script runner (~60-70% of scripting features). However, it is not a drop-in replacement for interactive use. The three biggest blockers are: no readline/line editing, no command history, and no real job control. This plan covers the remaining work (Phases 9-19) to achieve full drop-in compatibility.

## Constraints

1. **Pure Go only** — No CGo, no C dependencies. All code and third-party libraries must be pure Go.
2. **Permissive licenses only** — All third-party dependencies must use MIT, BSD, Apache 2.0, or equivalent permissive licenses. No GPL/LGPL.
3. **Backwards compatible** — All existing `go test ./...` must continue to pass.

## References

- **Bash 5.3 docs**: upstream GNU Bash manual at `https://www.gnu.org/software/bash/manual/bash.html`
- **Bash 5.3 source**: `external/bash-5.3/` (C source for reference)
- **Bash 5.3 tests**: `external/bash-5.3/tests/` (83 `.tests` files, 87 `.right` expected output files, 88 `run-*` runner scripts)
- **Current bashy**: `cmd/bashy/` (main.go, interactive.go, prompt.go, version.go)
- **Interpreter**: `interp/` (api.go, runner.go, builtin.go, vars.go, test.go)
- **Existing confirmation tests**: `interp/interp_test.go:TestRunnerRunConfirm` — runs test cases against real `bash 5.3` and compares output

## Third-Party Library Selection

All candidates are **pure Go** with **permissive licenses**:

| Library | License | Notes |
|---------|---------|-------|
| `github.com/ergochat/readline` | MIT | Maintained fork of chzyer/readline. Line editing, history, completion, vi/emacs modes. **Recommended.** |
| `github.com/chzyer/readline` | MIT | Original, widely used but unmaintained since 2022. |
| `github.com/peterh/liner` | X11/BSD | Simpler API, history support, but no programmable completion callbacks. |
| `github.com/nyaosorg/go-readline-ny` | MIT | Feature-rich, actively maintained, CJK-aware. |

**Decision**: Use `github.com/ergochat/readline` (MIT, pure Go, maintained, feature-complete).

---

## Validation Strategy

### Level 1: Existing Unit Tests
```bash
go test ./...                    # Must pass after every phase
go test -race ./...              # No data races
```

### Level 2: Bash 5.3 Confirmation Tests (existing)
The project already has `TestRunnerRunConfirm` in `interp/interp_test.go` which runs ~1000+ test cases against real `bash 5.3` and compares output:
```bash
CGO_ENABLED=0 go test -run TestRunnerRunConfirm -exec 'dockexec bash:5.3' ./interp
```

### Level 3: Bash 5.3 Native Test Suite (83 test files)
The bash source at `external/bash-5.3/tests/` contains 83 `.tests` files with `.right` expected output. The `run-all` script executes each test via `${THIS_SH} ./<name>.tests > output 2>&1` and diffs against `<name>.right`.

**Approach**: Create a Go test harness (`cmd/bashy/bash_test.go`) that:
1. Builds bashy
2. Sets `THIS_SH=./bashy`
3. Runs each `run-*` script
4. Compares output against `.right` files
5. Reports pass/fail per test category

```bash
# Manual execution:
cd external/bash-5.3/tests
THIS_SH=../../../bashy ./run-all

# Or individual tests:
THIS_SH=../../../bashy ./run-arith
THIS_SH=../../../bashy ./run-array
THIS_SH=../../../bashy ./run-glob
```

**Test categories by priority** (start with core, expand to interactive):

| Priority | Tests | Coverage |
|----------|-------|----------|
| P0 - Core | `run-arith`, `run-array`, `run-braces`, `run-case`, `run-casemod`, `run-comsub`, `run-cond`, `run-dollars`, `run-exp-tests`, `run-func`, `run-glob`, `run-heredoc`, `run-parser`, `run-posix2`, `run-quote`, `run-redir` | Language fundamentals |
| P1 - Builtins | `run-alias`, `run-builtins`, `run-dirstack`, `run-read`, `run-shopt`, `run-trap`, `run-type` | Builtin commands |
| P2 - Advanced | `run-assoc`, `run-coproc`, `run-extglob`, `run-nameref`, `run-varenv` | Advanced features |
| P3 - Interactive | `run-complete`, `run-histexp`, `run-history`, `run-jobs` | Interactive features (Phase 9+ dependent) |

### Level 4: POSIX Compliance Tests

**Option A — Open Group VSX-PCTS** (official POSIX certification):
- Free 12-month license for open source projects from The Open Group
- Tests IEEE Std 1003.1-2016/2018 shell and utilities
- Download: `https://www.opengroup.org/testing/downloads.html`

**Option B — ShellSpec** (open source, BDD framework):
- POSIX-compliant test framework for shell scripts
- Tests shell portability across dash, bash, ksh, zsh, mksh
- `https://shellspec.info/`

**Option C — Custom POSIX subset tests**:
- Create a `tests/posix/` directory with focused POSIX compliance tests
- Based on the POSIX shell specification (sections 2.1-2.14)
- Cover: quoting, parameter expansion, command substitution, arithmetic, here-documents, pathname expansion, redirection, command search/execution

**Recommendation**: Start with Level 3 (bash native tests) to measure baseline compatibility. Then add Level 4 Option C (custom POSIX tests) for targeted coverage. Pursue VSX-PCTS certification as a stretch goal.

### Level 5: Continuous Validation Makefile Target
```makefile
## test-bash: Run bash 5.3 native test suite against bashy
test-bash: build
	cd external/bash-5.3/tests && THIS_SH=../../../bin/bashy ./run-all

## test-posix: Run POSIX compliance tests
test-posix: build
	cd tests/posix && THIS_SH=../../bin/bashy ./run-all
```

---

## Phase 9: Readline and Line Editing

**Goal**: Replace raw stdin with full line editing (arrow keys, Ctrl-A/E, word movement, kill/yank).

**Files**:
- `cmd/bashy/interactive.go` — Rewrite to use `readline.Instance`
- `go.mod` — Add `github.com/ergochat/readline`

**Design**: Create `readline.Instance` with PS1 prompt. Loop: `readline.Readline()` -> feed to parser -> collect until `!parser.Incomplete()` (use PS2 for continuation) -> execute.

---

## Phase 10: Command History

**Goal**: Persistent history, history expansion, `history`/`fc` builtins.

**Files**:
- `cmd/bashy/history.go` (new) — History manager: in-memory list + `~/.bashy_history` file, HISTSIZE/HISTFILESIZE/HISTCONTROL
- `cmd/bashy/interactive.go` — Wire history, add history expansion pre-processing (`!!`, `!$`, `!n`, `^old^new`)
- `interp/builtin.go` — Implement `history` (-c/-d/-a/-r/-w) and `fc` (-l/-s/-e) via a `HistoryHandler` callback on Runner
- `interp/api.go` — Add `HistoryHandler func(op string, args []string) ([]string, error)` to Runner
- `interp/vars.go` — Add `HISTCMD` dynamic variable

---

## Phase 11: Startup Files

**Goal**: Load `.bashrc`, `.bash_profile`, `/etc/profile` on startup.

**Files**:
- `cmd/bashy/main.go` — Add startup file loading, fix `--norc`/`--noprofile` flags, detect login shell (`argv[0]` starts with `-` or `--login`)

**Load sequence**:
- Login: `/etc/profile`, then first of `~/.bash_profile`, `~/.bash_login`, `~/.profile`
- Interactive non-login: `~/.bashyrc` (fallback `~/.bashrc`)
- Non-interactive: `$BASH_ENV`

---

## Phase 12: Named FD Redirections

**Goal**: Support `exec {fd}>file` and `{fd}<file`.

**Files**:
- `interp/runner.go` — Extend `redir()`: detect `{varname}` in `rd.N.Value`, allocate FD >= 10, store in `openFds` map, set variable
- `interp/api.go` — Add `openFds map[int]*os.File` to Runner

The parser already handles `{varname}>` syntax (syntax/nodes.go `Redirect.N`). Only the interpreter needs changes.

---

## Phase 13: Missing Shell Variables (~30)

**Files**: `interp/vars.go`, `interp/api.go`, `cmd/bashy/interactive.go`

**Group A** (trivial, add to `lookupVar`):
- `HOSTNAME` — `os.Hostname()`
- `HOSTTYPE` — `runtime.GOARCH`
- `MACHTYPE` — `runtime.GOARCH + "-unknown-" + runtime.GOOS`
- `OSTYPE` — `runtime.GOOS`
- `SHLVL` — increment from parent env
- `BASH_EXECUTION_STRING` — store `-c` argument
- `SHELLOPTS` — colon-separated list of enabled `set -o` options
- `BASHOPTS` — colon-separated list of enabled `shopt` options

**Group B** (require Runner state):
- `COLUMNS` / `LINES` — `term.GetSize()`, update on SIGWINCH
- `HISTCMD` — from history manager (Phase 10)

**Group C** (require execution hooks):
- `PROMPT_COMMAND` — execute before each PS1 in interactive.go
- `PS0` — display after command read, before execution
- `PS4` — custom xtrace prefix (replace hardcoded `"+ "` in trace.go)

---

## Phase 14: `read` Builtin Enhancements

**Files**: `interp/builtin.go`, `interp/api.go`

**Missing options**:
- `-t timeout` — `context.WithTimeout` around read
- `-n nchars` / `-N nchars` — Read N characters
- `-d delim` — Custom delimiter
- `-e` — Use readline for input (via `ReadlineHandler` callback on Runner)
- `-i text` — Initial text for `-e`
- `-u fd` — Read from file descriptor

---

## Phase 15: Programmable Completion

**Files**:
- `interp/completion.go` (new) — `completionSpec`, `completionRegistry`, generate completions
- `cmd/bashy/completion.go` (new) — Wire to readline Tab callback
- `interp/builtin.go` — Replace `compgen`/`complete`/`compopt` stubs
- `interp/vars.go` — COMP_WORDS, COMP_CWORD, COMP_LINE, COMP_POINT, COMPREPLY

**`complete` registers specs**: `-W wordlist`, `-F function`, `-C command`, `-G glob`, `-A action`, `-X filter`, `-P prefix`, `-S suffix`, `-o option`

**Tab callback**: Parse current line -> find command -> look up spec -> if `-F`, set COMP_* vars, call function, read COMPREPLY -> return matches to readline.

---

## Phase 16: POSIX Job Control

**Goal**: Real process groups, terminal control, Ctrl-Z.

**Files**: `interp/runner.go`, `interp/api.go`, `interp/builtin.go`, `interp/os_unix.go`

**Design**: Replace goroutine-based `bgProcs` with real job table:
```
type job struct {
    id, pgid int
    pids     []int
    cmd      string
    status   jobStatus  // running, stopped, done
    done     chan struct{}
    exit     *exitStatus
}
```

**Key points**:
- Set `Setpgid: true` in `exec.Cmd.SysProcAttr`
- Foreground: `tcsetpgrp(fd, pgid)` for terminal control
- SIGTSTP handler: stop foreground job, add to table as "stopped"
- `fg %n`: `tcsetpgrp` + SIGCONT + wait
- `bg %n`: SIGCONT without terminal control
- `JobControl(bool)` RunnerOption to opt-in

---

## Phase 17: Remaining Shopt Options (29)

**Files**: `interp/runner.go`, `interp/api.go`, `interp/builtin.go`

**Batch A** (high impact):
- `inherit_errexit` — Propagate errexit into command substitution subshells
- `localvar_inherit` / `localvar_unset` — Local variable scoping
- `sourcepath` — Search PATH for `source` argument
- `checkjobs` — Warn about running jobs on exit
- `execfail` — Don't exit on exec failure

**Batch B** (interactive):
- `cdspell` / `dirspell` — Spelling correction (Levenshtein)
- `histappend`, `histreedit`, `histverify`, `cmdhist`, `lithist` — History options

**Batch C** (compat modes):
- `compat31` through `compat44` — Low priority, per-version behavior tweaks

---

## Phase 18: `help` Builtin with Embedded Text

**Files**:
- `interp/helptext/` (new dir) — One `.txt` per builtin with Bash-style help output
- `interp/help.go` (new) — `//go:embed helptext/*.txt`, lookup function
- `interp/builtin.go` — Replace current `help` case with embedded lookup

---

## Phase 19: PS0, PS4, SIGWINCH, `times`

**Files**: `cmd/bashy/interactive.go`, `interp/trace.go`, `interp/builtin.go`

- **PS0**: After readline returns line, before `r.Run()`, print expanded PS0
- **PS4**: In trace.go, replace `"+ "` with expanded PS4
- **SIGWINCH**: `signal.Notify` -> `term.GetSize()` -> update COLUMNS/LINES
- **times**: Use `syscall.Getrusage(RUSAGE_SELF/CHILDREN)` for real user/sys times

---

## Phase 20: Test Harness for Bash 5.3 Test Suite

**Goal**: Automated validation that bashy passes the bash 5.3 native tests.

**Files**:
- `cmd/bashy/bash_compat_test.go` (new) — Go test that builds bashy and runs each `external/bash-5.3/tests/run-*` script
- `Makefile` — Add `test-bash` and `test-posix` targets

**Design**:
1. `TestBashCompat` iterates over `external/bash-5.3/tests/run-*` scripts
2. Each test: set `THIS_SH` to bashy binary, run the script, capture output
3. Compare against `.right` file
4. Report per-test pass/fail with diff on failure
5. Use `t.Skip` for tests that depend on unimplemented features (with TODO tracking)

**Incremental approach**: Start by running all 83 tests, record which pass. Create a "known failures" list. Each phase should shrink the failure list. Target:
- After Phase 14: Pass P0 tests (core language — ~16 tests)
- After Phase 16: Pass P1+P2 tests (builtins + advanced — ~30 tests)
- After Phase 19: Pass P3 tests (interactive — all 83 tests)

---

## Dependency Graph

```
Phase 9  (Readline)           <- FIRST PRIORITY
  |-- Phase 10 (History)
  |     +-- Phase 15 (Completion)
  |-- Phase 11 (Startup Files)
  +-- Phase 19 (PS0/PS4/SIGWINCH)

Phase 12 (Named FD)           <- Independent
Phase 13 (Shell Variables)     <- Independent (some need Phase 9)
Phase 14 (read enhancements)   <- Independent
Phase 16 (Job Control)         <- Independent, complex
Phase 17 (Shopt Options)       <- Depends on 16 for checkjobs
Phase 18 (help)                <- Independent
Phase 20 (Test Harness)        <- Independent, should be done early
```

**Recommended order**: 20 (test harness first) -> 9 -> 12+14 (parallel) -> 13 -> 10 -> 11 -> 16 -> 15 -> 17 -> 18+19

---

## Verification Summary

| Level | Method | When |
|-------|--------|------|
| L1 | `go test ./...` | After every change |
| L2 | `TestRunnerRunConfirm` vs real bash 5.3 | After every phase |
| L3 | Bash 5.3 native test suite (83 tests) | `make test-bash` |
| L4 | POSIX compliance (ShellSpec or custom) | `make test-posix` |
| L5 | Interactive smoke tests | Manual after Phases 9, 10, 15, 16 |
