# POSIX redirection conformance — deferred cases

Scope for this pass was `interp/runner.go` only — the redirection-application
logic. Two of the four target cases need changes outside that surface and are
deferred here with reasons (no band-aids).

## Fixed in this pass

- **Case 1 "quote removal in redirection operand"** (`cat <\i'n'"0"`): already
  matched bash 5.3 before any change (prints `CONTENT`, rc 0). No edit needed.
- **Case 4 "redirections apply in order of appearance"**
  (`echo - 1>/dev/null 3>&1 2>&3 3>&- ; { 1>&- 2>&1 ; }`): fixed. Root cause was
  in `Runner.fdCaps`: a closed output slot is modeled as the `badFdWriter`
  sentinel in `r.stdout`/`r.stderr`, but `fdCaps` treated that non-nil writer as
  a live fd. So a later `2>&1` dup'ing the already-closed fd 1 succeeded instead
  of reporting EBADF. `fdCaps` now treats a `badFdWriter` slot as closed
  (`isClosedFdWriter`), so the in-order dup of a closed fd fails with
  `1: Bad file descriptor`, rc 1 — matching bash. Covered by the new
  `closed_output_fd_dup_in_order_is_bad_fd` case in
  `interp/redirect_fidelity_test.go`.

## Deferred

### Case 2 "effect of input closing" — `cat <&-`

Expected: stdin closed, external `cat` reads a closed fd → EBADF → non-zero exit,
no stdout. Actual gosh: rc 0.

Root cause: `closeFd(0)` sets `r.stdin = nil`. The default exec handler
(`interp/handler.go`, `DefaultExecHandler`) passes `hc.Stdin` straight to
`exec.Cmd.Stdin`; a nil `Stdin` makes os/exec connect the child to `/dev/null`,
so `cat` reads EOF and exits 0. The runner cannot distinguish "fd 0 explicitly
closed via `<&-`" from "no stdin configured" — both are `nil` — and even if it
could, handing the child a genuinely-closed fd is exec-plumbing work in
`handler.go`, not redirection-application logic in `runner.go`.

Why deferred (not a band-aid): a correct fix needs (a) a distinct "closed fd 0"
sentinel in the runner fd model and (b) `DefaultExecHandler` passing a real
bad/closed `*os.File` to the child for that sentinel. Both live outside
`interp/runner.go`, and changing the nil-stdin → `/dev/null` mapping risks
regressing the many commands that legitimately run with no stdin.

### Case 3 "effect of output closing" — `echo >&-`

Expected: stdout closed, builtin `echo` writes to a closed fd → non-zero exit.
Actual gosh: rc 0.

Root cause: `closeFd(1)` sets `r.stdout = badFdWriter{}`, whose `Write` returns
`ExitStatus(1)`. But the builtin output helpers `r.out`/`r.outf` discard the
write error (`io.WriteString(r.stdout, s)` ignores its return), so `echo`
finishes with success. The existing `posix_closed_output_cat_fails` case passes
only because *external* `cat` gets a real closed fd from the OS; the in-process
builtin path never observes the error.

Why deferred (not a band-aid): a faithful fix means detecting write failures in
the builtin output path (`echo`/`printf` in `interp/builtin.go`), returning
non-zero and emitting `write error: Bad file descriptor`. Making `r.out`
propagate write errors globally would change behavior for every builtin in every
context (early-closing pipes, SIGPIPE-equivalent paths) — a broad,
regression-prone change well beyond redirection-application logic and outside the
`interp/runner.go` scope of this pass.
