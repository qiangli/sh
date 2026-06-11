quotearray residual blockers
============================

Current verified measurements in this sandbox:

- 2026-06-11: rebuilt this checkout's `bin/bashy` with
  `GOCACHE=$PWD/.cache/go-build go build -o bin/bashy ./cmd/bashy`.
- Gate fixtures, with `THIS_SH=$PWD/bin/bashy` anchored at the repo root before
  `cd external/bash-5.3/tests`:
  - `errors`: 70 diff lines.
  - `redir`: 0 diff lines; runner emits only its warning banner.
  - `history`: 0 diff lines; runner emits only its warning banner.
  - `quotearray`: 22 diff lines.

Implemented progress in this round:

- `declare A[$k]=X` and string-form `declare "A[$k]=X"` now reject the
  post-expansion malformed `]` key as `A[]]=X` while accepting `*` and `@` as
  ordinary associative keys.
- Associative assignment keys from simple parameter expansions are expanded
  once, so values like `$(echo foo)` and `$key` stay literal keys instead of
  being evaluated a second time.
- `let assoc[$key]++` now reports the malformed associative subscript in the
  bash-shaped `let:` diagnostic instead of mutating `assoc`.
- Namerefs to array targets such as `assoc[@]` and `array[@]` now expand to the
  referenced array values for `$nref`, fixing the missing `star bang at` and
  `1 2 3` output.
- `unset array[@]` on a populated indexed array now leaves `declare -a
  array=()`; a later unset of the already-empty array removes it.

Remaining live `quotearray` clusters:

- `quotearray2.sub` still misses one `assoc_expand_once` round-trip:
  `test -v assoc["$key"]` leaves `assoc` empty where bash preserves
  `["$var"]="value"`.
- `quotearray3.sub` still differs for `unset` with command-substitution-shaped
  associative keys. The hard part is preserving the distinction between quoted
  whole operands (`unset 'a[$key]'`, which bash treats as a string operand and
  can evaluate the subscript) and unquoted raw operands (`unset a[$key]`, which
  should avoid the split-into-two-invalid-identifiers behavior without breaking
  normal `unset A[$key]` for special keys like `@`).
- `quotearray5.sub` string-token unset cases still leave populated
  `$(echo foo)`, `$key`, and `foo` keys where bash prints `declare -A a=()`.
- Compound associative assignment ordering/semantics for
  `declare -A a=(@ v0 . v1)` still retains the `@` key in two cases where bash
  only prints `[.]="v1"`.

Verification notes:

- `GOCACHE=$PWD/.cache/go-build go build ./...` passes. Go still prints a
  sandbox warning when it cannot write the global module stat cache.
- `GOCACHE=$PWD/.cache/go-build go test ./expand/...` passes.
- Focused `GOCACHE=$PWD/.cache/go-build go test ./interp -run
  'TestRunnerRun|TestBashCompat|TestBash'` passes.
- Full `GOCACHE=$PWD/.cache/go-build go test ./interp/... ./expand/...` is
  blocked by this sandbox denying `/bin/ps` in `TestSetsidNewSession` and
  `TestNohupChildIsInNewSession`; no other failures were observed before that
  package failed.
