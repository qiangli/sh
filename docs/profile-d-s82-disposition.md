# Profile D S82 disposition

This records the requested reconciliation of
`profile-d-s82-c287d82`, run against `sh` main `8a7b94fe`. Statuses below are
the TET result codes from the remote journals: PASS, FAIL, UNRESOLVED,
UNSUPPORTED, and UNTESTED.

## Exact TP mapping

The journal is the pre-repair run. Where a baseline `FAIL` is marked as a
source defect below, the focused regression tests in this branch exercise the
same behavior after the repair. `kill` and `time` each have a normal and a
non-effective (`*_NE`) test control, so the six baseline failures in each
column are intentionally listed as three duplicated TP pairs.

| Utility | TP results | Ownership of non-PASS |
| --- | --- | --- |
| `fc` | 1 U, 2 U, 3 U, 4 FAIL, 5 FAIL, 6 U | TP4/5 are interactive-editor fixture failures: the transcript feeds `s/world/goodbye/` to the shell (`q` then becomes `command not found`) instead of driving the configured `ed` session. No portable `fc` tests are marked U. |
| `kill` | `kill.ex`: TP8, TP9, TP19 FAIL; `kill_NE.ex`: TP8, TP9, TP19 FAIL; remaining reported TPs P/U/US | Source defects fixed: `-l 0` naming and signal delivery/zero checks. Covered by `TestKillListZeroPrintsExit` and the carrier/async signal regressions. |
| `time` | `time.ex`: TP13, TP15, TP18 FAIL; `time_NE.ex`: TP13, TP15, TP18 FAIL; TP7/8 U, remaining reported TPs P/US | Source defect fixed: `time -p` uses LC_ALL > LC_NUMERIC > LANG radix precedence. TP7/8 remain explicitly not portable. Covered by `TestTimePosixLocaleRadix`. |
| `sh` | `sh_07.ex` TP36 FAIL, TP34 UNRESOLVED; `sh_09.ex` TP15 FAIL; all other reported TPs P/U/US | TP36 is the source slash-command lookup case; TP15 is the source inherited INT/QUIT case. TP34 remains harness/environment-owned. |
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
| `pwd` | TP6 FAIL; TP1–5, TP7–10 P | TP6 is a real closed-stdout diagnostic; covered by `TestPwdIssue7Interface`. |
| `echo` | TP8 FAIL; TP1–7, TP9 P; TP10 UNTESTED | TP8 is a real closed-stdout diagnostic; covered by `TestEchoIssue7Interface`. |

`fc` TP4/5, every external-version TP1/2 case in the builtin suites, and the
explicitly unsupported or untested cases are harness/fixture-owned. The
external-version failures are not evidence that the in-process builtin is
missing: POSIX command lookup must select the builtin, while `exec` can only
replace the shell with an executable file. POSIX semantics take precedence
over GNU behavior in the local focused tests.

## Source regressions covered

The focused tests cover `command -v` broken-pipe reporting when `SIGPIPE` is
ignored, `kill -l 0`/invalid 128 handling, locale radix selection for `time
-p`, and inherited SIGINT/SIGQUIT ignoring for asynchronous jobs. Existing
Issue 7 interface tests cover the corresponding `echo`/`pwd` closed-output
diagnostics (and `cd`, `alias`, and `printf`).
