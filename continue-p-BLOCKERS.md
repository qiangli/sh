# continue-p.tst conformance

All `continue-p` cases already match GNU bash 5.3 under `gosh --posix`; no
interp/expand changes were needed. Verified byte-for-byte, including the edge
cases:

- `continue 0` → errors to stderr with non-zero exit (yash `-d -e n`).
- `continue N` larger than the actual nest depth → clamps to the outermost
  enclosing loop (one-more, much-more cases).
- `eval continue`, and `continue` inside `{ }`, `if`/`then`/`else`, `case`,
  before/after `&&`/`||` — all unwind the loop correctly.
- exit status of `continue` with `$? > 0` is 0.

## Non-blocker divergence (yash vs GNU bash, gosh follows bash)

`test_OE 'continuing with !'`:

```
for i in 1; do
    ! continue
    echo not reached
done
```

yash/POSIX expects final exit status 0. GNU bash (verified on 3.2 and the 5.3
build) negates the `continue` builtin's 0 status to 1, so the loop's final
exit status is 1. `gosh --posix` already produces exit 1, matching GNU bash —
which is the conformance target — so this is intentionally left as-is rather
than "fixed" toward the yash expectation.

No pure-Go (fork/exec, signals, job-control, interactive) blockers in this file.
