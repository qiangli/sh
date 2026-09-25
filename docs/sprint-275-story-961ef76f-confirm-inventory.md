# Sprint 275 Story 961ef76f4497 Confirm Inventory

Date: 2026-09-24

Candidate: `agent/weave-issue-7` after cherry-picking harness commit
`30b5d68ed5679269ed516c71fd1b470827970dbd` from isolated sh weave #5, plus
the narrow harness corrections in this workspace.

Comparator: `/opt/homebrew/bin/bash`, `GNU bash, version 5.3.15(1)-release
(aarch64-apple-darwin25.4.0)`. The harness rejects bashy even when
`~/.local/bin/bash` appears first on `PATH`.

Command:

```sh
set -o pipefail
REQUIRE_SHELLS=1 go test -v -timeout 30m ./interp -run '^TestRunnerRunConfirm$' -count=1 2>&1 | tee /tmp/sprint275-961ef76f/testrunnerrunconfirm.final.raw.log
```

Outcome: `FAIL`. Every numbered case was accounted for: 1,998 total subtests,
1,871 pass, 113 fail, 14 skip.

Skipped case IDs: `0886 1864 1865 1959 1960 1961 1962 1963 1964 1965 1966
1967 1968 1989`.

Failed case IDs: `1641 1971 1969 1297 1291 1290 1275 1276 1272 1274 1273
1271 1268 1270 1269 1267 1264 1261 1263 1262 1195 1185 1179 1148 1147 1137
1136 1135 1134 1132 1131 1133 1130 1129 1128 1112 1126 1110 1109 1108 1106
1107 1105 1085 1080 1076 1078 1082 1079 1077 0159 0113 0049 0021 0914 0913
0907 0906 0905 1615 1713 1594 1659 0888 0885 0867 0866 1454 0863 0856 1429
1430 1407 0823 1912 1907 1887 1867 1866 0674 0575 0576 0573 0574 0565 0513
0561 0560 0554 0539 0525 1657 1656 1654 0489 0514 1650 1646 1645 1642 1643
1644 0382 1313 1312 0335 0239 1527 0191 1808 1809 1791 1818`.

## Harness Corrections

- GNU Bash discovery now scans `PATH` and explicit common install locations,
  deduplicates candidates, and rejects version strings containing `bashy`.
- The comparator starts with `argv[0] == "bash"` and an explicit confirm
  environment. `HOME` is selected per case: a real temp directory by default,
  the literal leading `HOME=/abs` assignment when present, and `$PWD/home` for
  the two cases that create that directory themselves. This fixes the previous
  tilde and glob pollution harness errors without changing fixtures.
- `strmatch.so` still builds on Darwin with `-dynamiclib -undefined
  dynamic_lookup`, and now builds on non-Darwin hosts with `-shared -fPIC`.
  With `REQUIRE_SHELLS=1`, missing GNU Bash 5.3 or missing loadable headers is
  fatal, not a green skip.

## First-Cause Groups

Diagnostic prefix / non-interactive fatality differences: `0113 0382 0513 0514
0573 0574 0575 0576 0823 0888 1105 1106 1107 1108 1109 1110 1112 1128 1129
1130 1131 1132 1133 1134 1135 1136 1137 1179 1185 1312 1313 1643 1644 1645
1646 1650 1654 1659 1818`.

Option-name shorthand expectations not accepted by GNU Bash `set -o/+o`:
`1261 1262 1263 1264 1267 1268 1269 1270 1271 1272 1273 1274 1275 1276 1290
1291`.

Arithmetic / parameter semantics still genuinely differ from Bash 5.3:
`0335 0489 0525 0560 1076 1077 1078 1079 1080 1082 1085 1126 1147 1148 1527`.

Array / nameref / assignment-word behavior differences: `0539 0554 0561 1615
1641 1642 1656 1657 1887`.

Job control, signal, background, or process-environment model differences:
`0863 0866 0867 0905 0906 0907 0913 0914 1454 1594 1791 1808 1809 1907 1912`.

Formatting/platform output differences: `0049 0159 0239 0674 0885 1195 1407
1969 1971`.

Loadable/extglob/parser-timing mismatch: `1713`.

Harness-only command not present in GNU Bash: `0191` (`runner-state` is a
test-only interpreter helper, not a Bash builtin).

Timing-sensitive read behavior: `1866 1867`.

`#JUSTERR` expected an error, but GNU Bash 5.3 returned success without a
`bash:` diagnostic: `0021 1297 0539 1791 1912`.

## Disposition

Harness/environment errors corrected here: Bash discovery, per-case startup
HOME, temp-home glob pollution, and Linux-portable loadable compiler flags.
No `runTests` fixture was edited.

Unresolved cases remain red by the exact IDs above. They are not hidden by
skips. The reproducible command is the command recorded above. First observable
limit for this inventory is the integrated candidate after `30b5d68e` plus this
story's harness corrections; no narrower product bisection was performed in
this follow-up because the remaining set is a categorized diagnostic inventory
and several groups are comparator/fixture-normalization mismatches rather than
clear narrow interpreter defects.
