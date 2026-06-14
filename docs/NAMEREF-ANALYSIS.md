# Nameref Fixture Cluster Analysis

This document clusters the failures observed in the `nameref.tests` fixture from the Bash 5.3 test suite when run against `bashy`.

## Candidate Issues

### 1. Unsupported `+n` Attribute Removal
*   **Description:** `bashy` does not support the `+n` option in `declare` or `typeset` to remove the nameref attribute from a variable, reverting it to a normal variable.
*   **Evidence:**
    ```
    declare: invalid option "+n"
    ```
*   **Suspected Region:** `interp/builtin.go` (the `declare` and `typeset` implementation).
*   **Fibonacci Estimate:** 3

### 2. Panic on Empty Variable Name during Resolution
*   **Description:** The shell panics with "variable name must not be empty" when a nameref resolves to an empty string (or an invalid identifier that becomes empty during resolution) and is subsequently looked up in the environment.
*   **Evidence:**
    ```
    panic: variable name must not be empty
    goroutine 1 [running]:
    mvdan.cc/sh/v3/interp.(*Runner).lookupVar(..., {0x0, 0x0})
        /interp/vars.go:160
    ```
*   **Suspected Region:** `interp/vars.go` (`lookupVar` needs to safely handle empty inputs from nameref resolution) and `interp/runner.go` (`expandEnv.Get`).
*   **Fibonacci Estimate:** 5

### 3. Incompatible Flag Parsing for Combined Short Flags
*   **Description:** `bashy` fails to parse combined short flags like `-uc` (common in Bash tests), which results in the Go `flag` package reporting an error and the shell exiting before executing the test logic.
*   **Evidence:**
    ```
    flag provided but not defined: -uc
    Usage of .../bin/bashy:
      -c string
      ...
    ```
*   **Suspected Region:** `cmd/bashy/main.go` (`splitCombinedShortFlags` function is missing some bash-style flag combinations used by the test suite).
*   **Fibonacci Estimate:** 3

### 4. Incorrect Error Message Format and Missing Context
*   **Description:** `bashy` often omits the file name and line number prefix required by Bash 5.3 compatibility, and the specific wording of nameref-related errors (e.g., "readonly variable" vs "cannot unset: readonly variable") differs from the expected output.
*   **Evidence:**
    ```
    < foo: readonly variable
    ---
    > ./nameref.tests: line 106: foo: readonly variable
    ```
*   **Suspected Region:** `interp/runner.go` (error formatting logic) and various builtins in `interp/builtin.go`.
*   **Fibonacci Estimate:** 5

### 5. Circular Nameref and Recursion Depth Enforcement
*   **Description:** `bashy`'s detection of circular namerefs and the enforcement of the maximum nameref depth (limit of 8 in Bash) is either missing, produces different warnings, or fails to stop recursion correctly.
*   **Evidence:**
    ```
    > ./nameref8.sub: line 16: typeset: warning: v: circular name reference
    > ./nameref8.sub: line 18: warning: v: maximum nameref depth (8) exceeded
    ```
*   **Suspected Region:** `interp/vars.go` (nameref resolution logic in `lookupVar` or a dedicated nameref resolver).
*   **Fibonacci Estimate:** 8

### 6. Failures in Nameref-to-Array and Array-Element Mapping
*   **Description:** `bashy` struggles with namerefs that point to array elements (e.g., `declare -n ref=arr[0]`) or when array subscripts are applied directly to a nameref variable.
*   **Evidence:**
    ```
    ./nameref6.sub: line 18: typeset: x[3]: reference variable cannot be an array
    ./nameref15.sub: line 78: var[@]: bad array subscript
    ```
*   **Suspected Region:** `interp/vars.go` (handling of `expand.Indexed` and `expand.Associative` kinds during nameref resolution).
*   **Fibonacci Estimate:** 8

### 7. Incorrect `unset -n` Semantics
*   **Description:** In Bash, `unset -n` should unset the nameref variable itself. `bashy` appears to either unset the target variable or fail to distinguish the two behaviors correctly.
*   **Evidence:** Discrepancies in variable state after `unset` operations in `nameref.tests`.
*   **Suspected Region:** `interp/builtin.go` (`unset` implementation).
*   **Fibonacci Estimate:** 5

### 8. Missing Validation for Invalid Nameref Identifiers
*   **Description:** Bash 5.3 performs strict validation on the names and targets of namerefs (e.g., rejecting `/` or `%` as variable names), which `bashy` sometimes allows or reports with different error codes.
*   **Evidence:**
    ```
    > ./nameref11.sub: line 14: declare: `/': invalid variable name for name reference
    ```
*   **Suspected Region:** `interp/builtin.go` (`declare -n` validation logic).
*   **Fibonacci Estimate:** 3
