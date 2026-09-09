# GoSource typed nil and interface results

Sprint: #118; Story: #52; Story-ID: d564bada90bb

The unchanged Tour interface-values-with-nil program retained dynamic *T in the
interpreter but printed a nil interface because the dependency decoder ignored
the concrete type on a nil wire value. The error exercise and solution instead
failed when nil return operands entered scalar evaluation. Reproduce all three
against a native Go oracle before repairing their value metadata.

Nil operands now acquire a declared type at parameter/result binding. A nil
interface remains different from an interface holding a nil pointer. Concrete
values returned through an interface retain their dynamic type before the
result's static type is installed. Live pointer parameters derive nil state from
their pointer storage, not their empty textual carrier. Explicit nil conversions
and interface aggregate fields preserve the same metadata. Interface equality
checks dynamic identity, including type aliases, before the represented value.
The predeclared any alias becomes an empty-interface AST by Go object identity;
a user declaration with the same spelling is not mistaken for that alias.

The native dependency decoder honors concrete nilable types and rejects unknown,
non-nilable, or incompatible nil types. It still runs only imported dependencies;
no original program or method body is compiled. Existing unsupported callback
identities remain rejected. This slice does not implement every interface-versus-
concrete comparison or native-handle comparison inside an interface, nor a new
channel/function nil representation. Ambiguous or unsupported identities do not
receive fabricated values.

Validation includes three SHA-bound unchanged Tour originals and eight authored
programs, each through the native Go SDK, Runner, and a real compiled artifact
with both source files removed before execution. Controls exercise typed nil
assertions, nil interface distinction, single/multiple nil returns, pointer and
interface parameters, live pointer state, aliases, nil slices/maps, and nested
interface fields. Runner Reset changes the original type declaration and proves
no stale type/value survives. Separate real-process protocol negatives reject
malformed nil values; those negatives are not product-coverage PASS claims.
Run these with race instrumentation, existing interface/pointer/generic/codec
regressions, full gosource/typed JSON tests, and native Bash confirmation.

Measured results: all 11 programs (33 mode executions), Reset, and three real
protocol negatives pass together with race instrumentation in 69.683s. Existing
interface/pointer/generic/local-codec/Reset regressions pass in 66.240s. Full
gosource passes in 32.627s and typed JSON in 0.176s. Native Bash 5.3.15
confirmation remains FAIL in 13.453s on existing fixture/environment differences;
this is not shell conformance PASS. Raw before/after original results and all
commands are retained in the worker evidence directory.
