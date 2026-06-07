# Bashy vs. GNU Bash 5.3 — Comprehensive Gap Analysis

Date: 2026-05-26
Reference bash sources: `/Users/qiangli/projects/poc/ai/sh/external/bash-5.3/`
Bashy sources: `/Users/qiangli/projects/poc/ai/sh/interp/`, `/Users/qiangli/projects/poc/ai/sh/syntax/`, `/Users/qiangli/projects/poc/ai/sh/expand/`, `/Users/qiangli/projects/poc/ai/sh/cmd/bashy/`

## Executive Summary

Bashy is now a credible scripting Bash on top of `mvdan/sh`. The parser handles
virtually all of Bash 5.3's command grammar (process substitution, coproc,
select, here-strings, named-fd `{var}>`), the expansion engine covers all
documented `${var@op}` transforms (`U/u/L/Q/E/A/a/K/k/P`), and every POSIX
special builtin plus the bulk of Bash's set is wired in. Phases 1-20 of the
existing roadmap closed most of the structural holes; what remains is largely
*finish work*: exact-match error message format, a handful of stubborn parser
constructs that bash accepts (`${ cmd; }` funsub, `((expr ) )` spacing,
`${H*}` inside `[[ ]]`), real job-control (process groups + TTY), readline
integration (history expansion `!!/!$`, tab completion through `complete`),
and the long tail of secondary shell variables (`MAIL*`, `COMP_*`,
`READLINE_*`, `BASH_COMPAT`, `BASH_XTRACEFD`).

The agentic story is open territory. None of the obvious differentiators
(JSON-emitting builtins, deterministic mode, per-runner resource limits,
audit/replay hooks, capability declarations) exist yet. Bashy's
`OpenHandler`/`ExecHandler` middleware already gives us most of the seams
we need; the work is wiring builtins, flags, and a small introspection
surface on top.

The "passing 4/83 bash 5.3 tests" headline understates progress: nearly all
the remaining failures are error-message mismatches (a single shared fixer
unblocks ~60 tests at once), not missing features. The shape of work that
remains is much smaller than that number suggests.

---

## Coverage Snapshot

| Area                             | Bashy coverage | Bash 5.3 surface | Priority |
|----------------------------------|----------------|------------------|----------|
| POSIX special builtins (15)      | ~95% (all wired) | full | done |
| Bash core builtins (40)          | ~80% (some stubs)| full | P2 |
| Parameter expansion `${...}`     | ~95% | full | P3 |
| `${var@op}` transforms           | 100% (10/10) | 10 ops | done |
| Arithmetic                       | ~85% | full | P2 |
| Brace expansion                  | ~90% (step, padding) | full | P3 |
| Process / command substitution   | ~95% (no `${ cmd; }`) | full | P0 |
| Here-doc / here-string           | ~95% (`<<''` edges) | full | P2 |
| Pattern matching / extglob       | ~85% | full | P2 |
| Arrays (indexed + assoc)         | ~90% | full | P3 |
| Nameref (`declare -n`)           | ~70% | full | P2 |
| Redirections (incl. `{var}>`)    | ~90% | full | P3 |
| Job control                      | ~40% (no PGID/TTY) | full | P5 |
| Programmable completion          | stubs only | full | P5 |
| Readline integration             | basic via ergochat | deep | P5 |
| History expansion (`!!`, `!$`)   | none | full | P5 |
| Shopt options (50+)              | ~12 supported | 53 in bash 5.3 | P4 |
| `set -o` options (21)            | ~14 supported | 21 | P3 |
| Special variables                | ~25 of ~50 | ~50 | P3 |
| Error message format             | ~30% bash-compatible | n/a | P1 |
| Agentic extensions               | none | n/a (new) | P0–P3 |

---

## 1. Builtins

### Missing entirely (no `case` arm, no recognition in `IsBuiltin`)

None. Every standard `*.def` name in `external/bash-5.3/builtins/` is now in
`IsBuiltin` (`interp/builtin.go:43-119`). Even outliers like `inlib` and the
reserved-word "builtins" (`for`, `while`, `case` from `reserved.def`) are
handled implicitly by the parser/dispatcher distinction.

