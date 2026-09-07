# Readonly guard emission

This slice adds the emitter helpers that turn `readonly` marking, rebinding,
container mutation and the collection builtins into ordinary Go guarded by the
runtime's `ReadonlyState`, plus the two runtime entry points they need. It
installs nothing in the compiler's statement dispatcher: the main-compiler owner
wires the callsites.

Readonly is a **runtime** property here, exactly as in the interpreter. Every
public readonly negative must compile as an ordinary Go program and then fail
while running, with the interpreter's diagnostic on stderr and exit status 2. A
compile-time rejection would be a different language.

## Explicit state, typed unwind

`ReadonlyContext{State}` carries one expression: the `*shellrt.ReadonlyState` of
the enclosing program or task, threaded as an ordinary parameter —
`program.Readonly` for a program built by the runtime's `Program` helper. There
is no package-global state, no thread local and no goroutine-id lookup, so a
task and its caller can never disagree about which state they check. It is
required; a default would reintroduce the global this contract forbids.

A failing guard calls `shellrt.MustReadonly`, which panics with the guard's own
typed error. That is not a failure sink: nothing is recorded, deferred code
runs, and the program boundary catches the unwind, reports the diagnostic once
and exits with the error's own status (`ReadonlyError.ExitStatus()` is 2). The
runtime's `Program.Run` recognizes any panicked error carrying `ExitStatus() int`,
so no boundary edit is needed for these guards.

## What each helper emits

- `readonlyDeclare` takes the `readonly name...` declaration clause and emits one
  `State.Mark("name", &name)` guard per name. Marking records runtime locations,
  so the address of the binding itself must be passed, never a copy.
- `readonlyRebind` guards a whole-binding rebinding with `State.CheckAssign`,
  which protects the binding location only: rebinding a distinct alias is not
  mutation of the shared object's contents.
- `readonlyMutation` emits `State.CheckMutation` over the root binding's address
  and the root binding's own value, then the ordinary Go update.
- `readonlyBuiltin` captures the target argument, captures the operands that need
  it, guards with `State.CheckBuiltin`, and returns the builtin call as an
  expression so `append` keeps its value form.

`Kind` (`"field"`, `"map"` or `"slice"`) is the interpreter's diagnostic
vocabulary and must come from the compiler's type information: `"field"` when
the final path element is a field, `"map"` when the parent container is a map,
and `"slice"` for every other indexed parent — an array included, which is what
the interpreter prints for `var a [3]int; readonly a; a[1] = 5`. A field
diagnostic names only the final selector (`cfg.Meta.Name` reports `.Name`) while
an indexed one names the whole path (`.Ports[0]`, `["nested"]["port"]`).

## Guard timing, measured against the interpreter

The guard resolves the root binding's address and the root binding's *value*, so
it needs no part of the path evaluated. That is not a convenience: it is what
the interpreter does. Measured on the live interpreter,

| fixture | interpreter |
| --- | --- |
| `cfg.Ports[5] = 1`, readonly `cfg`, length 1 | readonly diagnostic, not `BASHPP-ECOLLECTION-BOUNDS` |
| `m["a"] = 1`, readonly nil map `m` | readonly diagnostic, not `BASHPP-ENIL-MAP` |
| the same statements without `readonly` | the bounds and nil-map diagnostics |

so a lowering that evaluated the path first would report the wrong failure. The
mutation update therefore stays one ordinary Go statement: every operand appears
once and is evaluated once, in Go's own left-to-right order, after the guard —
which is also where the interpreter evaluates the right-hand side of a
collection or selector assignment. Because the root binding's value carries the
identity, a map, slice or pointer alias still resolves to the marked owner
(`alias := s; alias[0] = 9` reports `through alias "alias" and path [0]`), and an
overlapping subslice resolves through the region the runtime recorded.

