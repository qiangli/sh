# Bash# static checks and runtime guard helpers

This slice supplies compiler helpers for defaults, named arguments, enums,
null-flow diagnostics, and runtime deep readonly guards. It does not by itself
wire the compiler dispatcher or establish compiled parity for the public
Bash# corpus.

## Static compiler boundary

Call `CheckBashSharp(file)` on the original positioned syntax tree before
emission. Failures are `ErrorList` diagnostics retaining source positions and
public `BASHPP-E...` codes. The CLI should render `Code + ": " + Msg` on stderr
and exit 2 for these static failures; the compiler must return no artifact.
It must not prepend a generic `LOWER-E...` diagnostic to the public message.

The checker validates default ordering; duplicate, unknown and multiply bound
named arguments; missing required arguments and excessive positional arguments;
enum member validity, duplication, literal conversion membership and parameter-
typed enum switch exhaustiveness. It traverses nested switches. The enum
emitter produces an ordinary named integer type and constants. The callable
emitter must account for an exhaustive enum switch when Go's general return
analysis still requires a terminal statement. Runtime enum conversions with
nonconstant values still require membership guards.

The null analysis consumes positioned typed expressions, conditions, assignments
and function signatures. Legacy return and argument fields are still `Word`
objects; a small expression adapter decodes those already classified typed
fields. It never reparses an entire mixed source file as a Go program, and
ordinary shell statements do not turn the checker off. It tracks nullable
parameters and explicit declarations, nil comparisons, short-circuit guards,
terminating branches, aliases and reassignment that invalidates narrowing.

This is a bounded flow checker, not a complete typed-IR proof. Closure capture,
interprocedural mutation, arbitrary shell writes, all loop exit paths and alias
refinement need further integration. Static acceptance here must not be used as
proof that an uninstrumented runtime dereference is safe. Shell-to-typed writes
must invalidate affected narrowing facts when that boundary is implemented.

## Ordered defaults and named arguments

`planSharpCall(call, function)` returns a `sharpCallPlan`:

- `Words` lists every supplied value in original source order, followed by
  omitted defaults in parameter order.
- `ParameterOrder` maps the unchanged native parameter list to those values.

The caller must evaluate each word exactly once into a typed temporary, in
`Words` order and in the caller's scope. It then calls the ordinary native
function with the temporaries reordered by `ParameterOrder`. Reordering the
original expressions directly is incorrect because it changes side effects.
Evaluating a default inside the callee would likewise change its lookup scope.
`defer` and task launch must capture these already evaluated argument values at
their scheduling boundary, before applying the callable's body scope rules.

Invoke this binding contract only for signatures with defaults or calls with
names. Ordinary Go-form arity diagnostics belong to the existing callable
checker. Function values, receivers, variadics, callable metadata and generic
instantiations must carry sufficient signature information before named/default
binding can be applied to them; these helpers do not invent that metadata.

## Deep readonly runtime boundary

`lower/shellrt.ReadonlyState` provides:

- `Mark(name, bindingAddress)` records a binding and reachable object identities.
- `CheckAssign(bindingAddress)` checks rebinding of the marked root location.
- `CheckMutation(name, bindingAddress, container, sourcePath, kind)` checks the
  actual map, slice, pointer, or struct/array address being mutated.

Generated code must evaluate target/index operands exactly once, check before
the store or mutating builtin, then perform the operation only on success.
The guard returns a `ReadonlyError` with the public diagnostic and
`ExitStatus() == 2`. The runtime entry boundary must emit that message once,
set status 2 and preserve the language's continuation/termination behavior.
The seven public readonly negative cases must emit compilable artifacts and
fail when run; rejecting them during transpilation is the wrong phase.

Identity is recorded at runtime, so an alias created before `readonly` remains
protected. The registry follows maps, slices, pointers, interfaces, structs and
arrays, retains roots, detects cycles and recognizes overlapping slice views.
A different object with equal contents remains mutable. A copied struct's
independent fields and its shared nested references retain Go's distinct copy
and alias semantics. The current registry protects a marked root's rebinding;
rebinding a separate alias does not mutate the old referenced object.

Each program/session needs its own registry. The type is safe for concurrent
registry access, but locking the registry does not synchronize unrelated native
mutations. Subshell/task snapshots that clone native values must remap readonly
identity to the cloned objects and preserve original owner names. Sharing a
registry with cloned addresses cannot establish that behavior. No snapshot
integration is supplied here. Zero-size slice-element identity and pointer views
with changing named type representations also require explicit integration
review before broad claims.

## Verification

Standalone tests cover exact static diagnostics and positions, mixed-source
null checking, guard narrowing, reassignment, and named/default binding order.
Runtime unit tests cover preexisting aliases, deep containers, imported pointer
values, overlapping subslices, pointer-to-field aliases, cycles, independent
objects and the exact runtime error status.

With `BASHSHARP_CORPUS` pointing to the public `tests/bashsharp` directory,
`TestBashSharpPublicCorpus` checks all 33 lowering rows: 15 static rejections
with exact diagnostics and 18 static acceptances, including the seven readonly
cases that must remain runtime failures. This test is a phase contract check,
not compiled artifact execution. The parser must first accept nested typed calls
in conditions and concrete function-typed parameters; a parser failure remains
a real test failure and is not rewritten away.
