# Value-projection helpers

`lower/projection.go` and `lower/shellrt/project.go` answer one question: when a
typed Bash++ value is used as a shell word, what bytes does the shell see?

The shell itself stays string-typed. The interpreter already fixes the answer —
see `expand/object.go` and `interp/bashpp_scalar.go` — and these helpers render
the same bytes from compiled code. They do not run source at compile time, do
not embed an interpreter, and never render a value to text and reparse that
text to decide what language it was.

## The rule: provenance decides, never shape

Every projection needs a *root kind*, and that kind is a compiler fact recorded
where the binding is created. It is never recovered later by inspecting the
value.

| Root | Kind | Renders |
| --- | --- | --- |
| Native struct / map / slice / pointer | `KindObject` | deterministic JSON |
| Result of any imported-package call | `KindObject` | JSON, *including a `string` result* |
| `nil` rich root | `KindObject` | `null` |
| Native scalar binding | `KindScalar` | plain interpreter text |
| Scalar reached by selector or index | `KindScalar` | plain text, not the root's JSON |

Two of these rows are the reason shape must not be consulted:

- An imported call returning `"incremental"` is bound by
  `interp/bashpp_readonly.go`'s `bashPPShortDeclImported` through
  `expand.NewObject` *unconditionally*, so its root is an object and it renders
  `"incremental"` — with quotes. A native `string` binding holding the identical
  bytes renders `incremental`. Nothing about the value distinguishes them; only
  the binding site does.
- A field selected out of a rich value is a plain scalar even though its parent
  projects as JSON. `h.Item.Second` is `7`, not `{"Second":7}` and not `"7"`.

The corollary is a prohibition: an ordinary string is never relabelled as JSON
because of what it happens to contain. `{"a":1}` stored in a `string` projects
as those five-plus characters, unchanged.

Canonical evidence: `tests/lowering/profile-additional/recursive-substitution.bpp`
in `bashpp-tests` records stdout `{"Item":{"First":"n","Second":7}}` for
`echo "$h"` over a `Holder[int]{Item: Pair[string,int]{First:"n", Second:7}}`.

## Constants keep their source spelling

The interpreter evaluates untyped constants with `go/constant` over the source
text and prints numerics with `ExactString`. So `1.5` prints as the exact
rational `3/2`, and `0x1.8p+1` prints as `3`
(`tests/lowering/profile-additional/literal-float.bpp` records `3/2 3`).

`projectionFromLiteral` retains that spelling from the literal node, and
`projectValue` emits it as a plain Go string literal with **no runtime call at
all**. That is the only path by which the exact spelling can survive into a
compiled program.

`projectionFromFloat64` exists and always fails, with a hint pointing at the
literal. This is deliberate. A `float64` cannot recover the rational: `1.5` is
exactly representable, so a lenient implementation using
`constant.MakeFloat64` would look correct on the fixture and silently print
`1.1` as something other than `11/10`. Floating provenance is retained or the
compile fails; it is never approximated. Correspondingly, a float reaching
`shellrt.Project`'s scalar path returns the visible `UnsupportedScalar` marker
rather than a decimal the interpreter would never print.

Go floats in general are *not* all treated as having this provenance — only
values the compiler recorded as untyped constant literals do. Native float
arithmetic remains ordinary native Go.

## Runtime behaviour and safety

`shellrt.Project(value, kind)` is the runtime entry point.

`shellrt` is linked into every generated program, so it stays on the standard
library — importing `expand` would drag `golang.org/x/text` and `golang.org/x/mod`
into each generated program's module graph, which breaks the existing callable
build tests. `Project`'s object path is therefore a *port* of
`expand/object.go`, not a call into it. The two are held together mechanically
by `TestProjectMatchesInterpreterCoercion`, which diffs `shellrt.Project`
against `expand.ObjectString` over a shared table covering rich roots, nil
roots, tag options, byte slices, unsupported map keys, capabilities, caller
methods, cycles and non-finite floats. That test's `expand` import is test-only
and never reaches a generated program.

- It **only reads**. It assigns through no pointer, writes no map or slice
  element, and calls no caller-defined method. Projecting a readonly binding
  cannot disturb its identity, and repeated projection is byte-identical.
- The object encoding is **deterministic**: `encoding/json` sorts map keys and
  keeps struct field declaration order.
- A value carrying `MarshalJSON`, `MarshalText`, `String` or `Error` — including
  a map alias or a struct method value — collapses to the fixed
  `InvalidObject` marker (`<invalid object>`) **without running that method**.
  Refusal happens in a preflight pass, before any marshaling begins, which is
  what keeps caller code from running and keeps a capability from reaching the
  encoder at all.
- Channels and callables are capabilities. They never leak a marshaling handle,
  a pointer, or any other referenceable token; they collapse to the same marker.
  Cyclic and excessively deep graphs do too.

## Emitter integration

`projector`'s zero value is usable, so the seam against `compile.go` /
`words.go` / `callables.go` is **one struct field plus four call sites**. It is
stated here in full because those files have a different owner; nothing beyond
this is requested of them.

1. In `emitter` (`lower/compile.go`), add exactly one field. Its zero value is
   usable, so `compilePass`'s composite literal needs no change:

   ```go
   projections projector
   ```

2. Wherever the emitter already calls `e.push()` / `e.pop()`, also call
   `e.projections.projectionPush()` / `e.projections.projectionPop()`.

3. Wherever the emitter already calls `e.bind(name)` for a typed value binding,
   also record that binding's provenance — chosen from compiler metadata, never
   from the value's shape:

   ```go
   e.projections.projectionBind(name, pr)
   ```

   `pr` is one of `projectionFromLiteral(lit)` (untyped constant literal, exact
   spelling retained), `objectProjection()` (rich native root, and *every*
   imported-package call result), or `scalarProjection()` (native scalar, or a
   scalar reached by selector or index).

4. At the shell-word boundary (`words.go`'s `parameter` / `stringParts` path),
   replace the raw Go expression with the projection:

   ```go
   text, err := e.projections.projectValue(name, expr)
   if err != nil {
       return "", projectionDiagnostic(node, err)
   }
   ```

   For a retained constant this is a plain quoted literal and emits no runtime
   call, so the only import needed is the `shellrt` one the emitter already
   manages.

`TestProjectorTracksEmitterScopes` drives a real `emitter`'s `push`/`pop`/`bind`
in lockstep with a `projector` and asserts they agree on shadowing and scope
exit, so this seam is proven mechanically rather than only described.
`.agents/handoff-projection.md` carries the same text as the early coordination
note; that path is gitignored, so this document is the durable copy.

`projectValue` returns Go source of type `string`. Errors carry a package
diagnostic code (`LOWER-EUNDEFINED` for an unrecorded name, `LOWER-EEXPR`
otherwise); `projectionDiagnostic(node, err)` attaches the position the emitter
already has. An unrecorded name is an error, not a guess.

## Limits

`TestProjection*`, `TestProjectValue*` and `TestProjectorTracksEmitterScopes` in
`lower/projection_test.go`, and `TestProject*` in
`lower/shellrt/project_test.go`, check these helpers directly against recorded
interpreter output and prove the emitter scope seam holds. They are
helper-level checks. No profile case is compiled or executed here, and nothing
in this document claims `go-profile` or `profile-additional` passes end to end.
