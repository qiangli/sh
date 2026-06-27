# command-p.tst — POSIX conformance status (issue #317)

Oracle: GNU bash 5.3 (`gosh --posix`). Verified every `test_*` case in
`command-p.tst` byte-for-byte against `/tmp/gosh --posix` and cross-checked
against a real bash. **All cases already match bash 5.3 — no `interp/`/`expand/`
changes were required.**

## Not a divergence to fix (gosh already matches the bash oracle)

Two yash cases declare `-e 0` but exit 1 under **both** gosh and bash 5.3, so
gosh correctly matches the oracle. They are yash-vs-bash POSIX gaps, not
gosh-vs-bash gaps; "fixing" them would *diverge* from bash 5.3:

- `output of describing external command (-v, with slash)`:
  `command -v ./foo | grep '^/' | grep '/foo$'` — bash's `command -v ./foo`
  prints `./foo` (the word as typed), not an absolute path, so `grep '^/'`
  finds nothing and the pipeline exits 1. (`test_E`, stderr is empty either way.)
- `output of describing non-special built-in (-v)`:
  `command -v echo | grep '^/'` — bash prints the builtin name `echo`, not a
  path, so `grep '^/'` exits 1. (`test_E`, stderr empty.)

Both confirmed identical on bash (`command -v ./foo` → `./foo`,
`command -v echo` → `echo`). gosh reproduces bash exactly.

## Pure-Go ceiling blockers

None. Every case in this file runs to completion under the pure-Go engine
(no interactive mode, real signals, job control, or fork/exec required).

## Self-gate

`go test ./interp/ ./expand/ -skip 'TestParseConfirm|TestRunnerRunConfirm'`
is green in a clean environment. Two `TestRunnerRun` subtests (#472, #739)
fail *only* when the local ycode shell wrapper injects a stray exported `a`
env var into the test process (a `set`/env-dump pollution artifact, the same
class of host-env gotcha documented in CLAUDE.md); they pass under
`env -i`. No code was changed, so these cannot be regressions.
