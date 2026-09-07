# Method callables under the runtime ABI

A method is lowered exactly the way a free callable is: a private capability
method that takes the caller's program and call site and holds the body, and a
public method that keeps the declared Go signature by opening its own program.
Only the private method performs the marker check, so a compiled type's exported
surface is unchanged and a Go caller that never went through this compiler
reaches the public method and runs with assistance off — which is the answer the
interpreter gives for the same source.

```bash
agentic func (v T) Show(n int) string { echo "$n" }
```

```go
func (v T) __bpp0_call_Show(__bpp0_program *__bpp0_rt.Program, __bpp0_site __bpp0_rt.Site, n int) string { … }
func (v T) Show(n int) string { /* NewProgram, Run, the private call, the results */ }
```

The private name is the spelling a free callable already uses in execution mode,
and it does not depend on the receiver. One spelling across every receiver is
what lets a *private capability interface* — an interface literal naming only
the private method — resolve a marked method through an interface value without
the compiler knowing the dynamic type.

## Resolution

The receiver's static type, and not the method name on its own, selects the
declaration: two types may declare one name with different signatures, and
resolving by name alone lowers a call against the wrong signature. A name
declared on several receivers with no receiver type to choose between them is a
positioned diagnostic rather than a guess.

A concrete receiver emits the direct private selector, so Go's own rules supply
pointer/value auto-addressing and promotion from an embedded field. A receiver
whose static type is a declared interface emits the capability assertion, whose
signature comes from the interface's own method specification — embedded
elements followed — because the specification, not any one implementation, names
the signature every implementation shares. The capability interface states bare
types only: Go rejects a signature that mixes named and unnamed parameters, so a
named source parameter beside the runtime's program and site parameters would
not parse.

An implementation that does not carry the private method is foreign to the
source unit. The assertion fails and the public method runs, with assistance
off. The private branch returns; without that return a zero-result private call
fell through into the public method as well, repeating the method's effects and
asking a second time for a permission that had already been denied.

A method whose name this unit does not declare at all is an ordinary Go call.

## Handles

`f := v.M` captures Go's own method value on the private method. The receiver is
captured once with the receiver's own rule — a copy for a value receiver, the
address of an addressable operand for a pointer one, matching the interpreter —
and the value's type is already the private ABI, so the program and the call
site stay invocation-time arguments. A handle is therefore checked against the
frame it is *called* from, not the frame it was created in. A handle taken
through an interface asserts once at capture and yields either the private
method value or a closure over the captured public one.

## Verification

Every case is built into a real binary, its source removed, and the binary run
with no usable `PATH` and compared with interpretation of the same Bash++
program — exact stdout, exact stderr, exact status. Covered: marked allow and
deny, two receivers sharing a name with different signatures, a pointer receiver
auto-addressing a value, an interface-resolved marker, handle capture by each
receiver's own rule with call-site context, handles keeping each receiver's own
signature, a generic receiver, promotion from an embedded receiver, and
multi-result return through both ABIs. A separate artifact covers the branch no
Bash++ source can express: an interface value whose dynamic type was never
compiled here, falling back to the public method with assistance off.

Wiring the three delegations into the statement dispatcher — declaration, call,
captured value — remains integration work outside this slice.
