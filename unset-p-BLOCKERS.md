# unset-p.tst — POSIX conformance verification

Verified every case in the yash `unset-p.tst` suite against `gosh --posix`
(byte-for-byte stdout, exit status, and stderr-presence). **All cases already
pass; no interp/ or expand/ changes were needed.**

Setup: `a b c d x` external scripts on `PATH=.:$PATH` (per the suite's `setup`).

| Case | Result |
|------|--------|
| deleting existing variable (default) | PASS — stdout `unset 2`, exit 0 |
| deleting non-existing variable (default) | PASS — `1 2 unset`, exit 0 |
| deleting many variables (default) | PASS — `unset unset unset 4 unset`, exit 0 |
| only variable is deleted by default | PASS — `unset`, exit 0 |
| deleting many variables (-v) | PASS — `unset unset unset 4 unset`, exit 0 |
| only variable is deleted (-v) | PASS — `unset`, exit 0 |
| deleting existing function (-f) | PASS — `a` then `external b`, exit 0 |
| deleting non-existing function (-f) | PASS — `a` then `external b`, exit 0 |
| deleting many functions (-f) | PASS — `external a/b/c`, `d`, `external x`, exit 0 |
| only function is deleted (-f) | PASS — `1`, exit 0 |
| read-only variable cannot be deleted (default) | PASS — empty stdout, error on stderr, non-zero exit, `echo not reached` not run (special-builtin error aborts the non-interactive shell) |
| read-only variable cannot be deleted (-v) | PASS — same as above |

## Blockers

None. No case required interactive mode, real signals/job-control, or
fork/exec. Nothing deferred.
