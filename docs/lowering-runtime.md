# Bash++ bounded runtime foundation

`mvdan.cc/sh/v3/lower/shellrt` is the runtime a compiled Bash++ program links
against. Alongside the scalar output helpers already in `runtime.go`, it now
carries an explicit **persistent shell state boundary** (`session.go`) and a
**task ownership model** (`task.go`).

This document freezes the API the compiler glue will be written against. It
also states honestly what is not implemented yet.

## Scope

- A generated program holds one root `*Session`. The session owns persistent
  shell state and every task launched from it.
- Dynamic shell is reached only through the `ShellRunner` seam, and only for
  regions the syntax tree already identified as shell. There is no whole-program
  wrapper: a typed program is never handed to an interpreter as a fallback.
- A typed-only artifact links no interpreter at all. `lower/shellrt` imports
  nothing outside the standard library (see *Package dependency invariant*).

## Frozen API

```go
// ---- persistent shell state ----------------------------------------------

type VarKind uint8
const (Scalar VarKind = iota; Indexed; Associative)

type Var struct {
    Kind     VarKind
    Str      string            // Kind == Scalar
    List     []string          // Kind == Indexed
    Map      map[string]string // Kind == Associative
    Exported bool
    ReadOnly bool
}
func (v Var) String() string          // scalar view: element 0 of an array
func (v Var) Equal(o Var) bool

type State struct {
    Dir     string
    Vars    map[string]Var
    Options map[string]bool
    Status  int
}
func (s State) Clone() State
func (s State) Environ() []string      // sorted NAME=value, exported scalars

type Stdio struct { In io.Reader; Out, Err io.Writer }

func KnownOptions() []string           // errexit noglob nounset pipefail xtrace

// ---- dynamic shell seam ---------------------------------------------------

type ShellRunner interface {
    RunShell(ctx context.Context, st *State, io Stdio, src string) error
    Clone(io Stdio) (ShellRunner, error)
    Close() error
}
var ErrNoShell error

// ---- session --------------------------------------------------------------

type Session struct{ /* ... */ }
type SessionOption func(*Session) error

func NewSession(opts ...SessionOption) (*Session, error)
func WithDir(path string) SessionOption
func WithEnviron(pairs ...string) SessionOption
func WithVars(vars map[string]Var) SessionOption
func WithStdio(in io.Reader, out, err io.Writer) SessionOption
func WithOption(name string, on bool) SessionOption
func WithShell(sh ShellRunner) SessionOption
func WithContext(ctx context.Context) SessionOption

func (s *Session) Snapshot() State
func (s *Session) Dir() string
func (s *Session) Chdir(path string) error
func (s *Session) Get(name string) (Var, bool)
func (s *Session) Set(name string, v Var)
func (s *Session) SetString(name, value string)
func (s *Session) Unset(name string)
func (s *Session) Export(name string)
func (s *Session) Environ() []string
func (s *Session) Option(name string) bool
func (s *Session) SetOption(name string, on bool) error
func (s *Session) Status() int
func (s *Session) SetStatus(code int)
func (s *Session) Stdio() Stdio
func (s *Session) SetStdio(in io.Reader, out, err io.Writer)
func (s *Session) Context() context.Context
func (s *Session) Shell(ctx context.Context, src string) error

// ---- tasks ----------------------------------------------------------------

type TaskFunc func(ctx context.Context, child *Session) error

type TaskError struct { Ordinal uint64; Err error }   // Error, Unwrap
type TaskPanic struct { Ordinal uint64; Value any; Stack []byte }
var ErrSessionClosed error

type Task struct{ /* ... */ }
func (t *Task) Ordinal() uint64
func (t *Task) Session() *Session      // nil if the task never started
func (t *Task) Wait() error

func (s *Session) Go(fn TaskFunc) *Task
func (s *Session) Arm()
func (s *Session) Blocking(fn func() error) error
func (s *Session) Cancel()
func (s *Session) Join() error
func (s *Session) Close() error
func (s *Session) Active() int         // diagnostics only
```

## Semantics

### State is a projection, not the shell

The shell's rich state — functions, traps, aliases, array attributes such as
`declare -i`, option state beyond `KnownOptions` — lives in the `ShellRunner`
and stays there. It is never serialized through `State` and never rebuilt from
it. `State` is only the part the typed side can read and write: cwd, variables
(including array **values**, which are carried, not flattened), the projected
options and `$?`.

A backend applies only what the typed side actually changed since the last
projection. Re-applying the whole projection would flatten shell-only
attributes on variables the typed program never touched.

### Persistence

One backend per session, alive for the session's whole life. A region that
defines a function, sets `set -u`, builds an array or leaves a non-zero status
is observed by every later region of that session. The reference backend runs a
region statement by statement rather than as a whole `*syntax.File`, because
running a `File` implies an exit and would fire the `EXIT` trap at the end of
every region.

Runtime bookkeeping (applying typed writes, probing option state) restores `$?`
afterwards, so `$?` reports the region's own last command.

### Child tasks

`Session.Go` gives the body a child `*Session` with:

