# dbg-support Fixture Cluster Analysis

This document clusters the failures observed in the `dbg-support.tests` fixture from the Bash 5.3 test suite when run against `bashy`.

## Candidate Issues

### 1. Unsupported `extdebug` Shopt and Extended Debugging Logic
*   **Description:** The `extdebug` shell option is accepted in `bashOptsTable` but appears to report itself as "unsupported" during script execution and fails to enable critical debugger behaviors. Specifically, it should enable the `BASH_ARGC`/`BASH_ARGV` stacks and enhanced `caller` output.
*   **Evidence:**
    ```
    ./dbg-support.tests: line 19: shopt: unsupported option "extdebug"
    ```
*   **Suspected Region:** `interp/builtin.go` (shopt case) and `interp/api.go` (ensuring `supported: true` is properly respected by the builtin).
*   **Fibonacci Estimate:** 5

### 2. Missing `functrace` (`set -T`) and Trap Inheritance
*   **Description:** `bashy` does not support `set -o functrace` (or its shorthand `-T`), which is required for `DEBUG` and `RETURN` traps to be inherited by shell functions and sourced scripts. This results in these traps not firing within function scopes in the test.
*   **Evidence:**
    ```
    ./dbg-support.tests: line 76: set: invalid option: "functrace"
    ./dbg-support.tests: line 91: set: invalid option: "+T"
    ```
*   **Suspected Region:** `interp/api.go` (missing from `noOpSetOptions` or `posixOptsTable`) and `interp/runner.go` (logic for trap inheritance).
*   **Fibonacci Estimate:** 8

### 3. Incorrect `$LINENO` and `$FUNCNAME` Context in Traps
*   **Description:** When a `DEBUG` or `RETURN` trap fires, the value of `$LINENO` and `${FUNCNAME[@]}` inside the trap body is often incorrect (typically showing line `1` and missing the caller's function name).
*   **Evidence:**
    ```
    < debug lineno: 1 
    ---
    > debug lineno: 78 main
    ```
*   **Suspected Region:** `interp/runner.go` (specifically `fireDebugTrap` and `trapCallback` where `OverrideLineno` is set).
*   **Fibonacci Estimate:** 5

### 4. `caller` Builtin Discrepancies and Missing Source Info
*   **Description:** The `caller` builtin does not correctly report the call stack depth or the source file/line of the caller, particularly when `extdebug` should be providing extra detail.
*   **Evidence:**
    ```
    < debug lineno: 1 fn1
    ---
    > debug lineno: 35 fn1 85 ./dbg-support.tests
    ```
*   **Suspected Region:** `interp/builtin.go` (the `caller` case) and `interp/runner.go` (maintenance of `callStack`).
*   **Fibonacci Estimate:** 3

### 5. Incorrect `BASH_SOURCE` and `BASH_LINENO` during Sourcing
*   **Description:** `BASH_SOURCE` is not updated correctly when a script is sourced, often retaining the name of the main script instead of the sourced file.
*   **Evidence:**
    ```
    < SOURCED BASH_SOURCE[0] ./dbg-support.tests
    ---
    > SOURCED BASH_SOURCE[0] ./dbg-support.sub
    ```
*   **Suspected Region:** `interp/runner.go` (source command handling and `callStack` entry creation).
*   **Fibonacci Estimate:** 5

### 6. Missing `BASH_ARGC` and `BASH_ARGV` Dynamic Variables
*   **Description:** `bashy` lacks the dynamic arrays `BASH_ARGC` and `BASH_ARGV`, which should track the number of arguments and the arguments themselves for each entry in the call stack when `extdebug` is enabled.
*   **Evidence:** Inferred from missing functionality required by debugger-style scripts in the fixture.
*   **Suspected Region:** `interp/vars.go`.
*   **Fibonacci Estimate:** 8
