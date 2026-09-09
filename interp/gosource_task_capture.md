# GoSource lexical capture identity across tasks

Sprint: #118 · Story: #52 · Story-ID: d564bada90bb

## The problem

An original Go closure captures its free variables **by reference**: a goroutine
and its parent name one variable. A Bash++ task snapshot deep copies every
mutable value reachable from the shell environment, which is right for classic
Bash++ — `go f()` there is a shell construct and a task is a private copy of the
shell — and wrong for a GoSource program. `bashpp_task.go` already gave an
imported `sync.Mutex` or `atomic.Int64` one identity across the boundary, but
the plain interpreted cell the mutex *protects* was still copied, so a correctly
synchronized Go program printed `counter 0` where Go prints `counter 8`.

## The rule

Identity is granted to exactly the cells a launched task's closure captures
lexically, and to nothing else.

- **Only in GoSource mode.** `Runner.bashPPGoSourceTaskCapture` returns a nil
  set otherwise and every hook is a no-op on nil, so the classic Bash++
  deep-copy snapshot is byte-for-byte unchanged.
- **Only free variables**, computed by the lexical scope walker in
  `gosource_task_scope.go`. A parameter, a `:=`, a `var`, a range or select
  binding, or a nested closure's parameter is a *different* variable from an
  outer one that shares its spelling, and the outer cell is not shared.
- **Only plain interpreted cells.** Native handles keep the reviewed
  descriptor-copy rule in `bashpp_task.go`; channels keep their own identity
  machinery. Neither is re-decided here.

## Why not over-approximate

The first revision collected every `syntax.Lit` in the body and argued a
superset was safe. It is not. Sharing is not a no-op on a cell the task never
touches: it *removes the deep copy*, splicing the parent's live cell into a
concurrently running task's environment where an alias, a pointer target or a
captured closure scope can reach it. Concretely, that revision would have shared
an unrelated outer `counter` because the body contained `fmt.Println("counter")`,
and would have shared an outer `x` the body's own `x := …` shadows and can never
name. It also used an ASCII-only identifier predicate, so `计数器` was silently
dropped from the capture set and its program kept printing the stale parent
value.

The walker therefore reports **exactness**. A construct it does not model sets
`exact=false` and the capture set is discarded wholesale: the task falls back to
the classic deep-copy snapshot. That direction loses Go's by-reference capture
for one launch — a visible, testable wrong answer — rather than aliasing a cell
the program never named, which is an invisible one.

## Resolving the launched callee once

Go evaluates a function value in the launching goroutine. So do we, once, in the
parent, before the snapshot:

- a `func` literal in callee position needs no resolution;
- a declared `func` and a closure held in a variable are pure lookups;
- a **computed callee** (`go factory()()`) is evaluated exactly once, and the
  resulting closure handle is pinned on the child.

`bashPPGoSourcePin` carries the closure **handle**, not the parent's
`*bashPPFunc`, so the child resolves it through its own cloned closure registry:
the same function, with the child's copy of everything the snapshot legitimately
copied. `Runner.bashPPLookupFunc` consults the pin first, which is what prevents
a second evaluation of a computed callee and prevents the child from re-reading
a variable that may hold a different function by then.

## Deciding a cell once

Whether a cell may be shared is decided the **first** time a launch names it,
and remembered. After a cell is shared, the parent is no longer its only owner,
so re-inspecting its payload on a later launch reads a value a running task is
concurrently writing — a data race in the interpreter itself, which `go test
-race` reports on any loop that launches the same closure twice. Deciding once
is also the correct answer and not merely the safe one: what is classified is
the variable's *type* — plain value, channel, or imported native handle — and a
Go variable's type is fixed at its declaration.

## Race safety

A shared cell is shared exactly as a Go variable is, which makes synchronization
the program's responsibility, as in Go. Every native `mu.Lock()`, `wg.Wait()`
and `atomic.Add` is a request serializing on the dependency session's own Go
mutexes, so a program that synchronizes the way Go requires also establishes the
happens-before edges the race detector checks. A program that does not
synchronize races here because it races in Go too.

## API surface (new, isolated)

Owned by this story; nothing else in `interp` is expected to call it.

`gosource_task_capture.go`

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `bashPPGoSourcePin` | `struct{ call *syntax.BashPPCall; handle string }` | the callee a task resolved in its parent |
| `(*Runner).bashPPGoSourceTaskCapture` | `(*syntax.BashPPCall) (map[*bashPPCell]bool, *bashPPGoSourcePin)` | the cells to share, and the pin to install |
| `(*Runner).bashPPGoSourceTaskBody` | `(*syntax.BashPPCall) (*syntax.Block, map[string]bool, *bashPPScope, *bashPPGoSourcePin)` | launched body, signature bindings, lexical env |
| `(*Runner).bashPPGoSourceTaskFunc` | `(*syntax.BashPPCall) (*bashPPFunc, *bashPPGoSourcePin)` | resolve the callee once |
| `bashPPGoSourceSharableCell` | `(*bashPPCell) bool` | plain interpreted cell only |

`gosource_task_scope.go`

| Symbol | Signature | Purpose |
| --- | --- | --- |
| `bashPPGoSourceFreeNames` | `(*syntax.Block, map[string]bool) (map[string]bool, bool)` | free variables + exactness |
| `bashPPGoSourceScope` | struct | the frame stack; not used outside this file |

Hooks into code owned elsewhere, kept to the minimum:

- `interp/api.go` — two `Runner` fields (`bashPPGoSourceCapture`,
  `bashPPGoSourcePin`) and one line handing the shared set to the scope cloner.
- `interp/bashpp_scope.go` — `bashPPCloner.shared`, aliasing a shared cell
  instead of copying it, memoized so every edge lands on one cell.
- `interp/bashpp_concurrency.go` — `cloneBashPPTaskCells` skips a shared cell
  rather than rewriting the parent's payload in place; `bashPPTaskSnapshot`
  scopes the set to one `subshell` call; `bashPPGo` resolves capture and pin.
- `interp/bashpp_func.go` — a four-line pin check at the top of
  `bashPPLookupFunc` (coordinate with the `func.go` owner before moving it).

## Controls

- `gosource_task_capture_frontend_test.go` — capture-set precision measured on
  the representation `gosource.Parse` actually produces, for shadows, Unicode
  identifiers, string-literal text, selector roots, struct field keys, local
  slice/map, explicit args, nested closures and captured pointers.
- `gosource_task_capture_e2e_test.go` — the same programs as **real original Go**
  in all three modes (Go toolchain oracle, interpreter, lowered-and-compiled),
  plus a classic Bash++ deep-copy control. The original source is verified
  unchanged; nothing is rewritten and no native body is forwarded.

  These programs synchronize with **channels**, not `sync.Mutex`/`WaitGroup`.
  The snapshot in this lineage has no rule for carrying an imported native
  handle across the task boundary — a sync-synchronized program fails closed
  with `unsupported mutable Bash++ object type *interp.bashPPBridgeValue`
  before capture is consulted at all. That rule is a sibling story's work and is
  not here. `TestGoSourceCaptureNativeHandlePending` pins that boundary as an
  executable fact, so the day the rule lands the test fails and the sync-shaped
  program moves up into the three-mode table.
- `gosource_task_capture_internal_test.go` — the cloner and task-walk hooks, the
  native-handle and channel exclusions, and the fail-closed contract.
