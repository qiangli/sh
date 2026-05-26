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
- [x] `((true ) )` — arithmetic with space before `)` in case clause (peekArithmEnd skips horizontal whitespace)
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
- [x] `printf %b` — interpret backslash escapes in argument (already worked; regression tests added)
- [x] `printf %(fmt)T` — datetime formatting (strftime subset; -1 = now, -2 = shell start, integer = Unix timestamp)
- [x] `printf --` — argument terminator (already worked via flagParser; regression test added)
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
- [x] Brace expansion sequence step: `{0..10..2}` step handling (now uses |step| with sign matching range direction; {10..1..2} → 10 8 6 4 2)
- [x] Brace expansion zero-padding: `{01..05}` → 01 02 03 04 05 (now also handles mixed widths like `{01..100}` and negative ranges)
- [x] `$'...'` ANSI-C `\cX` control-char escape (\cA → 0x01, \c@ → 0x00 etc.) — other ANSI-C escapes already worked
- [ ] IFS scoping: temporary IFS in simple commands vs eval/special builtins
- [ ] Word splitting with empty fields (IFS-related)
- [x] Tilde expansion in assignments: `PATH=~:$PATH` (LiteralForAssign + tildeInAssign flag)
- [ ] `$"..."` locale translation strings
- [x] Arithmetic base notation: `16#FF`, `2#1010` (bases 2-64 with bash's extended digit alphabet for 37-64)

### P4: Shell Variable Completeness

- [ ] `BASH_COMMAND` — set dynamically before each command (currently static)
- [x] `BASH_EXECUTION_STRING` — set by cmd/bashy from the -c argument (env-passed before runner construction)
- [ ] `BASH_SUBSHELL` — verify increments correctly in all subshell types
- [x] `COLUMNS` / `LINES` — terminal dimensions via term.GetSize() (probes stdin/stdout/stderr; empty when no TTY)
- [x] `PROMPT_DIRTRIM` — truncate \w in prompts (positive integer keeps last N components, prepends ".../")
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

---

## Bash 5.3 Gap Analysis (from comprehensive audit, 2026-05-26)

Full reports in `docs/bash-gap-analysis.md` and `docs/agentic-extensions.md`.
Items below are organized by priority and tagged by effort: S (1 commit),
M (a session), L (multi-session), XL (cross-cutting). Anything already
covered by an earlier section above is NOT repeated here.

### G0: Error-format pass (M, unlocks ~60 bash 5.3 tests)

- [ ] `<file>: line N:` prefix on every `failf` site (use `r.curStmt` pos)
- [ ] `<name>: usage: ...` ordering (vs. current `usage: <name>`) — match `printf`, `read`, `getopts`, etc.
- [ ] Quote style: bash uses `` `foo' `` (backtick + single-quote); bashy uses `'foo'` — change globally
- [ ] Exact wording match for: `command not found`, `bad substitution`, `not a valid identifier`, `readonly variable`, `unbound variable`, `cannot create temp file`, `arithmetic syntax error`
- [ ] Verify `bash --posix` mode output matches bash's `--posix` variants

### G1: Parser blockers (XL, unlocks 6 tests)

- [ ] `${ cmd; }` funsub parser production (`parse.y:1115`), `FuncSubst` AST node, runtime that runs the body in caller's scope (no subshell)
- [ ] `${ (shift) }` subshell-within-funsub
- [ ] `${H*}` — treat `*` as parameter-set pattern inside `[[ ]]`
- [ ] `((true ) )` — accept whitespace before closing `)` in case-clause arithm
- [ ] `case esac in esac)` — eval-time reparse of unusual case patterns
- [ ] `${|cmd;}` valsub (bash 5.3, separate from funsub)

### G2: Stub builtins worth finishing (M each)

- [ ] `complete`/`compgen`/`compopt` — full spec engine (`-F/-W/-G/-C/-A/-X/-P/-S/-o`), wire to readline tab callback (L)
- [ ] `history` — `-c/-d/-a/-r/-w/-n/-s/-p` on `~/.bashy_history` (M)
- [ ] `fc` — `-l/-s/-e/-n/-r` re-execute and edit (M)
- [ ] `bind` — `-p/-l/-x KEYSEQ:command/-r/-q/-u/-m keymap/-f file` (M)
- [ ] `disown -h` — mark jobs to skip SIGHUP (S)
- [ ] `help` — embed bash-style per-builtin help text (//go:embed) (S)
- [ ] `times` — `syscall.Getrusage(RUSAGE_SELF/CHILDREN)` (S)
- [ ] `ulimit` — at minimum: `-n` (file desc), `-u` (procs), `-t` (cpu time), `-f` (file size); respect cap from `setrlimit` (M)

### G3: Builtin completeness (S–M each)

- [ ] `mapfile -O origin`, `-c count`, `-C callback`, `-s count` (`builtins/mapfile.def:26`)
- [ ] `read -N nchars` (distinct from `-n`)
- [ ] `read -a array` for assoc arrays
- [ ] `declare -p` formatting matching `subst.c:string_var_assignment`
- [ ] `declare -f NAME` formatting matching bash (indent, semicolons, function header)
- [ ] `declare -i` enforce arithmetic-on-assignment for subsequent assignments
- [ ] `declare -u/-l/-c` case-attribute auto-transform (`att_uppercase`/`lowercase`/`capcase`)
- [ ] `printf %q` to use bash's `sh_quote_reusable` style
- [ ] `kill -L` (uppercase = signal table) alias
- [ ] `getopts` OPTERR variable, leading-colon-in-optstring silent mode
- [ ] `caller -e EXTDEBUG` extended-debug semantics
- [ ] `command --explain foo` (new; from agentic extensions)

### G4: Variables — secondary set (S each)

- [ ] `BASH_COMMAND` set before *every* simple command, not just traps
- [ ] `BASH_EXECUTION_STRING` — store `-c` argument
- [ ] `BASH_COMPAT` — accept and validate compatibility level
- [ ] `BASH_XTRACEFD` — redirect xtrace output to FD
- [ ] `BASH_ALIASES` — dynamic assoc array of aliases
- [ ] `BASH_CMDS` — dynamic assoc array of hashed paths
- [ ] `BASH_ARGV`/`BASH_ARGC` — function-call argv stack (requires `extdebug`)
- [ ] `BASH_MONOSECONDS` — monotonic clock (new in 5.3)
- [ ] `HISTCMD` — current history entry number
- [ ] `HISTCONTROL`, `HISTIGNORE`, `HISTTIMEFORMAT` — history filtering
- [ ] `FUNCNEST` — function recursion limit (default unlimited)
- [ ] `EXECIGNORE` — skip-exec patterns for command lookup
- [ ] `GLOBIGNORE` — glob-skip patterns
- [ ] `IGNOREEOF` — Ctrl-D count before exit
- [ ] `INPUTRC` — readline init file path
- [ ] `OPTERR` — getopts error-print flag
- [ ] `PROMPT_COMMAND` as array — iterate all entries
- [ ] `PROMPT_DIRTRIM` — truncate `\w`
- [ ] `PS0` — print after read, before exec
- [ ] `PS4` — replace hardcoded `+ ` in trace.go with expanded PS4
- [ ] `TIMEFORMAT` — for `time` builtin output
- [ ] `TMOUT` — interactive idle / `read` default timeout
- [x] `LINES`, `COLUMNS` — terminal dimensions via `golang.org/x/term`
- [ ] `OLDPWD` — bind as set-by-cd readonly-after-set
- [ ] `COMP_WORDS`, `COMP_CWORD`, `COMP_LINE`, `COMP_POINT`, `COMP_KEY`, `COMP_TYPE`, `COMPREPLY`, `COMP_WORDBREAKS` — set during completion functions
- [ ] `READLINE_LINE`, `READLINE_POINT`, `READLINE_MARK` — set during `bind -x` callbacks

### G5: Variable attributes (M)

- [ ] `declare -u` / `att_uppercase` — auto-uppercase on assignment
- [ ] `declare -l` / `att_lowercase` — auto-lowercase on assignment
- [ ] `declare -c` / `att_capcase` — auto-capitalize on assignment
- [ ] `att_invisible` — variable exists but has no value yet
- [ ] `att_trace` — function tracing for `set -o functrace`

### G6: `set -o` options (S each)

- [ ] `braceexpand` `-B` — accept toggle (always on)
- [ ] `emacs` / `vi` — switch readline edit mode
- [ ] `errtrace` `-E` — ERR trap inheritance
- [ ] `functrace` `-T` — DEBUG/RETURN trap inheritance
- [ ] `hashall` `-h` — toggle command hashing
- [ ] `ignoreeof` — Ctrl-D count before exit
- [ ] `interactive-comments` — `#` in interactive shells
- [ ] `keyword` `-k` — all `name=value` treated as env
- [ ] `notify` `-b` — async notify of bg completion
- [ ] `onecmd` `-t` — exit after one command
- [ ] `physical` `-P` — don't resolve symlinks in cd
- [ ] `privileged` `-p` — disable startup files and `$ENV`

