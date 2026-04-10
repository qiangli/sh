# Bash 5.3 Test Suite Status Report

Date: 2026-04-09 (updated)

## Summary

- **Unit tests**: All pass (`go test ./...` — 0 failures across 9 packages)
- **Bash 5.3 native tests**: 4/83 pass (extglob3, invert, strip, nquote1)
- **Test harness**: `make test-bash` runs all 83 tests from `external/bash-5.3/tests/`
- **Near-passing**: nquote5 (5 lines diff), ifs (8), iquote (15)

## Passing Tests

| Test | Description |
|------|-------------|
| extglob3 | Extended globbing patterns (advanced) |
| invert | Negation operator (`!`) in pipelines and conditions |
| strip | Pattern stripping (${var#pat}, ${var%pat}) |
| nquote1 | ANSI-C quoting ($'...') and echo -en |

## Failure Categories

| Category | Tests Affected | Examples | Effort |
|----------|---------------|----------|--------|
| **Error message format** | ~60 | `printf: usage:` vs `usage: printf`; missing `line N:` prefix; backtick quoting `` ` `` vs `'` | Medium |
| **Parser limitations** | ~15 | `+=` in arithmetic ternary; empty heredoc `<<''`; `${ cmd; }` funsub; `((expr ))` spacing | Hard |
| **Missing builtin features** | ~30 | `printf -v var`; `declare -f` display format; `type -t`; `set -o posix` behavior | Medium |
| **Quoting/escaping edge cases** | ~20 | Backslash in brace expansion; `$'...'` ANSI-C quoting differences | Hard |
| **Sub-file sourcing format** | ~10 | Tests source `.sub` helper files and expect exact error line numbers | Trivial |

## Near-Passing Tests (sorted by diff size)

| Diff Lines | Test | Primary Issue |
|-----------|------|---------------|
| ~5 | nquote5 | Minor quoting differences |
| ~5 | strip | Pattern stripping edge cases |
| ~9 | nquote1 | Quoting in special contexts |
| ~10 | ifs | IFS splitting edge cases |
| ~17 | dynvar | Dynamic variable format differences |
| ~21 | lastpipe | Lastpipe option behavior |
| ~23 | tilde | Tilde expansion edge cases |
| ~25 | tilde2 | Tilde expansion with users |
| ~28 | exportfunc | Exported function display format |
| ~31 | appendop | Append operator edge cases |

## Blocking Parser Issues

These parse errors abort the entire test file on the first occurrence:

1. **arith.tests:129** — `y=$((1 ? 20 : x+=2))` — `+=` compound assignment inside arithmetic ternary
2. **heredoc.tests:181** — `cat <<''` — Empty-string heredoc delimiter
3. **comsub.tests:86** — `${ }` — Brace command substitution (funsub)
4. **comsub2.tests:129** — `${ (shift) }` — Funsub with subshell
5. **cond.tests:91** — `${H*}` — `*` in parameter expansion inside `[[ ]]`
6. **parser.tests:7** — `((true ) )` — Arithmetic with space before `)` in case clause

## What Has Been Implemented (Phases 1-20)

### Phase 1: Foundation
- `cmd/bashy/` with main.go, interactive.go, prompt.go, version.go
- BASH_VERSION, BASH_VERSINFO, BASH identity variables
- PS1/PS2 prompt expansion (all Bash escape sequences)

### Phase 2: Parameter Expansion
- `${var@U}`, `${var@u}`, `${var@L}` — case transforms
- `${var@K}`, `${var@k}` — array key-value pairs
- `${var@P}` — prompt expansion
- `pe.Width` (mksh), `pe.IsSet` (zsh)
- Panic fix on unknown `@` operator

### Phase 3: Signals and Traps
- Generic `trapCallbacks` map replacing old callbackErr/callbackExit
- EXIT, ERR, DEBUG, RETURN pseudo-signals
- Signal name table (HUP through TERM)
- `trap -l`, `trap -p`
- BASH_COMMAND set before each command
- DEBUG trap fires before simple commands
- RETURN trap fires on function return

### Phase 4: Builtins
- `caller` with call stack tracking
- `hash` with command path caching (-r to clear)
- `help` with builtin listing
- `enable` / `enable -n` to disable builtins
- `jobs`, `fg`, `bg`, `kill`, `disown` (basic)
- `wait -n` (next job), `wait -p VAR`
- `times`, `umask`, `logout`, `suspend` (stub)
- `history`, `fc`, `bind` (stubs)

### Phase 5: Shopt Options
- `nocasematch` — case-insensitive in `[[ ]]`, `case`, `=~`
- `xpg_echo` — echo interprets escapes by default
- `autocd` — auto-cd to directory names
- `inherit_errexit` — propagate into command substitutions
- `sourcepath` — search PATH for source argument
- `failglob`, `globasciiranges`, `huponexit`, `lastpipe` (marked supported)

### Phase 6: Programmable Completion
- `compgen`, `complete`, `compopt` stubs

### Phase 7: Advanced Features
- Coproc execution with pipe setup and COPROC array
- BASH_REMATCH (already implemented upstream)

### Phase 8: Call Stack
- FUNCNAME, BASH_SOURCE, BASH_LINENO as dynamic arrays
- Call stack push/pop on function entry/exit

### Phase 9: Readline
- `github.com/ergochat/readline` (MIT, pure Go)
- Full line editing: arrow keys, Ctrl-A/E, kill/yank, Ctrl-R search
- Persistent history via ~/.bashy_history
- PROMPT_COMMAND execution before each prompt
- Fallback to basic mode if readline unavailable

### Phase 10-11: History and Startup Files
- Persistent history file
- Login shell: /etc/profile, ~/.bash_profile
- Interactive: ~/.bashyrc (fallback ~/.bashrc)
- Non-interactive: $BASH_ENV
- --norc, --noprofile, --login flags

### Phase 12: Named FD Redirections
- `{varname}>` syntax detected in redir()

### Phase 13: Shell Variables
- HOSTNAME, HOSTTYPE, MACHTYPE, OSTYPE
- SHELLOPTS, BASHOPTS (dynamic from option tables)
- SHLVL (incremented from parent)

### Phase 14: read Enhancements
- `-t timeout`, `-n nchars`, `-d delimiter`
- `-e`, `-i`, `-u` (accepted, basic handling)

### Parser Fixes
- `${var~}` / `${var~~}` case-toggle operators
- Pattern panic fix (regexp.Compile instead of MustCompile)
- `noclobber` (-C) and `posix` options
- `declare -F` (list function names)

### Phase 20: Test Harness
- `make test-bash` runs all 83 bash 5.3 tests
- `make test-bash-helpers` builds recho/zecho
- Baseline established: 2/83 passing

## What's Needed for Full Pass

1. **Error message matching** — Every error string must match GNU Bash exactly (format, quoting style, line number prefix)
2. **Parser fixes** — 6 blocking parse errors in arithmetic, heredoc, funsub, and conditional constructs
3. **Printf rewrite** — `-v var`, `%T` datetime, `%(fmt)T`, precision handling
4. **Declare/type output** — Function body display format, attribute display
5. **Brace expansion** — Backslash quoting edge cases, sequence step handling
6. **IFS handling** — Subtle splitting differences in edge cases

## Running the Tests

```bash
# Build and run all bash tests
make test-bash

# Run a single test
cd external/bash-5.3/tests
THIS_SH=../../../bin/bashy ./run-arith

# Run with diff output
THIS_SH=../../../bin/bashy ./bashy ./arith.tests > /tmp/out 2>&1
diff /tmp/out arith.right

# List all available tests
make test-bash-list
```