### Stub-only / no-op (lots of room here)

| Builtin   | Current state                                              | Bash reference |
|-----------|------------------------------------------------------------|----------------|
| `bind`    | accepted, no-op (`interp/builtin.go:1535`)                 | `bashline.c:1500+`, `builtins/bind.def` |
| `fc`      | hard-error stub `fc: history not available`                | `builtins/fc.def` (full list/edit/re-exec) |
| `history` | prints "not available in non-interactive mode"             | `builtins/history.def` (`-c/-d/-a/-r/-w/-n/-s/-p`) |
| `suspend` | hard-error `suspend: not supported`                        | `builtins/suspend.def` |
| `ulimit`  | hint message, no-op                                        | `builtins/ulimit.def` (full RLIMIT_* surface) |
| `complete`/`compgen`/`compopt` | hard-error `programmable completion not yet implemented` | `builtins/complete.def` (full spec language) |
| `times`   | stub (no rusage data)                                      | `builtins/times.def`, real `getrusage` |
| `disown`  | basic, missing `-h` (no-SIGHUP marker)                     | `jobs.def:213`, `nohup_job` |
| `newgrp`  | hint, no-op                                                | `builtins/newgrp.def` (out of scope; documented) |
| `bg`      | succeeds only when bg job exists; no SIGCONT/PGID work     | `fg_bg.def:88` |
| `help`    | listing only, no per-builtin embedded text                 | `builtins/help.def:240+` (with `--`/short docs) |
| `caller`  | basic stack depth; missing `caller -e EXTDEBUG` semantics  | `builtins/caller.def` |

Effort: each is S–M; the heavy ones are `history` + `fc` (need a history backend) and `complete*` (needs the spec engine).

### Implemented but output/error format differs from bash (drives ~60 test failures)

- `printf` usage line: bashy says `usage: printf [-v var] format [arguments]` (`builtin.go:292`); bash says `printf: usage: printf [-v var] format [arguments]` (`builtins/printf.def`).
- `declare -p` / `declare -f` formatting: bashy uses Go-style `printValue`; bash uses its own quoting/indentation rules (`builtins/declare.def:120+`, `subst.c:string_var_assignment`).
- `type` / `command -V` output: minor wording deltas vs. `findcmd.c`/`type.def`.
- `read` error: `read: read error` vs. bash's `<file>: line N: read: read error`.
- All builtins lack the `<file>: line N:` prefix on errors (bash adds this via `command_substring_error` / `report_error`).
- `getopts` error format and OPTERR handling: bashy doesn't honor `OPTERR` (`getopts.def:90+`).
- Test/expression operators: missing `-v var` semantics consistent with `[[ -v arr[idx] ]]` (associative-aware).

Effort: most are S each, but the file-prefixed error message work is M and unblocks roughly 60 of the 79 failing bash tests at once.

### Missing options inside otherwise-implemented builtins

- `mapfile -O origin`, `-c count`, `-C callback`, `-s count` (`mapfile.def:26`).
- `read -N nchars` (distinct from `-n`; treats delimiter as data) (`read.def:307` `internal_getopt "...N:"`).
- `read -a array` works but assoc handling weak; `read -r -d ''` edge cases.
- `declare -i` arithmetic-on-assignment isn't enforced for subsequent assignments.
- `enable -d`, `enable -f filename` for loadable builtins (we have no loadable mechanism; acceptable, but document).
- `printf %q` quoting style differences vs. bash's `sh_quote_reusable`.
- `kill -L` (uppercase L = signal table); bashy maps it to `-l` only.

### moreinterp coreutils

`moreinterp/coreutils/coreutils.go:36-` already wires u-root core for `cat`, `chmod`, `cp`, `find`, `ls`, `mkdir`, `mktemp`, `mv`, `rm`, `tar`, `touch`, `xargs`, `base64`, `gzip`, `shasum`. This isn't a bash compatibility item but it's relevant for the agentic story (see Section 9).

---

## 2. Parser / Syntax Features

### Blocking parse errors (each kills one bash 5.3 test file outright)

