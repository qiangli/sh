# QA Report — Round 10 verification

Date: 2026-06-11. Verifies the round-10 merges: Q1 (quotearray arith error
framing), Q2a (declare/unset special assoc keys), Q2b (test -v + read/printf -v
assoc keys), plus E2 (posix-mode select errors).

## 1. Fixture re-measurement (fresh build)

Measured with `make build && make test-bash-helpers`, then per fixture:
`THIS_SH=$BASHY ... $BASHY ./<t>.tests 2>&1 | diff - ./<t>.right | wc -l`.

| Fixture    | Expected | Measured | Gate          | Status |
|------------|----------|----------|---------------|--------|
| errors     | 77       | **73**   | <= 77         | PASS   |
| redir      | 0        | **0**    | == 0          | PASS   |
| history    | 0        | **0**    | == 0          | PASS   |
| quotearray | 48       | **48**   | < 61          | PASS   |

All four gate conditions hold. **Anomaly (benign):** errors measures 73, four
lines *better* than the 77 the round reported — the E2 posix-mode select merge
(f348bb82) and the for/select line-attribution fix (e6c77963) landed after the
77 snapshot was taken. No fixture regressed.

## 2. Regression tests added

Nine cases added to `runTests` in `interp/interp_test.go` (after the existing
`test -v A["$key"]` case, ~line 939), covering the merged quotearray2-5.sub
semantics so a future regression fails in `go test ./interp` without the
fixture harness:

- **Arith assignment with special assoc keys** (quotearray2): `(( A[$k]=2 ))`
  for `]`, `*`, `@`, and `let "a[\" \"]=11"` with a quoted-space key,
  including bash-order `declare -p` output.
- **`read` into special assoc keys** (Q2b, quotearray2): tab, space, `*`, `@`
  succeed; `read 'A[]]'` is rejected with `not a valid identifier`, exit 2.
- **`printf -v` into special assoc keys** (Q2b, quotearray2): same accept set;
  `printf -v 'A[]]'` rejected, exit 1.
- **Classic `test -v` with `]` key** (Q2b): `A[']']=x; test -v 'A[]]'` → true.
- **Single-expansion `unset -v`** (Q2a, quotearray5): `unset -v a["$key"]` and
  `unset -v a['$key']` remove the literal key without re-expanding a
  command-substitution payload (`key='$(echo foo)'`).

Deliberately *not* locked in (still divergent from bash per the remaining 48
diff lines, tracked for future rounds): `declare A[$k]=X` / `declare
"A[$k]=X"` with `]`/`*`/`@` keys, string-form `unset 'a[$key]'`
re-expansion, `let` with unquoted comma subscripts, and nameref-to-assoc
display cases in quotearray3/4.

## 3. Test runs

- `go test ./interp/... ./expand/...` — **PASS** (all, including the 9 new
  cases). Run with the documented `PATH=/bin:/usr/bin:...` workaround for the
  local ycode `sh` shim.

## 4. Notes

- The interp error format for `printf -v` invalid identifiers uses double
  quotes (`printf: "A[]]": ...`) while `read` uses bash backtick style; bashy's
  error-format pass normalizes both for the fixture, so the interp-level tests
  assert the interp-native strings.
- The commented subshell-loop-redirect quirk block in `interp/runner.go` was
  not touched.
