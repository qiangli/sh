# andor-p.tst — POSIX and-or list conformance

Base: bf858baf. Harness: `printf '%s' "<script>" | /tmp/gosh --posix`, compared
byte-for-byte against GNU bash 5.3.

## Status: all 14 cases pass

All 14 `andor-p.tst` cases match bash 5.3 byte-for-byte:
- 2-command `&&`/`||` lists (success+failure permutations): 8 cases
- 3-command list: 1 case
- Exit status of list (success/failure): 2 cases
- Linebreak after `&&`/`||`: 2 cases
- Pipelines in list: 1 case

No interp/expand changes were needed. The and-or list short-circuit logic in
`interp/runner.go` (`cmd()` → `case *syntax.BinaryCmd`, `AndStmt`/`OrStmt`
branches) and the `set -e` exemption in `errExitExemptByAndOr()` are already
POSIX-conformant.

## Deferred

None. All test cases are fully covered by the pure-Go interpreter.
