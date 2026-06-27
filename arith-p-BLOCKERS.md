# arith-p.tst — POSIX arithmetic expansion conformance

Base: bf858baf. Harness: `printf '%s' "<script>" | /tmp/gosh --posix`, compared
byte-for-byte against GNU bash 5.3.

## Status: all 41 cases pass

All 41 `arith-p.tst` cases match bash 5.3 byte-for-byte:
- single constant / variable / unset variable: 3 cases
- unary sign / negation operators: 2 cases
- multiplicative / additive / shift / bitwise operators: 4 cases
- relational / equality / logical / ternary operators: 10 cases
- conditional evaluation (&& / || / ?: short-circuit): 3 cases
- assignment operators (simple and compound): 2 cases
- operator precedence (16 sub-tests covering the full precedence table): 16 cases
- parentheses, parameter expansion, command substitution in arithmetic: 3 cases
- assignment in parameter expansion in arithmetic: 1 case

No interp/expand changes were needed.

## Deferred

### `((...))` arithmetic command (without `$`) fails in POSIX mode

```
$ printf '%s' '((1+1)); echo $?' | /tmp/gosh --posix
bash: line 1: 1+1: command not found
127
```

Expected (bash 5.3 --posix):

```
$ printf '%s' '((1+1)); echo $?' | bash --posix
0
```

**Root cause — parser configuration in cmd/gosh/main.go.** When `--posix` is
passed, gosh sets `syntax.Variant(syntax.LangPOSIX)` on the parser. The
`LangPOSIX` variant is the strict POSIX grammar that does not recognize `((…))`
as an arithmetic command — only `langBashLike (LangBash|LangBats) |
LangMirBSDKorn | LangZsh` support the `dblLeftParen` token. Bash `--posix`
instead uses `LangBash + PosixMode(true)`, which keeps bash extensions while
adding POSIX restrictions.

`$((…))` arithmetic expansion (with `$`) is unaffected — it uses the `dollDblParen`
token path, which has no language restriction.

**Deferred because:** the fix belongs in `cmd/gosh/main.go` (parser option:
`syntax.PosixMode(true)` with `LangBash` instead of `Variant(LangPOSIX)`) or in
`syntax/lexer.go`, both outside the `interp/`/`expand/` scope of this task.
The `((…))` command is not exercised by the `arith-p.tst` yash suite.

## Go test suite

`env -i PATH="$PATH" go test ./interp/ ./expand/ -skip 'TestParseConfirm|TestRunnerRunConfirm'` — all pass.
