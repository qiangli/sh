# `[[ ]]` fidelity — blocked cases

Scope of this task allowed edits only to `interp/test.go`, `interp/test_classic.go`,
and the new `interp/dbracket_fidelity_test.go`. Of the 13 cases, **2 were fixed**
in-scope (`043`, `044` — tilde expansion of `[[ ]]` operands, including empty
`$HOME` and the literal treatment of a tilde-expanded `=~` operand).

The remaining 11 require changes to files outside the allowed scope
(`syntax/parser.go`, `interp/runner.go`, `pattern/pattern.go`/`expand/expand.go`,
or the out-of-scope test table `interp/interp_test.go`). Each is documented below
with diagnosis and a verified/proposed patch.

---

## 005 — `=~` invalid-regex diagnostic wording

Script: `[[ foo.py =~ * ]] && echo true`
Bash: `… [[: invalid regular expression `*': Repetition not preceded by valid expression`
Ours: `[[: error parsing regexp: missing argument to repetition operator: `*``

**Diagnosis.** `binTest` (`interp/test.go`, `case syntax.TsReMatch`) prints the raw
Go `regexp.Compile` error. Bash uses POSIX `regcomp` wording. The fix itself is
fully in-scope and **verified** (output matched bash 5.3 exactly via `gosh`), BUT it
also changes the wording for the existing assertion in `interp/interp_test.go:2622`
(`[[ a =~ [ ]]`), which encodes the *old* Go wording and is **out of scope**.
Changing only `test.go` makes the regression-guard `TestRunnerRun` fail, so this is
blocked on a coordinated update to `interp_test.go`.

**Verified patch (test.go):**
```go
// imports: add  "errors"  and  resyntax "regexp/syntax"

// in binTest, case syntax.TsReMatch:
re, err := regexp.Compile(pat)
if err != nil {
    r.errf("[[: %s\n", bashRegexErrMsg(err, y))   // was: r.errf("[[: %s\n", err)
    r.exit.code = 2
    return false
}

func bashRegexErrMsg(err error, pat string) string {
    reason := err.Error()
    var rerr *resyntax.Error
    if errors.As(err, &rerr) {
        switch rerr.Code {
        case resyntax.ErrMissingRepeatArgument, resyntax.ErrInvalidRepeatOp:
            reason = "Repetition not preceded by valid expression"
        case resyntax.ErrMissingBracket:
            reason = "Unmatched [, [^, [:, [., or [="
        case resyntax.ErrMissingParen:
            reason = "Unmatched ( or \\("
        case resyntax.ErrUnexpectedParen:
            reason = "Unmatched ) or \\)"
        case resyntax.ErrTrailingBackslash:
            reason = "Trailing backslash"
        case resyntax.ErrInvalidCharRange:
            reason = "Invalid range end"
        case resyntax.ErrInvalidRepeatSize:
            reason = "Invalid content of \\{\\}"
        case resyntax.ErrInvalidCharClass:
            reason = "Invalid character class"
        case resyntax.ErrInvalidEscape:
            reason = "Invalid back reference"
        }
    }
    return "invalid regular expression `" + pat + "': " + reason
}
```
**Required out-of-scope companion change (interp/interp_test.go:2622):**
```go
// was: "[[: error parsing regexp: missing closing ]: `[`\nexit status 2 #JUSTERR"
"[[: invalid regular expression `[': Unmatched [, [^, [:, [., or [=\nexit status 2 #JUSTERR",
```

---

## 020, 025, 033, 035, 037, 039, 040, 041 — `[[ ]]` parse-error diagnostics

These are all **parse errors** produced by the `syntax` package while building the
`TestClause` AST; the `[[ ]]` never reaches `interp`. Bash detects them at parse time
and prints its three-line `unexpected token … / syntax error near … / <source line>`
diagnostic. Our `syntax/parser.go` emits its own one-line `N:M: …` errors.

| case | script | bash diagnostic (first line) | our error |
|------|--------|------------------------------|-----------|
| 020 | `op='==' ; [[ a $op a ]]` | ``unexpected token `$op', conditional binary operator expected`` | `not a valid test operator: `$`` |
| 025 | `[[ -f < ]]` | ``unexpected argument `<' to conditional unary operator`` | `` `-f` must be followed by a word`` |
| 033 | `[[ '(' foo ]]` | ``unexpected token `foo', conditional binary operator expected`` | `not a valid test operator: `foo`` |
| 035 | `[[ -z ]]` | ``unexpected argument `]]' to conditional unary operator`` | `not a valid test operator: `echo`` (mis-recovers past `]]`) |
| 037 | `[[ -z '>' -- ]]` | ``syntax error in conditional expression: unexpected token `--'`` | `not a valid test operator: `--`` |
| 039 | `[[ ]]` | ``syntax error near `]]'`` | `` `[[` must be followed by an expression`` |
| 040 | `[[ && ]]` | ``unexpected token `&&' in conditional command`` | `` `[[` must be followed by an expression`` |
| 041 | `[[ a 3< b ]]` | ``unexpected token `3', conditional binary operator expected`` | `not a valid test operator: LitRedir` |

**Diagnosis / why blocked.** Fixing these requires:
1. `syntax/parser.go` (`parseTestClause` / `testExpr` / `followWordTest`) to recognise
   these positions and emit bash-shaped diagnostics, and to stop parsing where bash
   does (e.g. 035/039 must not consume the closing `]]` / next line).
2. The bash three-line format (`… line N: <msg>` / `… line N: syntax error near `T'`
   / `… line N: `<source>'`) is added by the drop-in CLI's error reporter
   (`bashy`), not by this engine, so full fidelity also needs work there.

