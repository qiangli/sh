# Bash 5.3 compliance roadmap — agentic-tool drop-in

Goal: make `bashy` a drop-in bash 5.3 replacement for agentic tooling.
Strict-byte-match against the official bash 5.3 test suite (`make
test-bash`) is the headline metric. Interactive-only features
(history expansion, programmable completion, real job control,
restricted shell) are **deferred** until the script-correctness bands
are done.

Status: **45 passed, 29 failed, 13 skipped** as of 2026-06-07. Baseline
(April) was 4/83. The parser-wording sweep below is complete, but the
broader parser/arith/funsub Batch B tracked in `docs/bash-gap-analysis.md`
is not complete.

## Currently PASSING

```
dynvar  extglob3  ifs  invert  lastpipe  mapfile  nquote1
nquote5  posix2  strip  tilde
```

## Bands, ordered

### P0 — Drop-in script correctness (work first)

Every real-world bash script touches at least one of these. Each
test below is 1–2 fixes away from passing.

| Test | Diff | Blocker |
|---|---|---|
| `case` | 13 | (a) arithmetic-error in pattern aborts the case; (b) backslash in case patterns matches the next-char literally — needs quote-removal in subject and escape-preserving in pattern |
| `iquote` | 7 | CTLNUL byte propagation in word-splitting |
| `attr` | 14 | `readonly 'a=(v)'` string-form → infer indexed array (bash 5.3 quirk) |
| `posixpat` | 33 | `[[:alpha:]]`/`[[:digit:]]` POSIX bracket classes in case patterns |
| `set-e` | 55 | `set -e` edge cases (errexit propagation through pipelines, command-substitutions) |
| `comsub` | 59 | `return` in `$(...)` aborts the comsub but not the script; case-clause edge cases |
| `cond` | 195 | `${H*}` pattern of var names inside `[[ ]]`; `-ef`, `-nt`, `-ot` mtime tests |
| `arith` | 372 | Number-base literals (`16#FF`, `2#1010`), arithmetic-expr edge cases |
| `arith-for` | 99 | xtrace + arithmetic-for semantics |
| `appendop` | 38 | Array arithmetic with `((arr[i]+=N))`, mixed `+=` cases |
| `varenv` | 480 | local/export scope rules, function-arg-passing |
| `errors` | huge | `select $1` parser must accept non-literal var; error-wording sweep |

**Expected gain: 11 → ~18**

### P1 — Parser & expansion correctness

Pure expansion/parser holes that real scripts hit but less often than P0.

| Test | Diff | Blocker |
|---|---|---|
| `tilde2` | 23 | Tilde in heredocs (should not expand), `${var:-~}` quote rules |
| `nquote3` | 32 | argv printing of CTLESC sequences (related to iquote) |
| `nquote4` | 38 | similar argv printing |
| `nquote2` | 56 | similar |
| `parser` | 32 | bash-wording on parser errors (extend the eval-time rewriter to top level) |
| `braces` | 34 | brace-expansion edge cases: `{a..b..c}` step, zero-padding mixed |
| `comsub-eof` | 35 | `$(...)` recovery after heredoc-EOF warning |
| `quote` | 128 | `printf %q` edge cases for special chars |
| `quotearray` | 156 | array printing with quoting |
| `more-exp` | 218 | `${var/pattern/replacement}` with arrays and extended forms |
| `new-exp` | 814 | bash 5.3 new expansion forms (mass) |
| `rhs-exp` | 64 | assignment-RHS quote-stripping semantics |
| `nameref` | 1033 | `declare -n` indirect references |
| `read` | 133 | `read` builtin remaining options |
| `printf` | %n | `printf %n` directive support |

**Expected gain: ~18 → ~28**

### P2 — Feature completeness

| Test | Diff | Blocker |
|---|---|---|
| `dbg-support2` | 14 | `shopt -s extdebug` + LINENO/BASH_ARGV stack |
| `dbg-support` | ? | DEBUG trap semantics |
| `cprint` | 64 | `declare -f` multi-line printing format |
| `func` | 390 | function display + edge cases |
| `casemod` | 59 | `${var^^}`, `${var,,}` edge cases |
| `set-x` | 64 | arithmetic-for-loop xtrace lines |
| `intl` | 81 | `$"..."` locale strings |
| `getopts` | 121 | getopts remaining edge cases |
| `vredir` | 131 | named-FD `{var}>file` redirect semantics |
| `glob-bracket` | 107 | bracket-glob edge cases |
| `extglob` | 194 | extended-glob `?(pat)` `*(pat)` etc. |
| `extglob2` | 65 | similar |
| `globstar` | 468 | `**` recursive glob |
| `trap` | 195 | trap DEBUG/ERR/RETURN scope and ordering |
| `heredoc` | 171 | heredoc edge cases |
| `herestr` | 42 | here-string edge cases |
| `redir` | 187 | misc redirection forms |
| `posixexp2` | 43 | POSIX-mode expansion differences |
| `posixpipe` | 48 | POSIX pipeline behavior |
| `exportfunc` | 30 | `BASH_FUNC_X%%=...` env-var function import |

**Expected gain: ~28 → ~50**

### P3 — DEFERRED (per agentic-only directive)

These are interactive-shell features. Agentic tools don't use them.
Implement only after P0–P2 saturate.

| Test | Diff | Reason deferred |
|---|---|---|
| `histexp` | 1 binary | `!!`, `!$`, `^old^new^` — history expansion |
| `complete` | huge | programmable completion engine |
| `coproc` | TIME | real PTY + process groups |
| `jobs` | TIME | real PTY + tcsetpgrp + SIGTSTP |
| `rsh` | 43 | restricted shell mode |
| `alias` | 88 | alias-expansion-in-comsub specifics |
| `invocation` | ? | `-l`/`-i`/`--rcfile` startup-flag matrix |

## Execution phases

Each phase is one focused session.

**Phase A** — P0 quick wins:
1. `case` (arithmetic-error abort + backslash patterns) — DONE
2. `iquote` (CTLNUL through word-splitting) — DONE
3. `attr` (array-from-string for `-a` flag) — DONE
4. `set-e` (errexit edge cases) — DONE
5. `comsub-eof` (`$(...)` recovery)

**Phase B** — parser-wording sweep:
6. Generalise the eval-time bash-wording rewriter to ALL parser
   errors (not just eval). Unlocks `parser`, helps `comsub`,
   `exportfunc`, `posixpat`. — DONE for the narrow wording target
   (`parser`, `exportfunc`, and `posixpat` pass). This does not cover the
   broader arith/funsub/heredoc/conditional parser Batch B.

**Phase C** — expansion family:
7. CTLESC propagation → `nquote2/3/4`, helps `quote`/`quotearray`.
8. Tilde in heredocs → `tilde2`.
9. Brace expansion edge cases → `braces`.

**Phase D** — declare/printf format:
10. `declare -f` multi-line format → `cprint`, helps `func`/`comsub2`.
11. `printf %n` and `%q` polish.
12. `extdebug` shopt + LINENO/BASH_ARGV stack.

**Phase E** — long tail:
13. Glob features: `extglob`, `globstar`.
14. Heredoc/redirect edge cases.
15. POSIX-mode behavioural differences.

**Phase F (DEFERRED)** — interactive features.
