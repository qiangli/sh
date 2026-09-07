# Lowering typed constants across the shell boundary

A source `const` lowers to a real Go `const`. It is never demoted to a `var`,
because the refusal has to be a property of the lowering rather than a
convention: a constant with no storage cannot be written by a path that forgot
to check, while a `var` carrying a "please don't write me" flag can.

That leaves two jobs, both handled here.

## 1. A shell assignment to a constant is not a native write

`nativeScalarShellAssignment` claims plain scalar assignments and lowers them
as native Go writes. For

```
func main() {
const x int = 1
x=42
echo "shell=$x status=$?"
println(x)
}
main()
```

that produced `x = 42` against a Go constant, and `Compile` rejected the whole
program: `LOWER-ETYPE: cannot assign to x (neither addressable nor a map index
expression)`. The engine does not reject this program — it runs `x=42`, refuses
it with `x: cannot assign to const` at status 1, and stops there, so stdout is
empty and the run ends at 1.

So the assignment stays a shell statement and the declaration policy, not the
Go type checker, decides its outcome. `nativeScalarShellAssignment` declines
any target `lexicalConstantAssigned` reports as constant, and the statement
falls through to an ordinary shell region.

`lexicalConstantAssigned` reads the emitter's own scoped projection metadata:

```go
p, ok := e.projections.projectionLookup(name)
return ok && p.constant
```

`projection.constant` is set when a `const` BashPPDecl or ConstGroup spec is
lowered. Reusing the projector is not just economy — it already models
shadowing, scope push/pop and captured function views, so an inner `var x` is
native storage again and the constant returns when that scope pops. A second
table keyed by name would have to re-derive all of that, and one keyed by
emitter pointer would leak across concurrent compiles.

## 2. The constant needs a shadow at the boundary

`shellrt.RegisterConstant` installs a *shadow slot*: runtime-owned storage
holding a copy of the constant, marked present, with
`LexicalInfo{Constant: true, Readonly: true}` and the source type spelling.
The generated program keeps reading the real Go constant; the shadow exists so
that a shell region can project `$x`, and so that the boundary knows which
identity is constant.

Three properties matter:

- **Nothing takes the constant's address.** `&x` does not compile for a
  constant, and the registration passes the value.
- **The shadow is not in the address registry.** No pointer can alias it, so no
  native write can arrive through one.
- **A repeated registration republishes the name.** A declaration re-executed
  in a loop or a re-entered callable reaches an identity the store already
  holds. If the spelling was rebound in between, returning early would leave
  the constant in the store but pointing nowhere, and the boundary would
  silently stop projecting it.
- **Registration is keyed by resolved identity** — the same
  `local:<offset>:<name>` key `lexicalLocals` mints — not by spelling, so a
  constant shadowing a variable of the same name is a distinct binding, and
  re-registering one identity is idempotent. Idempotence is required rather
  than merely tidy: the engine refuses a second declaration of a name with
  `x redeclared in this block` (status 2), so a constant that stays in scope
  across several regions must be declared once.

`LexicalBindings.Constants` reports that metadata in name order. It is what a
backend declaration policy is installed from, so the engine keeps ownership of
the refusal wording and of the per-statement control transfer:

| region       | stderr                                        | status |
|--------------|-----------------------------------------------|--------|
| `x=42`       | `x: cannot assign to const`                   | 1, statement abandoned |
| `unset 'x'`  | `unset: x: cannot unset: readonly variable`   | 1, execution continues |

Those two lines are measured, not invented here; reusing them is the reason the
shadow carries metadata instead of reimplementing the diagnostics.

## Installing the postpass

`lexicalConstants` is a `[]lexicalEdit` producer shaped like `lexicalLocals`,
so `lexicalStorage` installs it with one line after storage registration:

```go
edits = append(edits, e.lexicalConstants(file, fs, info)...)
```

It descends into declarations that take no program parameter of their own: the
entry and the source main hold the root body as a literal passed to
`Program.Run`, and that literal's parameter is the only context a top-level
source constant ever has.