### G7: Shopt options (S each)

- [ ] `globskipdots` — skip `.`/`..` in `*` (new in 5.3, default on)
- [ ] `patsub_replacement` — `&` in replacement of `${var//pat/rep}` (default on in 5.3)
- [ ] `noexpand_translation` — suppress `$"..."` translation
- [ ] `varredir_close` — close named-fd on stmt exit
- [ ] `bash_source_fullpath` — full path in BASH_SOURCE (new in 5.3)
- [ ] `array_expand_once` — controls re-expansion in `[[ ]]`
- [ ] `extdebug` — enable BASH_ARGV/BASH_ARGC stack, `caller`-with-source line
- [ ] `localvar_inherit` — local vars inherit value from enclosing scope
- [ ] `localvar_unset` — local vars without value start unset (not "")
- [ ] `cdspell`, `dirspell` — Levenshtein corrections
- [ ] `restricted_shell` — actually enforce restrictions (for `rsh` test)
- [ ] `histappend`, `histreedit`, `histverify`, `cmdhist`, `lithist`, `mailwarn` — connect to history backend
- [ ] `login_shell` — reflect `WithLoginShell` state in `shopt -p`

### G8: Job control phase 1 (L)

- [ ] `Setpgid: true` on `exec.Cmd.SysProcAttr` (Unix)
- [ ] Track per-bgProc `pgid`
- [ ] `kill %N` resolves to pgid; signals whole group
- [ ] `kill 0` — signal current process group
- [ ] Jobspec parsing: `%+`, `%-`, `%?str`, `%str`, `%%`
- [ ] `jobs -p` (PID only), `-l` (long format with PID), `-n` (changed-since-last), `-r` (running), `-s` (stopped), `-x cmd` (substitute jobspec)
- [ ] `[1]+ Done <cmd>` status notification on prompt

