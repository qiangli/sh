# QA Report: Round-10 Verification

## Summary
Round-10 verification completed successfully. All four gate metrics meet or exceed expectations.

## Fixture Measurements

| Fixture | Expected | Measured | Status |
|---------|----------|----------|--------|
| errors | <= 77 | 71 | PASS |
| redir | 0 | 0 | PASS |
| history | 0 | 0 | PASS |
| quotearray | < 61 | 48 | PASS |

### Comparison to Pre-Merge Baseline
- errors: 159 -> 71 (expected: 71) - Q1, Q2a, Q2b, E4, E1 merged successfully
- redir: 0 -> 0 (unchanged)
- history: 0 -> 0 (unchanged)
- quotearray: 61 -> 48 (expected: 48) - Q1, Q2a, Q2b merged successfully

## Regression Tests Added

Added 7 new test cases to `interp/interp_test.go` covering quotearray2-5.sub semantics:

1. **Arithmetic with `[` key** - Tests that associative arrays can use `[` as a key in arithmetic expansion
2. **Arithmetic with `]` key** - Tests that associative arrays can use `]` as a key in arithmetic expansion
3. **test -v with `@` key** - Tests `test -v A[$key]` where key is `@` (special character handling)
4. **[[ -v ]] with `@` key** - Tests `[[ -v A[$key] ]]` where key is `@`
5. **unset -v with `@` key** - Tests `unset -v A[@]` only removes the `@` element, not entire array
6. **unset -v with `*` key** - Tests `unset -v A[*]` only removes the `*` element, not entire array
7. **unset with variable key** - Tests `unset A[$key]` where key is a variable containing `@`

All tests verify the merged behaviors from:
- Q1: quotearray arith framing
- Q2a: declare/unset assoc keys
- Q2b: test -v/read/printf -v

## Test Results

```
$ go test ./interp/... ./expand/...
ok      mvdan.cc/sh/v3/interp   1.566s
ok      mvdan.cc/sh/v3/expand   0.318s
```

All tests pass successfully.

## Anomalies

No anomalies detected. All measurements match expected values exactly.

## Conclusion

Round-10 verification PASSED. All gate requirements satisfied:
- errors <= 77: 71 PASS
- redir == 0: 0 PASS
- history == 0: 0 PASS
- quotearray < 61: 48 PASS
