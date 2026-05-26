# Plan: punted builtins (coproc / fg / umask / logout)

After the merge of bashy's Phase 3-8 work onto qiangli/sh master, local's
explicit implementations for `fg`/`bg`/`jobs`/`fc`/`umask`/`logout`/etc.
took precedence over qiangli's outpost-aware "unsupported in this shell — …"
hints. The merged spot-check tests and `TestUnsupportedHints` were trimmed
accordingly. This document captures the design work needed to bring four
of those builtins from "merged but minimal" to "actually correct".

Order of implementation:

1. **logout** + **exit** correctness (smallest, paves the way) ← this batch
2. **umask** (independent, small-medium) ← this batch
3. **fg** (reuses `wait`'s PID lookup work we already merged)
4. **coproc** (depends on numbered-fd support — biggest piece)

---

## 1. `logout` — gate `exit` on a login-shell flag

### Current state

`interp/builtin.go` `case "logout":` just sets `r.exit.exiting = true`
with no gating and no exit code propagation. Bash errors with `not login
shell: use 'exit'` if invoked from a non-login shell, otherwise behaves
like `exit`.

### Design

- Add `loginShell bool` field to `Runner` (`interp/api.go`).
- Add `WithLoginShell(bool)` `RunnerOption` so embedders (cmd/bashy
  interactive mode, outpost SSH session attach) can opt in.
- `case "logout":` becomes:
  - If `!r.loginShell`: return failure with the bash-compatible message.
  - Otherwise: reuse the same code path as `case "exit":` (accept 0 or 1
    arg, propagate `r.lastExit` if 0, parse code if 1).
- Wire Ctrl-D in `cmd/bashy/interactive.go` to the same logout-aware exit
  path when the runner was created with `WithLoginShell(true)`.

### Tradeoff

The flag is a single bool; bash also tracks "interactive" separately
(`$-` contains `i`). We're not modeling interactive vs. login distinction
here — outpost cares about login (it owns the session lifetime).
Future-proof by using a flag rather than baking the assumption into the
exit code path.

---

## 2. `umask` — per-Runner virtual umask

### Current state

`case "umask":` calls `syscall.Umask(int(mask))` directly. This is
**process-wide**: two Runners in the same Go process clobber each other,
and any non-shell code in the same binary inherits the shell's umask.
Outpost (which can host multiple Runners) cannot tolerate this.

The bare `umask` (no args) prints `0022` regardless of the actual mask.

### Design

- Add `umask int` field to `Runner`, defaulting to the process umask at
  Runner creation (read it non-destructively via the
  `m := syscall.Umask(0); syscall.Umask(m)` idiom — locked behind a
  package-level mutex to avoid races during init).
- `case "umask":` reads/writes `r.umask` only; never touches
  `syscall.Umask` after init.
- Apply the mask at the file-creation chokepoint:
  `r.open(ctx, path, flags, mode)` in `interp/runner.go`. When
  `flags & os.O_CREATE != 0`, transform `mode &^= os.FileMode(r.umask)`
  before delegating to `r.openHandler`.
- `mkdir`/`mkfifo`/etc. paths (if added later) follow the same pattern.

### Tradeoff

The umask is applied **only** to file-creation calls routed through the
runner. A custom `OpenHandler` that bypasses the runner's `open` method
(e.g., outpost middleware that calls `os.OpenFile` directly) won't have
the umask applied. That's a deliberate limit: we're modeling shell
umask, not system umask. Document on the field.

### Notes

- `syscall.Umask` only exists on Unix; gate via `_unix.go`/`_notunix.go`
  for the *init read*. On non-Unix, default `r.umask = 0o022`.
- Bare `umask` should print the actual mask, formatted as 4-digit octal
  with leading zero, e.g. `umask` → `0022` when the mask is `0o022`.
- `umask -S` (symbolic mode output) is out of scope for this batch.

---

## 3. `fg` — channel-based wait with optional SIGCONT

### Current state

After the merge, `case "fg":` returns `"fg: no current job"` when empty,
and on `fg %N` it waits via `<-bg.done` and propagates `*bg.exit`. Two
gaps:

1. It only accepts `%N` job-spec form; `fg <real-pid>` and `fg gN`
   sentinel aren't recognized (the `wait` merge fixed this for `wait`
   but `fg` wasn't updated).
2. If the underlying real PID was stopped (e.g. an external SIGSTOP),
   `<-bg.done` blocks forever because the process never finishes.

### Why we are not copying outpost's `outpost fg`

`outpost fg <pid>` (cmd/outpost/jobs.go) is a **different abstraction**:
- It reads a persistent on-disk registry of detached PIDs populated via
  `WithBgPidCallback` — pids that survived the original SSH session.
- It polls `syscall.Kill(pid, 0)` every 250 ms because the proc ref is
  gone; the OS exit status cannot be captured.
- It always returns 0 on natural exit (the comment in the file calls
  this out explicitly: "the OS exit status is not captured — this is the
  qiangli/sh detached process trade-off").

The in-shell builtin has strictly more information: the `bgProc` struct
with its `done` channel and `exit` pointer. Polling and dropping the
exit status here would be a regression. The two implementations stay
separate by design — outpost's is for cross-process detached jobs; ours
is for in-process bgProcs.

### Design (implementing in this batch)

- Mirror the merged `wait` logic for argument forms:
  - no args → most recently started `bgProc`
  - `%N` → `bgProcs[N-1]` (bash job-spec form)
  - `gN` sentinel → `bgProcs[N-1]` (matches `$!` legacy form)
  - bare integer → real-PID lookup by scanning `bg.pid.Load()`
- Once a target is picked, **if a real OS PID has been published**
  (`bg.pidReady` closed, `bg.pid.Load() > 0`), send `SIGCONT`
  best-effort to resume any stopped process. Errors are ignored — the
  proc may already be running or gone.
- Then wait `<-bg.done` and propagate exit via `r.exit`.
- For goroutine-only bgProcs (no real PID), the SIGCONT step is
  skipped. There's no "foreground" semantically because there was no
  process group transition.

### Tradeoff

Without process group / terminal control (the in-process shell has no
controlling TTY of its own), `fg` cannot truly "reattach" stdio the way
bash does. The implementation only waits + propagates exit. Embedders
that need real TTY reattach (interactive `cmd/bashy`, outpost SSH
session takeover) will need a `WithFgHandler` middleware in a later
batch; not in scope here.

### Cross-platform

`syscall.SIGCONT` is only defined on Unix. The SIGCONT step is gated
through a new `continueIfStopped(pid int)` helper in
`kill_unix.go`/`kill_notunix.go` — on non-Unix it's a no-op (there are
no suspended jobs to resume).

---

## 4. `coproc` — numbered-fd refactor

### Phase 1 — DONE (`fdTable` + coproc reads)

Shipped via `interp/api.go` (`Runner.fdTable map[int]*os.File`) and
`interp/runner.go`:

- `coproc` registers both pipe ends in `fdTable` keyed by the real OS
  fd numbers it also writes into `${COPROC[0]}` / `${COPROC[1]}`.
- `syntax.DplIn` (`<&N`) and `syntax.DplOut` (`>&N`) now look up
  numeric args via `fdTable`; arbitrary fds work for the dup forms
  instead of returning `unhandled %v arg`.
- Subshells clone `fdTable` so child mutations don't leak to the
  parent (bash inherits fds; the underlying `*os.File` handles are
  shared, the map slot ownership is not).

End-to-end test:

```sh
coproc CO { read line; echo got=$line; }
echo hi >&${CO[1]}
read out <&${CO[0]}
echo $out          # → "got=hi"
```

### Phase 2 — still pending

The redirect layer still hardcodes 0/1/2 for the `rd.N` numeric prefix
in `interp/runner.go` `redir()`. The Phase 2 work is:

- `exec N<file` / `exec N>file` / `exec N<>file` → open and store the
  file in `fdTable[N]` (persistent because `keepRedirs` is set for
  `exec`).
- Plain `N>file` / `N<file` (no exec) → same routing but scoped to the
  command via the save/restore dance in `stmtSync`.
- `N>&M` / `N<&M` with arbitrary N → mutate `fdTable[N]` to reference
  `fdTable[M]` (or stdin/stdout/stderr if M is 0/1/2).
- `{varname}>` / `{varname}<` named-fd allocator → currently writes a
  hard-coded "10" into the named var; should pick a real unused fd
  number from a per-runner allocator and route through `fdTable`.
- `cls.Close()` semantics for keepRedirs: today `defer cls.Close()` in
  `stmtSync` runs even when `keepRedirs` is true, which means
  `exec 3<file` would close the file immediately. Need to gate the
  close on `!keepRedirs` (or move ownership into `fdTable`).

### Tradeoff (still applies)

Real OS fds are still required for `exec` of external commands — bash
passes them through `execve`'s fd inheritance, which only works with
real fds. So the channel-based emulation idea is a dead end for any
script that ever execs an external program against a coproc fd. Phase 1
+ Phase 2 keep real `os.Pipe()` and a virtual fd table — that's the
general answer.

---

## Out of scope for any of this batch

- `bind` (readline keybindings) — irrelevant outside `cmd/bashy`
  interactive mode; punt.
- `caller` / `help` — local has stubs; not blocking outpost.
- `compgen` / `complete` / `compopt` — local stubs are fine; programmable
  completion is genuinely an SSH-client concern.
- `enable -n` (disable builtin) — local tracks it via
  `r.disabledBuiltins`; works for what outpost needs.
- `times` — local has the stub; the `time CMD` form already works via
  `syntax.TimeClause`.
