# assoc Fixture Cluster Analysis

This document clusters the failures observed in the `assoc.tests` fixture and its sub-tests from the Bash 5.3 test suite when run against `bashy`.

## Candidate Issues

### 1. Parser Failure on Unquoted Associative Subscripts with Spaces
*   **Description:** The `bashy` parser (via `mvdan/sh`) incorrectly attempts to parse associative array subscripts as arithmetic expressions during assignment if they contain spaces and are not quoted. This leads to a fatal syntax error.
*   **Evidence:**
    ```
    ./assoc.tests:47:13: not a valid arithmetic operator: `world`
    ```
    (Note: Column 13 corresponds to the space/start of the second word in `chaff[hello world]=flip`).
*   **Suspected Region:** `syntax/parser.go` (specifically `getAssign` and `eitherIndex`) and `syntax/parser_arithm.go`.
*   **Fibonacci Estimate:** 5

### 2. Panic in `assignVal` due to Nil `ArithmExpr` Interface Conversion
*   **Description:** `bashy` panics when attempting to perform certain associative array assignments where an `ArithmExpr` is expected but is nil or cannot be converted to a `Word`.
*   **Evidence:**
    ```
    panic: interface conversion: syntax.ArithmExpr is nil, not *syntax.Word
    goroutine 1 [running]:
    mvdan.cc/sh/v3/interp.(*Runner).assignVal(0xe7bc6c32708, {0xe7bc6ba05e0, 0x6}, {0x1, 0x0, 0x0, 0x0, 0x0, 0x0, {0x0, ...}, ...}, ...)
        /Users/qiangli/projects/poc/ai/sh/interp/vars.go:659 +0xd30
    ```
    (Observed in `assoc11.sub`, `assoc12.sub`, `assoc14.sub`, and `assoc15.sub`).
*   **Suspected Region:** `interp/vars.go` (specifically the type assertion in `assignVal`).
*   **Fibonacci Estimate:** 3

### 3. Missing Support for Attribute Unsetting with `declare +`
*   **Description:** The `declare` and `typeset` builtins in `bashy` do not support the `+` prefix for flags (e.g., `declare +i`), which in Bash is used to remove an attribute from a variable.
*   **Evidence:**
    ```
    declare: invalid option "+i"
    ```
*   **Suspected Region:** `interp/builtin.go` (the `declare` builtin implementation).
*   **Fibonacci Estimate:** 2

### 4. Loss of Associative Attribute (`-A`) on Assignment or Re-declaration
*   **Description:** Variables declared as associative arrays sometimes lose their `-A` attribute when assigned values or when re-declared with other attributes (like `-i`).
*   **Evidence:**
    ```
    < declare -A fluff=([foo]="one" [bar]="two" )
    ---
    > declare -- fluff="assigned"
    ```
*   **Suspected Region:** `interp/vars.go` and the interaction between `declare` and the variable storage.
*   **Fibonacci Estimate:** 5

### 5. Missing `-p` Option in `hash` Builtin
*   **Description:** The `hash` builtin fails when passed the `-p` option, which is used in Bash to associate a name with a specific path.
*   **Evidence:**
    ```
    assoc1.sub: line 17: hash: -p: invalid option
    ```
*   **Suspected Region:** `interp/builtin.go` (the `hash` builtin implementation).
*   **Fibonacci Estimate:** 2

### 6. Arithmetic Parser Failure on Logical Not with Associative Subscript
*   **Description:** The arithmetic parser fails to correctly handle the logical NOT operator (`!`) when followed by an associative array reference.
*   **Evidence:**
    ```
    assoc13.sub:22:7: `!` must be followed by an expression
    ```
    (Corresponding to `(( !assoc[$idx] ))`).
*   **Suspected Region:** `syntax/parser_arithm.go`.
*   **Fibonacci Estimate:** 3

### 7. Missing `assoc_expand_once` Shopt and Logic
*   **Description:** `bashy` accepts but does not yet implement the `assoc_expand_once` shopt, which prevents double-expansion of associative array subscripts in certain contexts.
*   **Evidence:** Divergences in `assoc9.sub` where subscripts are incorrectly expanded multiple times.
*   **Suspected Region:** `interp/api.go` and `expand/param.go`.
*   **Fibonacci Estimate:** 5