### G9: Job control phase 2 (XL)

- [ ] TTY control (`tcsetpgrp` via golang.org/x/sys/unix)
- [ ] SIGTSTP (Ctrl-Z) handler — stop foreground job, push to bg table
- [ ] `fg %N` — tcsetpgrp + SIGCONT + wait, restore TTY on exit
- [ ] `bg %N` — SIGCONT only
- [ ] `wait -f` — wait for terminal state, not just status change

### G10: Readline depth (L)

- [ ] Tab completion through `complete`/`compgen` registry (depends on G2)
- [ ] `bind -p` / `-l` / `-x KEYSEQ:cmd` / `-r` / `-q` / `-u` / `-f file`
- [ ] `~/.inputrc` / `/etc/inputrc` parsing (consider `xo/inputrc`)
- [ ] `set -o vi` / `set -o emacs` mode switching at runtime
- [ ] SIGWINCH handler — update `COLUMNS`/`LINES`

### G11: History expansion (M, separate from `history` builtin)

- [ ] `!!`, `!N`, `!-N`, `!str`, `!?str?`, `!$`, `!*`, `!:N`, `!:N-M`
- [ ] `^old^new^` substitution
- [ ] Modifiers: `:h`, `:t`, `:r`, `:e`, `:p`, `:s/old/new/`, `:&`, `:g`, `:a`
- [ ] `histchars` variable (default `!^#`) — change the trigger char

