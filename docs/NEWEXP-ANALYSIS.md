# new-exp Fixture Cluster Analysis

Analysis of the `new-exp` fixture from the `bash-5.3` test corpus, comparing `bashy` actual output against `new-exp.right` (with `^expect` lines stripped).

## Candidate Issues

### 1. Ambiguous Redirect in `$(< glob)` expansion
- **Evidence:** `new-exp2.sub: line 37: $TMPDIR/bashtmp.x*: ambiguous redirect` followed by `whoops: $(< filename) with glob expansion failed`.
- **Suspected Region:** `interp/runner.go` or `syntax/parser.go` specialization for `$(< file)`. It appears `bashy` fails to perform glob expansion (or incorrectly performs it) when used within the optimized file-read command substitution.
- **Fibonacci Estimate:** 5

### 2. Anchored Substitution `${var/#/replacement}` Failure
- **Evidence:** 
    - `${var/#/--}` where `var=blah` produces `blah` instead of `--blah`.
    - `${var/#/x}` where `var=abc` produces `abc` instead of `xabc`.
    - `${var/#/x}` where `var=` produces empty instead of `x`.
- **Suspected Region:** `expand/param.go`. The anchored start substitution (`/#`) seems to be ignored or failing to match an empty prefix at the start of the string.
- **Fibonacci Estimate:** 3

### 3. Global Anchored Substitution `//#pattern` Misinterpretation
- **Evidence:** `${var[*]//#abc/foo}` where `var=(abcde ...)` produces `foode ...` instead of `abcde ...`.
- **Suspected Region:** `expand/param.go`. `bashy` seems to interpret `//#` as a request for global anchored substitution (treating the first `/` as global and the second `/` as separator, with `#` anchoring), but bash 5.3 treats the first `/` as global and the pattern as `#abc` (not anchored, since `#` is not the first character after the separator).
- **Fibonacci Estimate:** 3

### 4. Missing Validation for Negative Substring Expressions
- **Evidence:** Expected error messages like `substring expression < 0` for `${@:1:$(($# - 2))}` (when `$#` is 1) are missing. `bashy` instead produces partial results or empty output without erroring.
- **Suspected Region:** `expand/param.go` in substring expansion logic.
- **Fibonacci Estimate:** 5

### 5. Lenient Parser for Command Substitution with Numeric Prefix
- **Evidence:** `$(1111111111111111111111</dev/stdin)` prints `hi` (the heredoc content) instead of failing with `command not found`.
- **Suspected Region:** `syntax/parser.go` or `interp/runner.go`. The parser might be misidentifying the numeric prefix as a file descriptor or ignoring it, leading to incorrect execution of the redirection as if it were `$(< /dev/stdin)`.
- **Fibonacci Estimate:** 8

### 6. Positional Parameter Array Expansion with Empty Arguments
- **Evidence:** Differences in the number of `argv[1] = <>` entries in the output, specifically when expanding positional parameters in various contexts (quoted vs unquoted).
- **Suspected Region:** `expand/expand.go` or `expand/param.go` handling of `@` and `*` expansion.
- **Fibonacci Estimate:** 5
