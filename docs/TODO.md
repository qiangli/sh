# Bashy: Bash 5.3 Drop-In Replacement — TODO Checklist

**Current status**: 4/83 bash tests passing | All Go unit tests pass
**Last updated**: 2026-04-09

---

## Completed Phases

- [x] **Phase 1**: Foundation — cmd/bashy, prompt expansion, version vars
- [x] **Phase 2**: Parameter expansion @U/@u/@L/@K/@k/@P, pe.Width, pe.IsSet
- [x] **Phase 3**: Trap system (EXIT/ERR/DEBUG/RETURN), signal names, trap -l/-p
- [x] **Phase 4a**: caller, hash, help, enable builtins, call stack
- [x] **Phase 4b**: history/fc/bind stubs
- [x] **Phase 4c**: Job control stubs (jobs/fg/bg/kill/disown/wait -n/-p)
- [x] **Phase 5**: Shopt options (nocasematch, xpg_echo, autocd, inherit_errexit, sourcepath)
- [x] **Phase 6**: Programmable completion stubs (compgen/complete/compopt)
- [x] **Phase 7**: Coproc execution, BASH_REMATCH
- [x] **Phase 8**: FUNCNAME/BASH_SOURCE/BASH_LINENO call stack arrays
- [x] **Phase 9**: Readline via ergochat/readline (MIT, pure Go)
- [x] **Phase 10**: Persistent history (~/.bashy_history) — basic via readline
- [x] **Phase 11**: Startup files (.bashrc, .bash_profile, /etc/profile, BASH_ENV)
- [x] **Phase 12**: Named FD redirections ({varname}> basic detection)
- [x] **Phase 13**: Shell variables (HOSTNAME, HOSTTYPE, OSTYPE, SHELLOPTS, BASHOPTS, SHLVL, PIPESTATUS, BASH_ARGV0, GROUPS)
- [x] **Phase 14**: read -t/-n/-d/-e/-i/-u options
- [x] **Phase 20**: Test harness (make test-bash with 15s per-test timeout)
- [x] **Parser**: ${var~}/${var~~} case-toggle operators
- [x] **Parser**: Pattern panic fix (regexp.Compile instead of MustCompile)
- [x] **Interp**: noclobber (-C) and posix options
- [x] **Interp**: declare -F, declare -i
- [x] **Interp**: export/readonly/local/declare as builtin commands (not just keywords)
- [x] **Interp**: echo combined flags (-en, -neE)
- [x] **Expand**: printf #/' flags, . precision, float formats, uppercase X
- [x] **Interp**: Positional params >9 (${10}, ${11}, etc.)

---

## Remaining Work — By Priority

### P0: Parser Fixes (blocking entire test files)

- [x] `+=` compound assignment in arithmetic ternary: `$((cond ? val : x+=2))`
- [x] Empty heredoc delimiter: `cat <<''` (already worked; regression tests added)
- [ ] `${ cmd; }` funsub (brace command substitution) execution
- [ ] `${ (shift) }` funsub with subshell
- [ ] `${H*}` — `*` as parameter expansion pattern inside `[[ ]]`
- [ ] `((true ) )` — arithmetic with space before `)` in case clause
- [ ] `case esac in esac)` — eval parsing of unusual case patterns

### P1: Error Message Format (affects ~60 tests)