### G12: Locale and i18n (M)

- [ ] `$"..."` gettext translation (use `golang.org/x/text/message`)
- [ ] `LC_ALL`, `LC_COLLATE`, `LC_CTYPE`, `LC_NUMERIC`, `LC_TIME` — wire through `unicode/utf8` and `time` formatters
- [ ] Case modification respect locale (currently uppercase via Unicode tables only)

### G13: Agentic extensions (see docs/agentic-extensions.md)

- [ ] **#1 Deterministic mode**: `set -o deterministic`, `BASHY_DETERMINISTIC=N` (S–M)
- [ ] **#2 `--json` flag** on `jobs`, `declare -p`, `declare -F`, `trap -p`, `set`, `set -o`, `shopt -p`, `type`, `times`, `kill -l` (S each, do all in one session)
- [ ] **#3 `runner-state` builtin** with subcommands `vars`/`traps`/`fds`/`opts`/`callstack`/`all` (S)
- [ ] **#4 Resource limits**: `WithMaxWallTime`, `WithMaxCPUTime`, `WithMaxOutputBytes`, `WithMaxChildProcs`, `WithMaxOpenFiles`; new builtin `limits` (M)
- [ ] **#5 Sandbox mode**: `WithSandboxRoots(read, write)`, `BASHY_SANDBOX_READ/WRITE` env, `sandbox-status` builtin (M)
- [ ] **#6 Audit hook**: `WithAuditHandler(func(AuditEvent))`, optional `BASHY_AUDIT_LOG=path.jsonl` (S)
- [ ] **#7 Dry-run mode**: `--dry-run` flag emitting `[would-run]` per leaf cmd; `command --explain foo` (M for full, S for explain only)
- [ ] **#8 Capability declarations**: `# bashy: requires net,fs-write` preamble + `require` builtin + `WithCapabilities(set)` option (S–M)
- [ ] **#9 Structured errors**: `WithStructuredErrors(func(ErrorEvent))` carrying kind/severity/pos/function (S)
- [ ] **#10 Record / replay**: `BASHY_RECORD=path.jsonl` and `bashy --replay file [--strict|--lax]` (M)
- [ ] **#11 Inline docs**: `bashy explain <name>` from `//go:embed help/*.md` (S; value is content)
- [ ] **#12 Cancellation audit**: verify `ctx.Done()` propagates into all loops/bg procs; add `WithCancelHook` (M)
- [ ] **#13 Embedder builtins**: `WithExtraBuiltins(map[string]BuiltinFunc)` (S)
- [ ] **#14 Metrics handler**: `WithMetricsHandler(func(Metric))` (S)
- [ ] **#15 Policy file**: `~/.bashy/policy.toml` or `.bashy.toml` with options/deny/caps sections (M)

### Recommended next batches (from gap-analysis Section "Recommended next batches")

1. **Batch A**: Error-message format pass (G0) — ~60 tests for one session
2. **Batch B**: `${ cmd; }` funsub + parser fixes (G1) — XL, unlocks comsub/comsub2/cond/parser tests
3. **Batch C**: Agentic batch 1 — G13 items #1 (deterministic), #6 (audit), #2 (json), #3 (runner-state)
4. **Batch D**: Job control phase 1 (G8)
5. **Batch E**: Programmable completion (part of G2)

