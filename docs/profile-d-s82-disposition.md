# Profile D S82 disposition

This records the requested reconciliation of
`profile-d-s82-c287d82`, run against `sh` main `8a7b94fe`. Statuses below are
the TET result codes from the remote journals: PASS, FAIL, UNRESOLVED,
UNSUPPORTED, and UNTESTED.

## Exact TP mapping

The journal is the pre-repair run. Where a baseline `FAIL` is marked as a
source defect below, the focused regression tests in this branch exercise the
same behavior after the repair. Each row gives the exact source-failure and
unresolved-harness TP mapping, plus the complete non-PASS set for the smaller
controls. The very large `sh` capability controls are condensed to their TET
classes; their raw, exact records remain in the cited journal. `F`, `R`, `US`,
and `U` mean the journal's FAIL, UNRESOLVED, UNSUPPORTED, and UNTESTED
classifications, respectively. The parenthesized `sN` retains the raw TET
status code where the harness did not give the result a useful textual name.

| Utility | TP results | Ownership of non-PASS |
| --- | --- | --- |
| `fc` | F: 4, 5, 7, 10, 11, 17–19, 21, 22, 26, 28, 29, 31–34; R: 8, 27, 30; U: 1–3, 6, 12, 20, 24, 35–48, 50–52 | The failing/editor-dependent cases are fixture-owned: its transcript feeds `s/world/goodbye/` and `q` to the shell instead of driving the configured `ed` session. The `R` cases then consume malformed history data produced by that fixture. |
| `kill` | `kill.ex` and `kill_NE.ex`: F 8, 9, 19; `kill.ex` also s3: 11–13, 21; U: 4, 15, 17; US: 3; warning: 24 | Source defects fixed: `-l 0` naming and asynchronous signal delivery/zero checks. Covered by `TestKillListZeroPrintsExit` and the carrier/async signal regressions. |
| `time` | `time.ex` and `time_NE.ex`: F 13, 15, 18; `time.ex`: U 7, 8, 11, 14, 16, 17, 23, 26, 29; US 3; warning 20 | Source defect fixed: `time -p` uses LC_ALL > LC_NUMERIC > LANG radix precedence. TP7/8 remain explicitly not portable. Covered by `TestTimePosixLocaleRadix`. |
| `sh` | `sh_07.ex`: F 36, R 34; `sh_09.ex`: F 15; the remaining non-PASS TPs are the journal's U/US capability controls | TP36 is the source slash-command lookup case; TP15 is the source inherited INT/QUIT case. TP34 remains harness/environment-owned. |
| `shell` | 1–11 P | No source failure. |
| `alias` | F 1; R 2; US 3; U 4, 11, 14–19, 22–25 | TP1/2 are the suite's external-version `exec`/C-binding route; the intrinsic builtin itself passes. |
| `cd` | F 1; R 2, 41; US 3, 16, 37 | TP1/2 are external-version routing. TP41 is the harness's invalid empty-symlink setup; TP16/37 are capability controls. |
| `command` | F 1, 49; R 2; US 3, 10–21, 28, 43–45; U 41 | TP1/2 are external-version routing. TP49 was the real closed-output `command -v` defect, fixed by `TestCommandVReportsBrokenPipeWriteError`; capability controls own the rest. |
| `getopts` | F 1; R 2; US 3 | TP1/2 are external-version routing. |
| `read` | F 1; R 2; US 3; U 12, 14, 16 | TP1/2 are external-version routing. |
| `umask` | F 1; R 2; US 3 | TP1/2 are external-version routing. |
| `wait` | F 1; R 2; US 3, 10; U 5, 16, 17 | TP1/2 are external-version routing; no wait semantic failure. |
| `hash` | F 6; R 7, 9; US 8; U 10, 12, 14, 16–24 | TP6/7 request an external `hash`; TP9 is `hash -- -fooy`, which correctly reaches operand lookup and fails because `-fooy` is not a command. |
| `unalias` | F 1; US 3; U 2, 5, 10–15, 17, 18, 21–24 | TP1/2 are external-version routing. |
| `pwd` | F 6, 15, 16; R 17; US 3; warning 18 | TP6 is the real closed-stdout diagnostic, covered by `TestPwdIssue7Interface`. TP15/16 are PATH_MAX fixture/environment failures and TP17 is the empty-symlink setup failure. |
| `echo` | F 8; US 3; U 10–15; warning 17 | TP8 is the real closed-stdout diagnostic, covered by `TestEchoIssue7Interface`. |

The `fc` non-PASS set, every external-version TP1/2 case in the builtin
suites, and the explicitly unsupported or untested cases are harness/fixture-owned. The
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
