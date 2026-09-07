# Readonly guard emission

This slice adds the emitter helpers that turn `readonly` marking, rebinding,
container mutation and the collection builtins into ordinary Go guarded by the
runtime's `ReadonlyState`. It adds no runtime code and installs nothing in the
compiler's statement dispatcher: the main-compiler owner wires the callsites.

Readonly is a **runtime** property here, exactly as in the interpreter. Every
public readonly negative must compile as an ordinary Go program and then fail
while running, with the interpreter's diagnostic on stderr and exit status 2. A
compile-time rejection would be a different language.

## Explicit state, explicit boundary

`ReadonlyContext{State, Abort}` carries two expressions:

- `State` evaluates to the `*shellrt.ReadonlyState` of the enclosing program or
  task. It is threaded as an ordinary parameter. There is no package-global
  state variable, no thread local and no goroutine-id lookup, so a task and its
  caller can never disagree about which state they check.
- `Abort` names a `func(error)` that does not return. It belongs to the program
  or task failure boundary, which owns the diagnostic and exit-status contract
  (`ReadonlyError.ExitStatus()` is 2). A boundary that returned would fall
  through into the update it was supposed to prevent.

Both are required; there is no default, because a fallback would reintroduce the
global state this contract forbids. The helpers emit no `rt.` reference of their
own and do not set the bridge flag — the caller declares the state.

## What each helper emits

- `readonlyDeclare` takes the `readonly name...` declaration clause and emits one
  `State.Mark("name", &name)` guard per name. Marking records runtime locations,
  so the address of the binding itself must be passed, never a copy.
- `readonlyRebind` guards a whole-binding rebinding with `State.CheckAssign`,
  which protects the binding location only: rebinding a distinct alias is not
  mutation of the shared object's contents.
- `readonlyMutation` decomposes the positioned target expression once. Every
  index operand is captured into its own temp, in source order, before the
  check; the check and the update then share that single evaluation. Field
  mutation checks `&<path>`, so a public field reached through a pointer alias
  resolves to the marked object rather than to the local binding, and writes
  through the original path. Map and slice mutation capture the parent container
  into one temp and write through it, preserving the deep alias identity the
  runtime records for pointers, shared maps and overlapping slice regions.
- `readonlyBuiltin` captures the target argument once, guards it, and returns the
  builtin call over that temp as an expression, so `append` keeps its value form.

`Kind` (`"field"`, `"map"` or `"slice"`) must come from the compiler's type
information; the emitter cannot tell a map from a slice from an array
syntactically. A value container such as an array must be routed as `"field"`, so
the guard uses its address and the update stays on the original path — writing
through a captured copy would not reach the original.

## Capture order and the value operand

Index operands and the mutated container are captured before the check. The
right-hand side is not, unless the caller supplies `ValueType`: a bare
`temp := <value>` gives an untyped constant its default type and changes
assignability, so `m["k"] = 1` into a `map[string]float64` would stop compiling.
Without `ValueType` the value still appears exactly once, in the update, and is
therefore evaluated after the check — which is also what the interpreter does for
its call-form rebinding. With `ValueType` the value is captured, typed, before
the check. A side-effecting right-hand side is the observable difference between
the two forms, so the compiler should pass `ValueType` wherever it knows the
target's element type.

`append` is guarded conditionally, matching the interpreter: the check runs only
when the new elements fit in the existing capacity, because only then does the
append write into the readonly backing array. A growing append allocates and
mutates nothing, and both implementations allow it. The `args...` form captures
its spread operand too, since its length decides the guard and the same value
must reach the call.

## Verification

`TestReadonlyEmitterNegativeArtifactParity` runs all seven public readonly
negatives — root rebinding, nested map path, slice path, struct field, aliased
deep path, subshell and readonly imported pointer field. Each fixture is parsed
from actual Bash++ source, emitted, compiled to a binary, has its generated
source removed, and is then run with an empty `PATH` — no shell, no Go tool and
no source are reachable. Exact stdout, exact stderr and the exit status are
compared with the interpreter running the same fixture. All seven compile and
fail at runtime with status 2.

`TestReadonlyEmitterBuiltinArtifacts` does the same for `clear`, `delete`, `copy`,
an in-place `append` and a growing `append`. The growing case is compared in
full; the guarded cases compare stdout, status and the diagnostic code only,
because of the wording gap below. Remaining tests pin the emitted shape: single
capture in source order, typed value capture ordering, the spread-append guard,
and the refusals (missing state or boundary, unknown kind, kind disagreeing with
the path, whole-binding target, undefined root, wrong builtin arity, a
non-readonly declaration clause).

The artifact tests use their own small program wrapper and a minimal harness
dispatcher. That wrapper is the test's stand-in for the compiler boundary: the
helpers never fabricate a program and never run the compiler's whole-file pass.
Nothing here certifies the compiler's own statement dispatcher, and the subshell
fixture is exercised as a lexical block — its guard fires before any mutation, so
the block form is observationally identical for that fixture, but real subshell
lowering belongs to the compiler owner.

## Runtime API gaps

1. **Builtin diagnostic wording.** The interpreter prints `cannot mutate readonly
   value "s" through append`, while `CheckMutation` can only produce
   `... through <kind> path <path>`. The helper passes the builtin name as the
   kind and the target's source text as the path, so the diagnostic code and the
   exit status match while the sentence does not. A `CheckBuiltin(name string,
   binding, container any, builtin string) error` entry point would close this.
2. **No owner accessor.** `ReadonlyState.owner` is unexported and `ReadonlyError`
   carries only a preformatted message, so generated code cannot build any other
   wording itself.
3. **No status-carrying failure sink.** `shellrt.Fail` hard-codes status 1 and
   nothing in the runtime consumes `ExitStatus()`, so every caller must supply
   its own abort boundary today.
4. **Synthetic bridge importer.** The compiler's type-check pass models the
   runtime package with a hand-written signature list that does not include
   `ReadonlyState`. Wiring the callsites requires extending it.

Whole-binding rebinding also still arrives as words rather than a typed value
expression, so the compiler owner needs a native value for the rebinding guard's
right-hand side.