- [ ] Add `<filename>: line <N>:` prefix to error messages from builtins
- [ ] Add `<filename>: line <N>:` prefix to error messages from setVar/readonly
- [ ] Match bash error message wording exactly (e.g., `readonly variable` → same)
- [ ] Error messages for `printf` should match bash format
- [ ] Error messages for `read` should match bash format
- [ ] Use backtick quoting style matching bash (`` ` `` vs `'`)

### P2: Builtin Enhancements (affects ~30 tests)

- [x] `printf -v var` — write output to variable instead of stdout
- [ ] `printf %b` — interpret backslash escapes in argument
- [ ] `printf %(fmt)T` — datetime formatting
- [ ] `printf` full error handling matching bash
- [ ] `declare -f` display format matching bash (indentation, semicolons)
- [ ] `declare -p` output format matching bash
- [ ] `declare -i` integer arithmetic on assignment
- [x] `type -t` — output just type name (alias/keyword/function/builtin/file)
- [x] `type -a` — show all matches (factored through typeMatches helper)
- [x] `type -f` — skip function lookup
- [x] `type -p` — print path only if no higher-priority match
- [x] `type -P` — force PATH search
- [x] `command -V` — verbose command description (reuses typeMatches)
- [x] `return` outside function — already errors with proper message
- [x] `let` with multiple expressions — already worked; regression tests added
- [x] `select` loop construct — rewrote to actually loop and handle EOF/empty/invalid
- [ ] `mapfile -O origin` — start index, `-c count`, `-C callback`
- [ ] `read -N` nchars (don't stop at delimiter)
- [ ] `getopts` OPTERR variable, error message format

### P3: Expansion/Quoting Fixes (affects ~20 tests)

- [ ] Brace expansion with backslash quoting: `\{a,b\}` should not expand
- [ ] Brace expansion sequence step: `{0..10..2}` step handling
- [x] Brace expansion zero-padding: `{01..05}` → 01 02 03 04 05 (now also handles mixed widths like `{01..100}` and negative ranges)
- [ ] `$'...'` ANSI-C quoting edge cases
- [ ] IFS scoping: temporary IFS in simple commands vs eval/special builtins
- [ ] Word splitting with empty fields (IFS-related)
- [ ] Tilde expansion in assignments: `PATH=~:$PATH`
- [ ] `$"..."` locale translation strings
- [ ] Arithmetic base notation: `16#FF`, `2#1010`

### P4: Shell Variable Completeness

- [ ] `BASH_COMMAND` — set dynamically before each command (currently static)
- [ ] `BASH_EXECUTION_STRING` — store -c argument in runner
- [ ] `BASH_SUBSHELL` — verify increments correctly in all subshell types
- [ ] `COLUMNS` / `LINES` — terminal dimensions via term.GetSize()
- [ ] `PROMPT_DIRTRIM` — truncate \w in prompts
- [ ] `HISTCMD` — current history number
- [ ] `COMP_*` variables (COMP_WORDS, COMP_CWORD, COMP_LINE, COMP_POINT, COMPREPLY)
- [ ] `BASH_ALIASES` — associative array of aliases
- [ ] `BASH_CMDS` — associative array of hash table
- [ ] `BASH_COMPAT` — compatibility level
- [ ] `BASH_XTRACEFD` — redirect xtrace to FD
- [ ] `MAIL` / `MAILCHECK` / `MAILPATH`
- [ ] `READLINE_LINE` / `READLINE_POINT`

### P5: Interactive Features

- [ ] History expansion: `!!`, `!$`, `!n`, `!-n`, `!string`, `^old^new`
- [ ] `history` builtin: -c (clear), -d (delete), -a (append), -r (read), -w (write)
- [ ] `fc` builtin: -l (list), -s (re-execute), -e (edit)
- [ ] `bind` builtin: -p (list), -x (key to command)
- [ ] Programmable completion: compgen/complete/compopt full implementation
- [ ] Tab completion wired to readline
- [ ] `PROMPT_COMMAND` execution (done basic, needs array support)
- [ ] `PS0` display after command read, before execution
- [ ] `PS4` custom xtrace prefix (replace hardcoded "+ ")
- [ ] SIGWINCH → update COLUMNS/LINES

### P6: Job Control (real process groups)

- [ ] Process group management (Setpgid in exec.Cmd.SysProcAttr)
- [ ] Terminal control (tcsetpgrp)
- [ ] SIGTSTP (Ctrl-Z) to stop foreground job
- [ ] `fg %n` — tcsetpgrp + SIGCONT + wait
- [ ] `bg %n` — SIGCONT without terminal control
- [ ] `jobs` — proper status display (running/stopped/done)
- [ ] `kill` — send signals to process groups
- [ ] `disown -h` — mark jobs to not receive SIGHUP
- [ ] `wait -f` — wait for job to terminate (not just change state)

### P7: Remaining Shopt Options

- [ ] `checkjobs` — warn about running/stopped jobs on exit
- [ ] `cdspell` / `dirspell` — spelling correction
- [ ] `histappend` — append to history file on exit
- [ ] `histreedit` / `histverify` — re-edit/verify history substitutions
- [ ] `cmdhist` / `lithist` — multi-line history formatting
- [ ] `execfail` — don't exit on exec failure
- [ ] `localvar_inherit` / `localvar_unset` — local variable scoping
- [ ] `extdebug` — extended debugging
- [ ] `compat31` through `compat44` — version compatibility modes
- [ ] `direxpand` — expand directory names in completion
- [ ] `globasciiranges` — wire to pattern matching (marked supported, verify)
- [ ] `progcomp` / `progcomp_alias` — programmable completion

### P8: Polish

- [ ] `help` builtin with proper embedded text per builtin (//go:embed)
- [ ] `times` with real rusage data (syscall.Getrusage)
- [ ] Named FD redirections: allocate real FD numbers, close support
- [ ] `exec` replacing the process (unix.Exec)
- [ ] `.` (source) line number tracking for error messages
- [ ] Function display format matching bash exactly
- [ ] Heredoc with tabs (<<-) indentation stripping edge cases

### P9: POSIX Compliance

- [ ] Obtain Open Group VSX-PCTS test suite license
- [ ] Create tests/posix/ with POSIX shell compliance tests
- [ ] ShellSpec integration for portability testing
- [ ] POSIX mode (set -o posix) behavioral differences

---

## Test Progress Tracking

| Test | Status | Diff Lines | Blocking Issue |
|------|--------|-----------|----------------|
| extglob3 | PASS | 0 | — |
| invert | PASS | 0 | — |
| strip | PASS | 0 | — |
| nquote1 | PASS | 0 | — |
| nquote5 | FAIL | 5 | Word splitting empty field |
| ifs | FAIL | 8 | IFS scoping in eval/export |
| posix2 | FAIL | 13 | sh -c, ${10}+, eval case esac |
| dbg-support2 | FAIL | 14 | DEBUG trap line tracking |
| dynvar | FAIL | 15 | BASH_ARGV0 settable, BASH_COMMAND |
| iquote | FAIL | 15 | printf %#x edge cases |
| lastpipe | FAIL | 14 | declare -i arithmetic, PIPESTATUS |
| tilde | FAIL | 23 | ~+/~- expansion, tilde in assignments |
| parser | FAIL | 23 | ((true ) ) in case clause |
| tilde2 | FAIL | 27 | Tilde expansion edge cases |
| comsub-eof | FAIL | 28 | Command substitution EOF handling |
| exportfunc | FAIL | 28 | Exported function format |
| appendop | FAIL | 31 | += operator edge cases |
| nquote3 | FAIL | 32 | Quoting in various contexts |
| posixpat | FAIL | 33 | POSIX pattern matching |
| lastpipe | FAIL | 14 | declare -i, PIPESTATUS format |
| nquote4 | FAIL | 38 | Quoting edge cases |
| rsh | FAIL | 42 | Restricted shell |
| posixexp2 | FAIL | 43 | POSIX expansion edge cases |
| attr | FAIL | 46 | Variable attributes |
| posixpipe | FAIL | 48 | POSIX pipeline behavior |
| set-e | FAIL | 55 | errexit edge cases |
| nquote2 | FAIL | 56 | Quoting in expansions |
| casemod | FAIL | 59 | Case modification operators |
| rhs-exp | FAIL | 64 | Right-hand side expansion |
| extglob2 | FAIL | 65 | Extended glob edge cases |
| set-x | FAIL | 75 | xtrace format (PS4) |
| cprint | FAIL | 70 | $'...' printing |
| braces | FAIL | 77 | Brace expansion edge cases |
| intl | FAIL | 81 | $"..." locale strings |
| alias | FAIL | 88 | Alias expansion edge cases |
| arith-for | FAIL | 99 | C-style for loop arithmetic |
| comsub-posix | FAIL | 103 | POSIX command substitution |
| glob-bracket | FAIL | 107 | Bracket glob patterns |
| getopts | FAIL | 121 | getopts edge cases |
| quote | FAIL | 127 | Quoting comprehensive |
| vredir | FAIL | 131 | Variable redirections |
| read | FAIL | 133 | read builtin options |
| mapfile | FAIL | 142 | mapfile options |
| quotearray | FAIL | 155 | Array quoting |
| type | FAIL | 158 | type builtin output |
| test | FAIL | 173 | test/[ expressions |
| redir | FAIL | 187 | Redirection edge cases |
| extglob | FAIL | 194 | Extended glob patterns |
| trap | FAIL | 195 | Trap handling edge cases |
| more-exp | FAIL | 217 | Parameter expansion |
| func | FAIL | 390 | Function handling |
| globstar | FAIL | 468 | ** recursive glob |
| varenv | FAIL | 480 | Variable/environment |
| shopt | FAIL | 547 | Shell options |
| new-exp | FAIL | 813 | New expansion features |
| nameref | FAIL | 1033 | Name references |
| arith | FAIL | 372 | Arithmetic (blocked: += in ternary) |
| heredoc | FAIL | 171 | Heredoc (blocked: <<'') |
| comsub | FAIL | 100 | Comsub (blocked: ${ }) |
| comsub2 | FAIL | 195 | Comsub2 (blocked: ${ }) |
| cond | FAIL | 194 | Conditional (blocked: ${H*}) |
| coproc | TIME | — | Needs terminal |
| jobs | TIME | — | Needs terminal |

---

## Quick Reference

```bash
# Run all Go tests
go test ./...

# Run bash 5.3 test suite
make test-bash

# Run single bash test
cd external/bash-5.3/tests
THIS_SH=../../../bin/bashy PATH=$PWD:$PATH ../../../bin/bashy ./<name>.tests

# Compare output
diff <output> <name>.right
```
