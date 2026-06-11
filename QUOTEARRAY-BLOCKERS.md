quotearray residual blockers
============================

Current verified measurements in this sandbox:

- After the builtin/expand changes in this round, the exact requested repro
  with this sandbox's rebuilt `bin/bashy` is `112` diff lines.
- Exact requested repro command: `155` diff lines. Note: `external` is a
  symlink here, so `realpath ../../../bin/bashy` resolves to a different
  checkout's `bin/bashy`, not this sandbox's rebuilt binary.
- Same fixture with this sandbox's rebuilt `bin/bashy`: `173` diff lines using
  `diff - ./quotearray.right | wc -l`.

Implemented progress:

- Whole double-quoted arithmetic text such as `"assoc[$key]++"` is reparsed
  without prematurely expanding `$key`, so associative-array subscript
  resolution increments the expanded key exactly once.
- Single-quoted arithmetic text with an expanded malformed associative
  subscript now reports bash's operand-error shape for the fixture's
  `x],b[$(echo uname >&2)` key instead of mutating the array.
- `[[ ... -eq ... ]]` and related arithmetic comparisons now prefer source
  operand text in bash mode, deferring array-subscript evaluation to the
  arithmetic evaluator.
- Double-quoted arithmetic text only takes the raw-text path when it contains
  a parameter expansion, preserving normal quote removal for cases like
  `let "a[\" \"]=11"`.
- Malformed arithmetic subscript diagnostics now carry the expanded token text
  through `expand.ArithmError.Text`, and indexed-array malformed subscripts
  quote bracket characters in that text, reducing the sandbox-local
  `quotearray` diff from `176` to `173`.
- `unset` now treats `@` and `*` as ordinary associative-array keys while
  preserving whole-array behavior for indexed arrays, and leaves an explicitly
  empty associative array after deleting its last element.
- `read` and `printf -v` now reject malformed builtin array destinations such
  as `A[]]`, while `printf -v array[@]` rejects indexed whole-array
  destinations as a bad subscript.
- Indirect expansion of associative `[@]`/`[*]` now uses bash 5.3 associative
  iteration order instead of sorted values.

Remaining clusters:

- Arithmetic error framing for indirect expressions like `$expr`/`expr` and
  malformed indexed-array subscripts such as `array[$index]++` still prints
  bashy's `((: <expanded-token> :` wrapper in command-arithmetic paths where
  bash 5.3 reports the expanded token directly. The remaining wrapper is added
  by `interp.Runner.bashArithmError`; `expand/arith.go` now supplies the right
  expanded override text, including backslash-escaped indexed-array tokens.
- Remaining `assoc_expand_once` builtin cases in `quotearray2.sub` through
  `quotearray5.sub` are now mostly outside this round's writable files:
  `declare A[$k]=X`, quoted string-form `declare "A[$k]=X"`, compound
  associative literals like `declare -A a=(@ v0 . v1)`, and classic `test -v`
  word-splitting diagnostics are handled in `interp/runner.go`/`interp/vars.go`
  paths.

Verification notes:

- `go build ./...` passes with `GOCACHE=$PWD/.cache/go-build`.
- `go test ./expand/... ./interp/...` is blocked by this sandbox denying
  `/bin/ps` in `TestSetsidNewSession` and `TestNohupChildIsInNewSession`.
  Focused `go test ./interp -run 'TestRunnerRun|TestRunnerRunConfirm|TestBash'`
  passes.