| Construct                  | Bashy behaviour | Bash file:line |
|----------------------------|-----------------|----------------|
| `${ cmd; }` funsub         | parser rejects  | `parse.y:1115` (`DOLBRACE compound_list '}'`) |
| `${ (shift) }` subshell funsub | parser rejects | `parse.y:1115` + subst.c funsub extraction |
| `${H*}` inside `[[ ]]`     | `*` not treated as parameter-set pattern | `subst.c:array_value_internal` |
| `((true ) )`               | rejects double space before `)`  | `y.tab.c` arith parse |
| `case esac in esac)` `pat` | eval-time reparse rejects | `parse.y:case_clause` |

All of these are P0 (XL): the funsub work in particular requires both a parser
production and a runner path (the funsub runs the body inline as if it were a
command substitution, sharing the parent's variable scope — there's no `$()`
subshell isolation).

### Parser features present but with rough edges

- `<<-` indent-stripping handles tabs but mixed tab/space edge cases differ
  from bash (`parse.y:read_secondary_line`).
- `coproc NAME { ... }` named form parses; unnamed `coproc { ... }` minor
  COPROC array semantics gaps.
- `(( a += b ? c : d ))` ternary-RHS compound assignment — already fixed in
  this branch.
- `time -p` flag forwarding through `TimeClause` works for simple pipelines
  but doesn't report user/sys split when the kid is a builtin.

### Parser features in bash that bashy's parser likely does not emit nodes for

- `${|cmd;}` valsub (5.3 addition, `subst.c:extract_dollar_brace_string`,
  `parse.y:413` `funsub` rule extension).
