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

## 3. `fg` — copy outpost's PID-based implementation

### Current state

`case "fg":` returns `"fg: no current job"` with exit 1 when there are no
bg procs. There's no actual foregrounding when there are bg procs.

### Design (next batch — not implemented here)

- Reuse the `wait` builtin's PID lookup logic we already merged (which
  handles both legacy `gN` sentinels and real OS PIDs).
- Default target: most recently started `bgProc` (top of the stack).
- For real-PID procs: send `SIGCONT` (via the existing
  `interp/kill_unix.go`/`kill_notunix.go` infrastructure), then wait on
  `bg.done`, then propagate exit status via `r.exit`.
- For goroutine-only procs (no real PID): degenerate to waiting on
  `bg.done`. There's no "foreground" semantically because there was no
  process group transition.
- Optional: a `WithFgHandler` middleware so outpost can intercept and
  delegate to its own job manager when the embedder owns the PID
  registry (mirrors `WithBgPidCallback`).

### Tradeoff

Without process group / terminal control (we have no controlling TTY in
this in-process shell), `fg` cannot truly "reattach" stdio the way bash
does. The implementation can only wait + propagate exit. Embedders that
need real TTY reattach must implement `WithFgHandler` themselves; that's
what outpost already does for its `outpost fg <pid>` command.

---

## 4. `coproc` — numbered-fd refactor (largest piece)

### Current state

`*syntax.CoprocClause` in `interp/runner.go` creates real `os.Pipe()`s
and exposes the raw fd numbers in `COPROC[0]` / `COPROC[1]`. Reading
from / writing to those fds via `${COPROC[N]}` array indexing works.
**Redirects** like `<&"${COPROC[0]}"` and `>&"${COPROC[1]}"` do not,
because the redirect layer only handles fds 0/1/2.

### Design (next batch — not implemented here)

The blocker isn't `coproc`-specific. It's a general "the redirect layer
needs numbered-fd support" gap. Recommended approach:

- Add a runner-level `fdTable map[int]*os.File` keyed by fd number.
- `coproc` registers `${NAME[0]}` and `${NAME[1]}` into the table.
- `exec N<...`, `exec N>...`, `exec N<>...`, `<&N`, `>&N` all consult /
  mutate this table instead of assuming 0/1/2.
- `dupFd(src, dst)` becomes an indirection through the table rather
  than direct stdio struct field assignment.

This unlocks coproc, `exec 3<&0`, FIFO process substitution edge cases,
and several lurking redirect bugs at once.

### Tradeoff

Real OS fds are still required for `exec` of external commands — bash
passes them through `execve`'s fd inheritance, which only works with
real fds. So the channel-based emulation idea is a dead end for any
script that ever execs an external program against a coproc fd. Keeping
real `os.Pipe()` and adding a virtual fd table is the more general
answer.

### Estimated effort

Medium-large. Touches `runner.go` (redirect handling), `handler.go`
(possibly a new handler kind), and several test expectations. Defer
until after `umask`/`logout`/`fg` are in.

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
