# Array Fixture Cluster Analysis

This document clusters the failures observed in the `array.tests` fixture from the Bash 5.3 test suite when run against `bashy`.

## Candidate Issues

### 1. Compound Assignment Syntax and Metacharacter Parsing
*   **Description:** `bashy`'s parser is stricter than Bash 5.3 regarding unquoted metacharacters within compound array assignments. It incorrectly identifies tokens like `&`, `<`, `>`, and `[` as syntax errors or redirection/subscript operators even when they should be treated as literal word elements in this context.
*   **Evidence:**
    ```
    ./array.tests: line 34: syntax error near unexpected token `&'
    ./array.tests: line 245: syntax error near unexpected token `[' (for `array2=(grep [ 123 ] \*)`)
    ./array.tests: line 321: syntax error near unexpected token `<' (for `metas=( <> < > ! )`)
    ```
*   **Suspected Region:** `syntax/parser.go` (parsing logic for `ArrayExpr` and word elements in compound assignments).
*   **Fibonacci Estimate:** 8

### 2. Missing Test Suite Helpers (`recho`, `zecho`)
*   **Description:** Many tests in the Bash suite rely on small external C programs like `recho` and `zecho` to verify how arguments are split and expanded. These are often not found in the `PATH` during `bashy` execution, or `bashy`'s `ExecHandler` fails to invoke them correctly, leading to missing output and "command not found" errors.
*   **Evidence:**
    ```
    ./array.tests: line 205: recho: command not found
    ./array.tests: line 212: zecho: command not found
    ```
*   **Suspected Region:** `cmd/bashy/main.go` (environment/PATH setup) and `interp/runner.go`.
*   **Fibonacci Estimate:** 2

### 3. Subscript Arithmetic and Identifier Validation
*   **Description:** `bashy` struggles with complex arithmetic expressions or variable identifiers used as subscripts, particularly in `unset` commands or assignments. It also reports "bad array subscript" in cases where Bash provides more specific arithmetic errors, or vice-versa.
*   **Evidence:**
    ```
    ./array.tests: line 232: unset: [nelem-1]: bad array subscript
    ./array.tests: line 125: ((: * : 1:3: `*` must follow an expression
    ```
*   **Suspected Region:** `interp/vars.go` (subscript evaluation) and `expand/arith.go`.
*   **Fibonacci Estimate:** 5

### 4. Array Slicing and Offset Logic Discrepancies
*   **Description:** The implementation of array slicing (`${array[@]:offset:length}`) in `bashy` diverges from Bash 5.3 behavior when dealing with sparse arrays, null elements, or negative offsets. It often selects the wrong starting element or fails to skip unset indices correctly.
*   **Evidence:**
    ```
    -one
    +three (in "include null element -- expect one")
    -seven
    +five seven (in "negative offset to unset element")
    ```
*   **Suspected Region:** `interp/vars.go` (slicing logic in `Indexed` and `Associative` types).
*   **Fibonacci Estimate:** 8

### 5. `declare -p` and `readonly` Display Format
*   **Description:** `bashy`'s output for `declare -p` and `readonly -a` contains discrepancies in variable presence (e.g., printing internal `BASH_ARGC` etc.), attribute naming (e.g., `declare -ar` vs `readonly -a`), and the representation of empty vs unset indices.
*   **Evidence:**
    ```
    +declare -a BASH_ARGC=()
    -readonly -a a=([1]="" [2]="bdef" ...)
    +declare -ar a=([1]="" [2]="bdef" ...)
    ```
*   **Suspected Region:** `interp/builtin.go` (`declare` and `readonly` implementation) and `interp/vars.go` (variable printing).
*   **Fibonacci Estimate:** 3

### 6. Builtin Behavioral Differences (`read -a`, `unset`)
*   **Description:** The `read -a` builtin in `bashy` sometimes leaks its prompt to standard output or fails to populate the array correctly. Similarly, `unset` on array elements or the array itself sometimes produces different side effects or error messages than expected.
*   **Evidence:**
    ```
    +array test: this of (prompt leaked to output stream)
    ./array.tests: line 147: declare: c: cannot destroy array variables in this way
    ```
*   **Suspected Region:** `interp/builtin.go` (handlers for `read`, `unset`, and `declare`).
*   **Fibonacci Estimate:** 5

### 7. Sparse Array Initialization and Expansion Errors
*   **Description:** `bashy` fails to correctly expand and assign elements when a compound assignment contains a quoted array expansion (e.g., `f=("${d[@]}")`), leading to literal strings being stored instead of the expanded elements, or incorrect index mapping in sparse arrays.
*   **Evidence:**
    ```
    -declare -a f=([0]="" [1]="bdef" [2]="hello world" ...)
    +declare -a f=([0]="\"\${d[@]}\"")
    ```
*   **Suspected Region:** `interp/runner.go` (compound assignment execution) and `interp/vars.go`.
*   **Fibonacci Estimate:** 8

### 8. Handling of Nested Array Assignments
*   **Description:** Bash 5.3 and `bashy` have different error reporting and behavior when attempting to nest array assignments (e.g., `d[7]=(abdedfegeee)`), which is generally unsupported but produces different diagnostics.
*   **Evidence:**
    ```
    -./array.tests: line 131: d[7]: cannot assign list to array member
    +./array.tests: line 131: arrays cannot be nested
    ```
*   **Suspected Region:** `interp/vars.go` (handling of `syntax.ArrayExpr` in element assignment).
*   **Fibonacci Estimate:** 3