Builtins are the other way around, and again this is measured: `delete(m, 1)` on
a readonly map reports the readonly failure rather than the key's type error, so
the interpreter evaluates a builtin's arguments before it checks the target.
`readonlyBuiltin` therefore captures the target and then the operands *before*
the guard. An operand is left in the call only when it is pure — a literal, an
identifier, a package-qualified constant, or parens/unary/binary over those —
because such an operand cannot have a side effect or panic, so its position
relative to the guard is unobservable, and hoisting it would be actively wrong:
a bare temp gives an untyped constant its default type, so `append(s, 1)` into a
`[]float64` would stop compiling. When the compiler knows the parameter's type it
passes `ArgTypes`, and the operand is captured with that explicit type instead.

`append` is guarded conditionally, matching the interpreter: the check runs only
when the new elements fit in the existing capacity, because only then does the
append write into the readonly backing array. A growing append allocates and
mutates nothing, and both implementations allow it. The `args...` form uses the
spread operand's runtime length.

## Verification

`TestReadonlyEmitterNegativeArtifactParity` runs all seven public readonly
negatives — root rebinding, nested map path, slice path, struct field, aliased
deep path, subshell and readonly imported pointer field.
`TestReadonlyEmitterGuardPrecedesPathEvaluation` adds the timing and wording
cases above, and `TestReadonlyEmitterBuiltinArtifactParity` covers `clear`,
`delete`, `delete` through an alias, `copy`, an in-place `append`, an in-place
spread `append`, and both growing appends.

Every one of those fixtures is parsed from actual Bash++ source, emitted,
compiled to a binary, has its generated source removed, and is then run with an
empty `PATH` — no shell, no Go tool and no source are reachable. **Exact stdout,
exact stderr and the exit status are compared with the interpreter running the
same fixture**, wording included. The remaining tests pin the emitted shape: no
hoisting in a mutation, operand capture before a builtin guard, typed capture
ordering, the spread-append guard, the purity classifier, and the refusals
(missing state, unknown kind, kind disagreeing with the path, whole-binding
target, undefined root, wrong builtin arity, more argument types than arguments,
a non-readonly declaration clause). Runtime tests cover `CheckBuiltin`'s owner
and alias wording, its slice-region resolution, and that `MustReadonly` unwinds
with the typed error, runs deferred code and carries status 2.

The artifact tests use their own small program wrapper and a minimal harness
dispatcher. The wrapper's boundary is a stand-in for the runtime `Program`
helper's `Run`, which catches the same typed unwind; the helpers never fabricate
a program and never run the compiler's whole-file pass. Nothing here certifies
the compiler's own statement dispatcher, and the subshell fixture is exercised as
a lexical block — its guard fires before any mutation, so the block form is
observationally identical for that fixture, but real subshell lowering belongs to
the compiler owner.

## Known divergences

- **Execution continues after a violation in the interpreter.** A readonly
  failure sets status 2 but the interpreter runs the following statements, while
  a compiled artifact unwinds to the boundary. The certified fixtures therefore
  end at the violating statement, which is also the shape of every public
  negative. Reconciling the two is a language decision, not a guard detail.
- **Struct copies.** `alias := cfg` copies the struct in Go, so `alias.Name = x`
  is an ordinary mutation of a distinct object, while the interpreter still
  reports an alias violation. The runtime is address-based and cannot infer this;
  remapping a clone's marks is the compiler obligation the runtime's `Program`
  documentation already records.
- **Pointer-rooted deref assignment.** The interpreter has a separate path with
  its own `through pointer` wording that evaluates the right-hand side before the
  check. These helpers do not cover it; routing it is the compiler owner's.

## Runtime API status

`CheckBuiltin` and `MustReadonly` were added to `lower/shellrt/readonly.go` by
this slice, which closes the builtin wording gap and removes the need for a
per-callsite abort boundary. No owner accessor is needed: the diagnostics the
generated code must produce are all built by the runtime itself. The compiler's
type-check pass now embeds the real runtime declarations, so nothing has to be
added to a synthetic signature list for these entry points.

Whole-binding rebinding still arrives as words rather than a typed value
expression, so the compiler owner needs a native value for the rebinding guard's
right-hand side.