- an independent deep copy of `State`;
- an independent shell backend from `ShellRunner.Clone`, so parent and child
  share no mutable backend — with `interp` that is `Runner.Subshell`, which
  applies the shell's own inheritance rules (`ERR` only under `errtrace`,
  `DEBUG`/`RETURN` only under `functrace`);
- the parent's streams, shared by reference;
- its own task group rooted at the task's context.

Backend access is serialized per session: `RunShell`, `Clone` and `Close` never
overlap on one instance. `interp.Runner.Subshell` mutates the runner it copies,
so concurrent launches would otherwise race.

### Launch handshake and arming

`Go` returns once the task has *armed*, which makes launch ordinals — and
therefore the primary failure — deterministic instead of schedule-dependent.
A task arms when the first of these happens:

1. the body returns or panics;
2. the body enters a blocking runtime operation the session owns: `Shell`,
   `Join`, `Close`, `Blocking`;
3. the body calls `Arm()`;
4. the group's context is cancelled.

**Compiler obligation.** A body that blocks on a primitive this runtime does not
own must arm first — emit `child.Arm()` before the block, or wrap it in
`child.Blocking(func() error { ... })`. Rule 4 is the safety net: a body that
violates the obligation stalls its own join, but never wedges the launcher, so
cancellation and shutdown still work.

### Failure, cancellation and join

- A genuine task failure cancels the group at that point, rather than deferring
  it to join time.
- A panic is converted into a `*TaskPanic` failure; the runtime does **not**
  re-panic. Structured cancellation and join still run, and the panicking task's
  own child tasks are still reaped. This matches `interp`'s task runtime, which
  records a panicking task as a failure with status 2.
- A failed `Clone` is that task's failure, reported through the same group
  bookkeeping, and the body never runs.
- `Join` waits for all tasks and reports the **primary failure**: genuine
  failures outrank failures that are only the group's own cancellation, and
  within a rank the lowest launch ordinal wins. So a group where task 2 fails
  while tasks 0 and 1 sit blocked reports task 2, not task 0's `context.Canceled`.
- `Close` = cancel + join + release the backend. Idempotent, safe to defer.
- `Go` after join or close returns an already-failed task (`ErrSessionClosed`)
  and starts no goroutine.
- A task body's own tasks are closed when the body returns, on every exit path,
  so nothing a task launched outlives it.

## Package dependency invariant

`lower/shellrt` imports only the standard library. `lower`'s artifact tests
build generated programs in a temporary module that has a `go.mod` and **no**
`go.sum`; if `shellrt` pulled in `interp`, that build would fail on missing
`go.sum` entries for `golang.org/x/{text,mod,sys,term}`.

Consequence, stated plainly: **no shell backend ships in this package yet.**
`NewSession` installs none, and `Session.Shell` reports `ErrNoShell` until one
is passed with `WithShell`. The interp-backed persistent backend exists and is
exercised in full by `session_test.go` (`interpShell`), but it lives in the test
binary. Shipping it means either a sibling package — `lower/shellrt/shellexec`
is the intended home — or teaching `lower/compile_test.go`'s fixture to produce
a `go.sum`. Both are outside this work package's file ownership.

## Verification

`go test -race ./lower/shellrt/` covers, against a real `interp` backend:

- state persistence across regions: `f() { echo yes; }; arr=(a b); set -u; false`
  followed by a region that sees the function, the array, `nounset` and `$?`;
- array and associative-array round trips in both directions, including values
  with spaces and quotes — no dropped or flattened array values;
- typed writes reaching the shell without clobbering untouched shell state
  (`declare -i` survives; a function survives);
- export projection; cwd persistence in both directions; status projection;
- trap persistence across regions, and child trap inheritance/restoration;
- child shell isolation: a task's variables, functions and trap edits do not
  reach the parent;
- launch-handshake ordering, arming through `Shell` and `Blocking`, and
  cancellation releasing an unarmed launcher;
- deterministic primary failure (repeated runs), genuine-over-cancellation
  ranking, panic-to-failure conversion with sibling cancellation and nested
  reaping, snapshot-failure reporting, `Go`-after-join, and concurrent
  launch/join under the race detector.

## Remaining work

- **Ship the backend.** See *Package dependency invariant*.
- **Compiler glue.** Nothing in `compile.go`/`words.go` emits `Session` calls
  yet; this work package deliberately touches no compiler dispatch. The glue
  needs: session construction in the entry function, `Shell` for identified
  shell regions, `Go`/`Join`/`Close` for concurrency, and the arming obligation
  above.
- **Typed↔shell bridge.** Typed scalars are synced through `State.Vars`; there
  is no representation yet for namerefs, `expand.Object` values, or a typed
  binding aliased to a shell array element.
- **Not modelled:** file descriptors and redirections beyond the session's three
  streams, job control, pipelines between tasks, channels, `set -e` semantics
  for typed statements, and agentic callback scopes. `WithContext` is the seam
  the callback scope will attach to; nothing consumes it yet.
- **Option projection** is read back with a `set +o` probe because the
  interpreter exposes no public getter. It covers `KnownOptions()` only.