None of this is reachable from `interp/test.go` or `interp/test_classic.go`
(`test_classic.go`'s `testParser` only drives the runtime `test`/`[` builtin, not the
`[[ ]]` conditional command). **Out of scope.**

---

## 028 — `(( ))` arithmetic error on array operand

Script: `a=('1 3' 5); (( a == b ))`
Bash: `… ((: 1 3: arithmetic syntax error in expression (error token is "3")` then `status=1`
Ours: `1:5: not a valid arithmetic operator: `3`` then `status=1`

**Diagnosis.** This is the `(( ))` arithmetic *command* (`*syntax.ArithmCmd`), handled
at `interp/runner.go:5442` via `r.arithm(cm.X)`. The array `a` expands to element 0
`1 3`, which fails to parse as arithmetic. The exit status already matches (1); only
the error wording differs. Bash's wording (`((: <expr>: arithmetic syntax error in
expression (error token is "T")`) must be produced in the `r.arithm` error path in
`runner.go` (cf. the existing `arithErrMsg` helper in `test.go`, which handles the
`[[ ]]` arithmetic-comparison path but not the `(( ))` command path).

**Out of scope** (needs `interp/runner.go`).

---

## 047 — extglob: quoted `()` must be literal in `==` patterns

Script (relevant line): `[[ 'foo()' == *'()' ]]` → bash prints `match2` (with and
without `shopt -s extglob`); we print nothing.

**Diagnosis.** Two coupled problems:

1. `interp/runner.go:7006 match()` always compiles patterns with
   `pattern.ExtendedOperators`, even when `extglob` is off. With extglob off,
   `*()` should be `*` + literal `()` and match `foo()`; our matcher treats `*(`
   as the extglob "zero-or-more" operator and fails.
2. `expand.Pattern` quotes a quoted sub-part via `pattern.QuoteMeta(val, 0)`, and
   `pattern.QuoteMeta` (`pattern/pattern.go:731`) only escapes `* ? [ \` — never the
   extglob trigger characters `( ) + @ !`. So the quoted `()` in `*'()'` reaches the
   matcher unescaped as `*()`, indistinguishable from an unquoted extglob operator.
   Result: with extglob *on*, the quoted parens are wrongly treated as an operator.

Bash keeps quoted/escaped parens literal regardless of `extglob`. The correct fix is
to make quoted sub-parts escape the extglob metacharacters so they survive the
`ExtendedOperators` matcher:

**Proposed patch (pattern/pattern.go — QuoteMeta):**
```go
func QuoteMeta(pat string, mode Mode) string {
    isMeta := func(r rune) bool {
        switch r {
        case '*', '?', '[', '\\':
            return true
        }
        if mode&ExtendedOperators != 0 {
            switch r {
            case '(', ')', '|', '+', '@', '!':
                return true
            }
        }
        return false
    }
    // …escape every rune for which isMeta(r) is true…
}
```
…and have `expand.Pattern` pass `pattern.ExtendedOperators` (the mode the consumer
`match()` uses) when quoting quoted sub-parts. This is **out of scope** (needs
`pattern/pattern.go` + `expand/expand.go`).

Note: the existing `bashStrmatchBracketCases` table in `test.go` shows the team
already special-cases tricky bracket/extglob matches there; a narrower in-`test.go`
fix would require reimplementing quote-aware pattern construction for the `==`/`!=`
operator (duplicating `expand.Pattern`), which is fragile and was judged riskier than
the pattern-package fix above.
