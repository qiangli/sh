quotearray residual blockers
============================

Current verified measurements in this sandbox:

- Exact requested repro command: `155` diff lines. Note: `external` is a
  symlink here, so `realpath ../../../bin/bashy` resolves to a different
  checkout's `bin/bashy`, not this sandbox's rebuilt binary.
- Same fixture with this sandbox's rebuilt `bin/bashy`: `199` diff lines using
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

Remaining clusters:

- Arithmetic error framing for indirect expressions like `$expr`/`expr` still
  prints bashy's `((: <expr> :` wrapper in several cases where bash reports the
  expanded token directly. Synthetic reparse positions are no longer mapped
  against unrelated source text, but the message shape still differs.
- Indexed-array malformed subscripts such as `array[$index]++` now classify as
  arithmetic syntax errors, but the diagnostic still differs from bash's
  backslash-escaped token text.
- Several `assoc_expand_once` builtin cases in `quotearray2.sub` through
  `quotearray5.sub` remain outside the arithmetic path, mainly `read`,
  `printf -v`, `declare`, `unset`, and `test -v` handling of quoted or special
  associative keys.

Verification notes:

- `go build ./...` passes with `GOCACHE=$PWD/.cache/go-build`.
- `go test ./expand/... ./interp/...` is blocked by this sandbox denying
  `/bin/ps` in `TestSetsidNewSession` and `TestNohupChildIsInNewSession`.
  Focused `go test ./interp -run 'TestRunnerRun|TestRunnerRunConfirm|TestBash'`
  passes.
