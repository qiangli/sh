# Arith Fixture Cluster Analysis

This document clusters the failures observed in the `arith.tests` fixture from the Bash 5.3 test suite when run against `bashy`.

## Cluster 1: Literal `$` Variable Expansion in Arithmetic
*   **Description:** `bashy` fails to correctly handle or error upon literal `$` variable prefixes within arithmetic contexts when they are passed as strings (e.g., in `let` or single-quoted arithmetic expansions).
*   **Evidence:**
    ```
    > ./arith.tests: line 177: let: jv += $iv: arithmetic syntax error: operand expected (error token is "$iv")
    ```
    (Note: `bashy` fails to produce this expected error, indicating it likely treats `$iv` as 0 or fails to re-parse it correctly).
*   **Suspected Region:** `expand/arith.go`
*   **Fibonacci Estimate:** 3

## Cluster 2: Quoted Array Subscript Evaluation
*   **Description:** `bashy` produces internal-sounding error messages when encountering quoted or escaped subscripts in arithmetic assignments, and fails to correctly evaluate the resulting array state.
*   **Evidence:**
    ```
    < ./arith10.sub: line 33: ((: \" \" : 1:6: not a valid arithmetic operator: `\"` (error token is "0 ")
    < declare -a a=([0]="15")
    ---
    > ./arith10.sub: line 33: " ": arithmetic syntax error: operand expected (error token is "" "")
    > declare -a a=([0]="16")
    ```
*   **Suspected Region:** `interp/runner.go` and `expand/arith.go`
*   **Fibonacci Estimate:** 5

## Cluster 3: Silent Success on Empty Arithmetic Operands
*   **Description:** `bashy` incorrectly allows empty strings as operands in arithmetic expressions, treating them as 0 and returning a success exit code, whereas Bash 5.3+ expects a syntax error.
*   **Evidence:**
    ```
    < 4 0
    ---
    > ./arith10.sub: line 89: ((: 1 -  : arithmetic syntax error: operand expected (error token is "-  ")
    > 4 1
    ```
*   **Suspected Region:** `expand/arith.go`
*   **Fibonacci Estimate:** 3

## Cluster 4: Parser EOF on Arithmetic Syntax Errors
*   **Description:** The shell parser fails to synchronize and find the closing `))` when an arithmetic expression contains a syntax error (such as missing separators), leading to an "unexpected EOF" instead of a specific arithmetic error.
*   **Evidence:**
    ```
    < ./arith.tests: line 335: unexpected EOF while looking for matching `)'
    ---
    > ./arith.tests: line 335: ((: x=9 y=41 : arithmetic syntax error in expression (error token is "y=41 ")
    ```
*   **Suspected Region:** `syntax/parser.go`
*   **Fibonacci Estimate:** 5

## Cluster 5: Arithmetic Trace Expansion (set -x)
*   **Description:** When execution tracing is enabled, `bashy` shows the original expression with variables in the trace output for `(( ))` commands, while Bash expects the expanded/evaluated expression.
*   **Evidence:**
    ```
    274c275
    < + (( $var ))
    ---
    > + ((  42  ))
    ```
*   **Suspected Region:** `interp/runner.go`
*   **Fibonacci Estimate:** 2

## Cluster 6: Unbound Array Variable Error Format
*   **Description:** `bashy` includes the array subscript in the "unbound variable" error message, while Bash (and the fixture) expects only the base variable name to be reported.
*   **Evidence:**
    ```
    < ./arith9.sub: line 5: a[0]: unbound variable
    ---
    > ./arith9.sub: line 5: a: unbound variable
    ```
*   **Suspected Region:** `expand/arith.go` or `interp`
*   **Fibonacci Estimate:** 2
