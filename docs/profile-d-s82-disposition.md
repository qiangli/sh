# Profile D S82 disposition

This records the requested reconciliation of
`profile-d-s82-c287d82`, run against `sh` main `8a7b94fe`. Statuses below are
the TET result codes from the remote journals: PASS, FAIL, UNRESOLVED,
UNSUPPORTED, and UNTESTED.

## Exact TP mapping

| Utility | TP results | Ownership of non-PASS |
| --- | --- | --- |
| `fc` | 1 U, 2 U, 3 U, 4 FAIL, 5 FAIL, 6 U | TP4/5 are interactive-editor fixture failures: the transcript feeds `s/world/goodbye/` to the shell (`q` then becomes `command not found`) instead of driving the configured `ed` session. No portable `fc` tests are marked U. |
| `kill` | 1 P, 2 P, 3 UNSUPPORTED, 4 UNTESTED, 5 P, 6 P, 7 P | No source failure. |
| `time` | 1 P, 2 P, 3 UNSUPPORTED, 4 P, 5 P, 6 P, 7 U, 8 U, 9 P | No source failure; TP7/8 are explicitly not portable. |
| `sh` | 1 P, 2 P, 3 UNSUPPORTED, 4 P, 5 P, 6 UNSUPPORTED, 7 P, 8 P, 9 P, 10 UNSUPPORTED | No source failure. |
| `shell` | 1–11 P | No source failure. |
| `alias` | 1 FAIL, 2 UNRESOLVED, 3 UNSUPPORTED, 4 UNTESTED, 5–10 P | TP1/2 are the suite's external-version `exec`/C-binding route; the intrinsic builtin itself passes. |
| `cd` | 1 FAIL, 2 UNRESOLVED, 3 UNSUPPORTED, 4–10 P | TP1/2 are external-version routing; builtin semantics pass. |
| `command` | 1 FAIL, 2 UNRESOLVED, 3 UNSUPPORTED, 4–9 P, 10 UNSUPPORTED | TP1/2 are external-version routing; TP10 is fixture capability. |
| `getopts` | 1 FAIL, 2 UNRESOLVED, 3 UNSUPPORTED, 4–5 P | TP1/2 are external-version routing. |
| `read` | 1 FAIL, 2 UNRESOLVED, 3 UNSUPPORTED, 4–9 P | TP1/2 are external-version routing. |
| `umask` | 1 FAIL, 2 UNRESOLVED, 3 UNSUPPORTED, 4–10 P | TP1/2 are external-version routing. |
| `wait` | 1 FAIL, 2 UNRESOLVED, 3 UNSUPPORTED, 4 P, 5 UNTESTED, 6–9 P, 10 UNSUPPORTED | TP1/2 are external-version routing; no wait semantic failure. |
| `hash` | 1–5 P, 6 FAIL, 7 UNRESOLVED, 8 UNSUPPORTED, 9 UNRESOLVED, 10 UNTESTED | TP6/7 request an external `hash`; TP9 is `hash -- -fooy`, which correctly reaches operand lookup and fails because `-fooy` is not a command. |
| `unalias` | 1 FAIL, 2 UNTESTED, 3 UNSUPPORTED, 4 P, 5 UNTESTED, 6–9 P, 10 UNTESTED | TP1/2 are external-version routing. |
| `pwd` | 1–5 P, 6 FAIL, 7–10 P | TP6 is a real closed-stdout diagnostic; covered by `TestPwdIssue7Interface`. |
| `echo` | 1–7 P, 8 FAIL, 9 P, 10 UNTESTED | TP8 is a real closed-stdout diagnostic; covered by `TestEchoIssue7Interface`. |

`fc` TP4/5, the external-version TP1/2 cases, and the explicitly unsupported
or untested cases are harness/fixture-owned. POSIX semantics take precedence
over GNU behavior in the local focused tests.

## Source regressions covered

The focused tests cover `command -v` broken-pipe reporting when `SIGPIPE` is
ignored, `kill -l 0`/invalid 128 handling, locale radix selection for `time
-p`, and inherited SIGINT/SIGQUIT ignoring for asynchronous jobs. Existing
Issue 7 interface tests cover the corresponding `echo`/`pwd` closed-output
diagnostics (and `cd`, `alias`, and `printf`).