- `\` line-continuation inside `((expr))` parsed differently between bash and
  the upstream `mvdan/sh` parser.

---

## 3. Variables (special / dynamic / readonly)

Bashy has 25-ish (`interp/vars.go:152-`). Bash 5.3 has ~50 in
`variables.c:1797-1900` plus the `special_vars[]` table at `variables.c:5760-`.

### Present in bash 5.3, missing or partial in bashy

| Variable                  | Bash role                                  | Bash source |
|---------------------------|--------------------------------------------|-------------|
| `BASH_COMMAND`            | Dynamic per-command currently-running text | `variables.c:1852` (`get_bash_command`) — bashy sets it but only on traps, not before every simple cmd |
| `BASH_EXECUTION_STRING`   | Stores `-c` argument                       | `variables.c:639` — missing in bashy |
| `BASH_COMPAT`             | Compatibility level                        | `variables.c:5760` `sv_shcompat` — missing |
| `BASH_XTRACEFD`           | FD to redirect xtrace to                   | `variables.c:5761` `sv_xtracefd` — missing |
| `BASH_MONOSECONDS`        | Monotonic clock                            | `variables.c:1855` — missing (5.3 addition) |
| `BASH_LOADABLES_PATH`     | Path for `enable -f`                       | `variables.c:697` — n/a (no loadable mechanism) |
| `BASH_ALIASES`            | Dynamic assoc array of aliases             | `variables.c:1897` `init_dynamic_assoc_var` — missing |
| `BASH_CMDS`               | Dynamic assoc array of hashed paths        | `variables.c:1895` — missing |
| `BASH_ENV`                | Sourced for non-interactive — wired in `cmd/bashy/main.go` | ok |
| `BASH_REMATCH`            | `[[ ]]` regex captures                     | wired ok |
| `BASH_ARGV`/`BASH_ARGC`   | Function-call argv stack                   | `variables.c:1889-90` — bashy has `BASH_ARGV0` only |
| `COMP_WORDBREAKS`         | Readline word-break chars                  | `variables.c:1878` — missing |
| `COMP_WORDS`, `COMP_CWORD`, `COMP_LINE`, `COMP_POINT`, `COMP_KEY`, `COMP_TYPE`, `COMPREPLY` | Set during completion functions | `pcomplete.c:740+` — none wired |
| `READLINE_LINE`, `READLINE_POINT`, `READLINE_MARK` | Set during `bind -x` callbacks | `bashline.c` — missing |
| `HISTCMD`                 | Current history number                     | `variables.c:1873` — missing |
| `HISTFILESIZE`, `HISTSIZE` callbacks | Dynamic propagation        | `variables.c:4969-71` — readline default only |
| `HISTCONTROL`, `HISTIGNORE`, `HISTTIMEFORMAT` | History filtering         | `variables.c:4968-72` — none active |
| `MAIL`, `MAILCHECK`, `MAILPATH` | Mailbox polling                      | `variables.c:4998-5000` — out of scope (modern noise) |
| `MAPFILE`                 | Default array name for `mapfile` (rarely used) | bash uses it as the default sink |
| `FUNCNEST`                | Function recursion limit                   | `variables.c:1776`, `setattr.c` — missing |
| `EXECIGNORE`              | Skip-exec patterns for command lookup      | `variables.c:4961` — missing |
| `GLOBIGNORE`              | Glob-skip patterns                         | `variables.c:4965` — missing |
| `IGNOREEOF`               | Number of Ctrl-Ds to exit                  | `variables.c:4984` — missing |
| `INPUTRC`                 | Readline init file                         | bashline.c — missing wiring |
| `OPTERR`                  | getopts error-print flag                   | `variables.c:5002` — missing |
| `PROMPT_COMMAND` (array)  | Wired, but only first element runs        | `variables.c` + `parser.c` — needs array iteration |
| `PROMPT_DIRTRIM`          | Truncate `\w`                              | `parse.y` prompt expansion — missing |
| `PS0`                     | Print after read, before exec              | `eval.c` — missing |
| `PS4`                     | xtrace prefix (currently hardcoded `+ `)   | `trace.c` — partial |
| `TIMEFORMAT`              | `time` builtin format                      | `execute_cmd.c:print_formatted_time` — missing |
| `TMOUT`                   | Timeout for `read` / interactive idle      | `eval.c:read_command` — missing |
| `LINES`, `COLUMNS`        | Terminal dimensions                        | `variables.c:4994` `sv_winsize` — missing |
| `OLDPWD`                  | Previous PWD                               | bashy sets via `cd -` path but not bound as readonly |
| `POSIXLY_CORRECT`         | Triggers `set -o posix`                    | `variables.c:5006` — partial |

Effort: most are S each (read-only or simple set hook); `BASH_COMMAND`
per-command and `COMP_*` array set are M each.

### Settable / read-only attributes

Bash has fine-grained attribute flags (`att_readonly`, `att_export`,
`att_integer`, `att_array`, `att_assoc`, `att_function`, `att_trace`,
`att_uppercase`, `att_lowercase`, `att_capcase`, `att_nameref`, `att_invisible`,
`att_nofree`, `att_imported`, `att_special`, `att_noassign`, `att_nounset`)
declared in `variables.h`. Bashy's `expand.Variable` has `Kind`, `ReadOnly`,
`Exported`, `Local`, `NameRef` only — missing the case-attribute group
(`declare -u`, `declare -l`, `declare -c`), `att_trace` for function tracing,
`att_invisible` for "exists but no value yet", and the noassign/nounset
attribute toggles.

---

## 4. Expansions

### Parameter `${var@op}` — DONE

All 10 transforms (`U/u/L/Q/E/A/a/K/k/P`) implemented in
`expand/param.go:323-369`. No gap.

### Parameter — substring, slice, prefix

| Form                       | Bashy state | Notes |
|----------------------------|-------------|-------|
| `${var:offset:length}`     | works for strings; partial for arrays | bash: `subst.c:parameter_brace_substring` |
| `${var:offset}` (no length)| works | |
| `${!prefix*}`, `${!prefix@}` (variable name prefix) | works | |
| `${!arr[@]}`, `${!arr[*]}` (array keys) | works | |
| `${!ref}` (indirect through string)     | works | |
| `${var/pat/rep}`, `${var//pat/rep}`, `${var/#pat/rep}`, `${var/%pat/rep}` | works | bash 5.3 also has `${var@P}` formatting interplay |
| `${var^}`, `${var^^}`, `${var,}`, `${var,,}` | works | |
| `${var~}`, `${var~~}` (case toggle, mksh extension that bash partially honors) | works | added recently |

No identified gap in parameter expansion proper, except inside `[[ ... ]]`:
`${H*}` is misparsed as set-membership (see Section 2).

### Arithmetic

- Base notation `16#FF`, `2#1010`, `0x` hex, `0` octal — partial (binary `2#` rejected in some paths; see `expand/arith.go`).
- Comma operator `(( a, b, c ))` — works.
- Ternary `?:` with compound assignment RHS — fixed in this branch.
- Pre/post increment/decrement — works.
- `**` exponent — works.
- `~`, `<<`, `>>` — works.
- Floating point — bash doesn't have it either (intentional).

### Brace expansion

- `{a,b,c}` — works.
- `{1..5}` numeric sequence — works.
- `{a..z}` letter sequence — works.
- `{01..05}` zero-padded — recently fixed.
- `{0..10..2}` step — partial (TODO in `docs/TODO.md`).
- `\{a,b\}` backslash-quoted suppression — broken (TODO).
- Nested braces `{a,{b,c}d}` — works.
- Bash 5.3 source: `braces.c:expand_amble`.

### Tilde

- `~`, `~/path`, `~user` — works.
- `~+` (PWD), `~-` (OLDPWD) — broken (TODO).
- `PATH=~:foo:~/bin` (assignment-context tilde) — broken (TODO).
- Bash 5.3 source: `general.c:bash_tilde_expand`.

### Other expansions

- `$"localized string"` (gettext) — not implemented; bashy strips the `$` only. `subst.c:locale_expand`.
- `$'ANSI-C\nstring'` — works; some edge cases in `\U`, `\x` differ.
- `<(...)` process substitution — works (`syntax.ProcSubst` runtime).
- `>(...)` — works.
- `$(<file)` shortcut — works (special-cased in command sub).
- Pathname expansion: `**` globstar, `**/*` — works when `shopt globstar` enabled.

---

## 5. Job Control

Bashy's job model is goroutine-based and has no concept of process groups.
Bash 5.3's job control lives in `jobs.c` (3800+ lines) and `jobs.h`.

| Feature                       | Bashy | Bash 5.3 reference |
|-------------------------------|-------|---------------------|
| Process groups (`setpgid`)    | none  | `execute_cmd.c:make_child` |
| Terminal control (`tcsetpgrp`)| none  | `jobs.c:give_terminal_to` |
| SIGTSTP (`Ctrl-Z`)            | none  | `jobs.c:waitchld` STOPSIG branch |
| `fg %N` with TTY reattach     | wait-only | `fg_bg.def:start_job` |
| `bg %N` with SIGCONT          | none-op | `fg_bg.def:start_job` (`async` arg) |
| `jobs -p`, `-l`, `-n`, `-r`, `-s` | partial (`-p`/no-flag) | `jobs.def` |
| Jobspec `%+`, `%-`, `%?str`, `%str` | not parsed | `jobs.c:get_job_spec` |
| `disown -h` (no-SIGHUP marker)| not implemented | `jobs.def:213` |
| `wait -f` (forced exit, not just state change) | not implemented | `wait.def` |
| Notifications `[1]+ Done foo` | none  | `jobs.c:notify_of_job_status` |
| `set -m` monitor mode         | accepted, no-op | `set.def:216` |
| `set -b` (notify on bg exit)  | accepted, no-op | `set.def:225` |

Effort: XL. Real job control is a single multi-week project. It is the
single largest interactive-shell gap.

---

## 6. Interactive Features

### Readline depth

`cmd/bashy/interactive.go` uses `github.com/ergochat/readline` for line
editing, history-up/down, and Ctrl-R search. Bash's readline integration
(`bashline.c`, 4000+ lines) does much more:

- Programmable completion via `complete -F func` callback into the shell.
- `bind -x KEYSEQ:command` runs shell code as a key binding.
- `bind -p` lists current key bindings.
- INPUTRC file parsing (`~/.inputrc` / `/etc/inputrc`).
- `set -o vi` / `set -o emacs` switches readline editor mode.
- Per-application bindings (`$if Bash`).
- `enable-bracketed-paste`, `colored-completion-prefix`, etc.

### History expansion (`histexpand.c` in bash; missing entirely in bashy)

| Designator | Meaning |
|------------|---------|
| `!!`       | previous command |
| `!N`       | history entry N |
| `!-N`      | N-th most recent |
| `!str`     | most recent starting with `str` |
| `!?str?`   | most recent containing `str` |
| `^old^new^`| substitute on previous command |
| `!$`       | last argument of previous |
| `!*`       | all args of previous |
| `!:N`      | argument N of previous |
| `!:N-M`    | argument range |
| Modifiers `:h`, `:t`, `:r`, `:e`, `:p`, `:s/old/new/`, `:&`, `:g`, `:a` | |

All missing.

### Prompt

Bashy expands PS1/PS2 via `prompt.go` with `\h`, `\u`, `\w`, `\W`, `\$`,
`\d`, `\t`, `\A`, `\T`, `\@`, `\j`, `\!`, `\#`, `\s`, `\v`, `\V`, `\n`,
`\r`, `\a`, `\\`, `\[`/`\]` non-printing markers. Missing:

- `\D{format}` strftime.
- `\j` job count (we return 0 always since no real jobs).
- PROMPT_DIRTRIM truncation of `\w`.
- PS0 (after-read, pre-exec).
- PS4 custom xtrace prefix (currently hardcoded `+ ` in `trace.go`).

### SIGWINCH

Not wired. Bash reinstalls a handler in `bashline.c:rl_set_screen_size` and
updates `COLUMNS`/`LINES` on `SIGWINCH`.

---

## 7. POSIX / `set -o` Options

Bashy has 9 POSIX options + 5 honoured Bash extensions (`api.go:759-770`).
Bash 5.3 has 21 (`builtins/set.def:194-238`).

### `set -o` options present in bash, missing or no-op in bashy

| Option              | Bash flag | Notes |
|---------------------|-----------|-------|
| `braceexpand`       | `-B`      | bashy accepts, brace expansion always on |
| `emacs`             |           | edit mode toggle; missing |
| `vi`                |           | edit mode toggle; missing |
| `errtrace`          | `-E`      | ERR trap inheritance; partial (we have ERR trap, no inherit flag) |
| `functrace`         | `-T`      | DEBUG/RETURN trap inheritance; partial |
| `hashall`           | `-h`      | bashy hashes anyway, but no toggle |
| `histexpand`        | `-H`      | history substitution; n/a (no histexp) |
| `history`           |           | enable/disable history list; partial via readline |
| `ignoreeof`         |           | trap Ctrl-D N times before exit; missing |
| `interactive-comments` |        | comments in interactive shells; missing (we always allow) |
| `keyword`           | `-k`      | all assignments treated as env; missing |
| `monitor`           | `-m`      | job control; partial (no PGID) |
| `nolog`             |           | don't save fn defs in history; n/a |
| `notify`            | `-b`      | notify of job completion; missing |
| `onecmd`            | `-t`      | exit after one command; missing |
| `physical`          | `-P`      | don't resolve symlinks in cd; missing flag (cd `-P` works) |
| `privileged`        | `-p`      | turn off `$ENV`, `$BASH_ENV`; missing |

Effort: most are S–M each; `privileged` is M because it interacts with
startup-file loading and EUID semantics.

---

## 8. Shopt Options

Bashy has 12 supported + ~30 accepted-but-no-op (`api.go:772-888`). Bash 5.3
shopt table at `builtins/shopt.def:180-270` has these names not yet wired:

| Option (bash 5.3)         | Bashy state | Notes |
|---------------------------|-------------|-------|
| `array_expand_once`       | unknown | new in 5.3 |
| `assoc_expand_once`       | accepted no-op | |
| `bash_source_fullpath`    | unknown | new in 5.3 (full path in BASH_SOURCE) |
| `cdable_vars`             | accepted | |
| `cdspell`                 | accepted | spelling correction |
| `checkhash`               | accepted | |
| `checkjobs`               | accepted | warn on running jobs at exit |
| `checkwinsize`            | accepted | |
| `cmdhist`                 | accepted | multi-line as one entry |
| `compat31..44`            | accepted | compat modes |
| `complete_fullquote`      | accepted | |
| `direxpand`               | accepted | |
| `dirspell`                | accepted | |
| `execfail`                | accepted | don't exit on exec failure |
| `extdebug`                | accepted | extended debug — affects `caller`, BASH_ARGV |
| `extquote`                | accepted | |
| `force_fignore`           | accepted | |
| `globskipdots`            | unknown | new in 5.3, skips `.` `..` in `*` |
| `gnu_errfmt`              | accepted | |
| `histappend`/`histreedit`/`histverify` | accepted | |
| `hostcomplete`            | accepted | |
| `lithist`                 | accepted | |
| `localvar_inherit`/`localvar_unset` | accepted | both relevant for nameref tests |
| `login_shell`             | bashy has it via `WithLoginShell`, but `shopt -p login_shell` doesn't reflect it |
| `mailwarn`                | accepted | |
| `no_empty_cmd_completion` | accepted | |
| `noexpand_translation`    | unknown | new in 5.3 (suppresses `$"..."` translation) |
| `patsub_replacement`      | unknown | new in 5.3 (controls `&` in replacement of `${var//pat/rep}`) |
| `progcomp`/`progcomp_alias` | accepted no-op | |
| `promptvars`              | accepted | |
| `restricted_shell`        | accepted | `rsh` test fails because of this |
| `shift_verbose`           | accepted | |
| `syslog_history`          | unknown | bash 5.3 (would never implement for security) |
| `varredir_close`          | unknown | new in 5.3 (closes named-fd on stmt exit) |

Effort: per option mostly S. `extdebug`, `localvar_inherit`/`localvar_unset`,
and `patsub_replacement` each unblock a specific bash 5.3 test family.

---

## 9. Agentic Extensions (summary; see `docs/agentic-extensions.md` for full design)

The OpenHandler/ExecHandler/ReadDirHandler/StatHandler/CallHandler middleware
in `interp/api.go` is already in place; almost every agentic feature can be
built on top without breaking compatibility. The eight highest-leverage:

1. **`--json` structured-output mode** for in-process builtins (`jobs`, `type`, `declare -p`, `set`, `trap -p`, `times`, `kill -l`).
2. **`runner-state` introspection builtin** dumping vars/traps/fds/options as JSON.
3. **Deterministic mode (`set -o deterministic` or `BASHY_DETERMINISTIC=1`)** that freezes `$RANDOM`, `$SRANDOM`, `$SECONDS`, `$EPOCH*`, `$BASHPID`, `$$`, `$!`, mtime-dependent globbing.
4. **Per-runner resource limits** (CPU time, wall time, output bytes, child count) checked in `ExecHandler` middleware and on builtin entry.
5. **Sandbox mode** layered on `OpenHandler`/`StatHandler` allow-listing paths.
6. **Audit hook** (`AuditHandler func(cmd []string, where Pos)`) fired pre-exec, pre-builtin, pre-trap; gives an embedder a complete provenance trail.
7. **Dry-run / `--explain` mode** that parses and reports what would execute without spawning.
8. **Capability declarations** (`#!bashy require net,fs,git`) parsed from script preamble and enforced.

---

## Recommended Next Batches (top 5)

### Batch A — Error-message format pass (M, ~60 tests unlocked)

Single concentrated effort to:
1. Add a `(file, line)` pair to every `failf` invocation (the parser already
   tracks pos via `r.curStmt`).
2. Replace `"usage: ..."` with `"<name>: usage: ..."` everywhere.
3. Replace single-quote (`'foo'`) error quoting with bash's mixed
   backtick-leading-single-quote-trailing style (`` `foo' ``).
4. Match exact wording: `readonly variable`, `not a valid identifier`,
   `command not found`, `bad substitution`, etc.

Payoff: ~60 bash 5.3 tests pass without any new feature work. Highest
ROI work unit by a wide margin.

### Batch B — `${ cmd; }` funsub + companion parser fixes (XL, ~6 tests unlocked, removes 2 P0 blockers)

Status on 2026-06-07: **not complete**. Targeted verification against
`bashy` showed all broader Batch B target files still failing. Current
partial progress: arithmetic ternary false-branch assignment precedence
now matches bash (`1 ? 20 : x+=2` errors as assignment to a non-variable),
and invalid base constants such as `3425#56`, `2#`, and `2#44` now emit
bash-shaped arithmetic errors. B5 source-text preservation is partially
wired for bashy arithmetic diagnostics, fixing the early `7 = 43`,
`44 / 0`, and `b /= 0` spacing mismatches. B7 `${#OP}` parser tolerance is
also wired: `${#:}`, `${#/}`, `${#%}`, `${#=}`, `${#+}`, `${#1xyz}`, and
`${#:%}` now reach bash-shaped runtime diagnostics. The first remaining
`arith` divergence is now `let 'jv += $iv'` arithmetic operand wording; the
first remaining `arith-for` arithmetic-error divergence is `(( j= ))`.

| Test | Current result | Diff lines |
|---|---:|---:|
| `arith` | FAIL | 10570 |
| `arith-for` | FAIL | 32 |
| `comsub` | FAIL | 97 |
| `comsub2` | FAIL | 204 |
| `comsub-eof` | FAIL | 46 |
| `cond` | FAIL | 106 |
| `more-exp` | FAIL | 78 |
| `heredoc` | FAIL | 152 |

The narrow parser-wording sweep in `docs/plan-bash53-roadmap-agentic.md`
is complete (`parser`, `exportfunc`, and `posixpat` pass), but it should
not be confused with this broader parser/arith/funsub batch.

1. Parser: add `funsub` production cloned from bash `parse.y:1115` (`DOLBRACE compound_list '}'`).
2. AST: a new `FuncSubst` `WordPart` (or repurpose `CmdSubst` with a flag).
3. Runtime: execute the body in the **same** variable scope as the caller
   (no subshell) — the semantic difference vs. `$()`. Capture stdout into the
   substituted string.
4. While inside the same file: `((expr ) )` arithm parse, `${H*}` pattern in
   `[[ ]]`, `cat <<''` empty heredoc (already pass).

Payoff: comsub, comsub2, cond, parser, and a couple other tests pass.
Also lays groundwork for `${|cmd;}` valsub later.

### Batch C — Agentic batch 1: deterministic mode + audit hook + JSON output (M)

1. New `RunnerOption` `Deterministic(bool)` and `BASHY_DETERMINISTIC=1` env
   trigger. Freezes the random/time-derived vars.
2. New `AuditHandler(func(ctx, args []string, pos Pos))` invoked from every
   builtin and `ExecHandler`. Default no-op.
3. New `--json` flag on `jobs`, `declare -p`, `trap -p`, `set` for
   machine-readable output.

Payoff: makes bashy distinctly more useful than vanilla bash for any
LLM-driven harness; no compatibility risk; small surface.

### Batch D — Real job control phase 1 (L)

1. Set `Setpgid: true` in `exec.Cmd.SysProcAttr` on Unix.
2. Implement `bgProc.pgid` and SIGCONT distribution to the group.
3. `kill %N` resolves to the pgid and signals the whole group.
4. `jobs -p`, `-l`, `-r`, `-s`, `-n` proper outputs.
5. Job-spec parsing for `%+`, `%-`, `%?str`, `%str`.

Phase 2 (TTY control + SIGTSTP) is a separate batch.

### Batch E — Programmable completion (L)

1. `complete` registry in Runner: `map[command]*CompletionSpec`.
2. `complete -F func`, `-W wordlist`, `-G glob`, `-C cmd`, `-A action`,
   `-P prefix`, `-S suffix`, `-X filter`, `-o option`.
3. `compgen` evaluates a spec on demand against a word.
4. Runtime: bind `COMP_WORDS`, `COMP_CWORD`, `COMP_LINE`, `COMP_POINT` before
   calling `-F func` body; read `COMPREPLY` after.
5. Wire to `readline.Config.AutoComplete` callback.

Payoff: a major interactive feature; also relevant for agents that want
context-aware suggestion lookups.

---

## Appendix — How to verify

```bash
make test-bash                                 # 4/83 passing today
go test ./...                                  # all green
go test -run TestRunnerRunConfirm ./interp     # bash 5.3 confirm tests
```

Each batch above should be measured by:

1. New bash 5.3 tests passing (delta against the table in
   `docs/report-bash53-test-status.md`).
2. No regression in `go test ./...`.
3. For agentic features: a focused test in `interp/interp_test.go` proving
   the new surface.
