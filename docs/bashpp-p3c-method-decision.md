# Bash++ P3-C — basic named-type methods

Sprint 114 · story `b2c7e6409da1` · 2026-09-03

P3-C adds one receiver to a Go-form function declaration: `(r T)` or
`(r *T)`. The receiver base must be a non-alias type declared in the same
runner session. A type has one method namespace, so declaring the same method
with value and pointer receivers is a duplicate.

Named scalar values are constructed with `var v T = value`; `var p *T` is a
nil pointer and an initialized `var p *T = value` is the bounded pointer value
form. The lexical cell stores named-type and pointer identity beside its
ordinary shell value. Consequently `$v` remains the underlying shell string,
while dispatch never serializes the value through JSON or the external P2
toolchain.

Direct selection follows Go's basic method-set rules. `T` has its value
methods; `*T` has value and pointer methods. An addressable variable of type
`T` may call or capture a pointer-receiver method by automatic address-taking,
and a non-nil `*T` may call a value method by automatic dereference. A pointer
receiver is invoked even when its pointer is nil; selecting a value method
through nil is diagnosed. Method values capture their receiver when selected.
Method expressions use `T.M(v, ...)` and `(*T).M(p, ...)`, and deferred method
calls retain the resolved receiver and evaluated arguments.

Local resolution is deterministic: a local typed value or locally declared
type at the selector head is resolved before an import binding. A missing
local method is a local method-set diagnostic, never a fall-through into the
toolchain evaluator.

## Deliberately excluded

Sprint 116 owns struct fields and promotion, embedding, interfaces and dynamic
interface dispatch, and generic receiver/method declarations. P3-C does not
implement any of those. Panic and recover are P3-D and are likewise not part
of this change.
