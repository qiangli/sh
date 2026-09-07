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

Every row below was measured against the engine (`interp.Runner` with
`interp.Lang(syntax.LangBashPP)`) or is a recorded corpus expectation. None of
it is inferred from the implementation.

| Root | Kind | Renders | Evidence |
| --- | --- | --- | --- |
| struct / array / slice / map, including named | `KindObject` | JSON | `s=[{"N":1,"T":"x"}]`, `a=[[1,2]]`, `l=[[1,2]]`, `m=[{"a":1}]` |
| nil map, nil slice | `KindObject` | `null` | `m=[null] s=[null]`; corpus `address-deref-new` ends `:null:null` |
| **any pointer, nil or not** | `KindPointer` | **empty** | `&S{}`, `&namedInt`, `new(S)`, `var p *S` all give `p=[]`; corpus `typed-nil-pointer:` |
| nil interface | `KindInterface` | **empty** | `i=[]`; corpus `nil-interface-assert::false` |
| interface holding a named scalar | `KindInterface` | plain | `i=[7]` |
| named int / string / bool / byte / rune | `KindScalar` | plain | `c=7 n=hi f=true`, `b=65 r=66` |
| selected field or index scalar | `KindScalar` | plain | corpus `1:3:5:7:9:10:0:0` |
| result of any imported-package call | `KindObject` | JSON, *including a `string` result* | `bashPPShortDeclImported` calls `expand.NewObject` unconditionally |

Two points are easy to get wrong, and both were corrected by measurement:

- **Pointer roots are empty whether or not they are nil.** This is not
  nil-handling; it is what a pointer root projects. It does not disturb rich nil
  behaviour — a nil map or slice is an *object* root and still renders `null`.
- **Named scalar types project plain**, so the runtime resolves scalars by
  reflect kind rather than a concrete type switch. A type switch over `int`,
  `string`, … silently misses `type Count int`, which the interpreter renders
  as `7`. Only the kind is read; no method on the value is consulted or invoked,
  so a named type carrying `String` or `Error` cannot execute caller code.

Two further rows are the reason shape must not be consulted:

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

## Floats: narrow measured provenance, and fail closed otherwise

The interpreter evaluates untyped constants with `go/constant` over the source
text and prints numerics with `ExactString`. Measured: `x := 1.5` interpolates
`3/2` and `x := 1.1` interpolates `11/10`
(`tests/lowering/profile-additional/literal-float.bpp` records `3/2 3`).

That is the *only* float rendering the engine currently produces. Every other
float path errors there today:

| Source | Observed |
| --- | --- |
| `var a float64 = 1.1` | `"var": executable file not found in $PATH` |
| `float64` struct field, slice element, map value | `BASHPP-ECOLLECTION-ELEMENT: cannot use string value as float64` |
| `a := 1.5; b := a + a` | `BASHPP-EEXPR-OPERAND: operator + not defined on Unknown and Unknown` |
| `var a float64` (zero value) | `0` |

So there is no typed-float rendering to preserve. `projectionFromLiteral`
retains the untyped literal's spelling and `projectValue` emits it as a plain Go
string literal with **no runtime call at all**; a float binding with no retained
text is a compile-time `LOWER-EEXPR` failure, which is the compiler declining
exactly where the engine declines. `projectionFromFloat64` exists only to make
that refusal a named, tested boundary — `1.5` is exactly representable as a
float64, so a lenient `constant.MakeFloat64` would pass the fixture and diverge
on `1.1`.

Go floats in general are *not* all treated as having this provenance — only
values the compiler recorded as untyped constant literals do. Native float
arithmetic remains ordinary native Go.

### Reassignment invalidates retained literals

The exact rational belongs to the *binding*, not to the name. Measured:

```
x := 1.5   →  3/2
x=2.5      →  2.5        (not 5/2)
```

After a shell assignment the variable holds the raw assigned text. A retained
literal that outlived its binding would reprint `3/2` forever, so the emitter
must report assignments:

- `projectionAssign(name, text)` — assignment whose text is statically known.
  The text is stored verbatim, not re-derived through `go/constant`. It updates
  the binding where it lives rather than creating a shadow.
- `projectionInvalidate(name)` — assignment whose text is not statically known.
  Drops the retained text and keeps the kind, so a float binding then fails
  closed instead of reprinting a stale rational, while an int or string binding
  falls back to ordinary runtime projection.

## Runtime behaviour and safety

`shellrt.Project(value, kind)` and `shellrt.ProjectErr(value, kind)` are the
runtime entry points.

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
- An unsupported value is an **explicit failure**, not a marker that would look
  like a successful projection downstream. `ProjectErr` returns the error;
  `Project` reports it through the package's existing `Fail` path, so the
  generated program exits nonzero with a diagnostic on stderr. The compiler is
  expected to have failed closed already; this is the backstop.
- A value carrying `MarshalJSON`, `MarshalText`, `String` or `Error` — including
  a map alias or a struct method value — collapses to `InvalidObject`
  (`<invalid object>`) **without running that method**. That marker is not an
  invention of this package: it is the interpreter's own, byte for byte.
  Refusal happens in a preflight pass, before any marshaling begins, which is
  what keeps caller code from running and keeps a capability from reaching the
  encoder at all.
- Channels and callables are capabilities. They never leak a marshaling handle,
  a pointer, or any other referenceable token; they collapse to the same marker.
  Cyclic and excessively deep graphs do too.

## Emitter integration

`projector`'s zero value is usable, so the seam against `compile.go` /
`words.go` / `callables.go` is **one struct field plus six call sites**. It is
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

   `pr` is one of `projectionFromLiteral(lit)` (constant literal, exact
   spelling retained), `objectProjection()` (rich native root, and *every*
   imported-package call result), `scalarProjection()` (native scalar including
   a named type, or a scalar reached by selector or index),
   `pointerProjection()`, or `interfaceProjection()`.

3b. On a shell assignment to a bound name, report it so a retained literal
   cannot outlive its binding:

   ```go
   e.projections.projectionAssign(name, text) // text statically known
   e.projections.projectionInvalidate(name)   // text not statically known
   ```

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
`lower/shellrt/project_test.go`, check these helpers against **measured** engine
output — each expectation quotes the observed stdout in a comment — and prove
the emitter scope seam holds. They are helper-level checks. No profile case is compiled or executed here, and nothing
in this document claims `go-profile` or `profile-additional` passes end to end.
