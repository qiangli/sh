# GoSource type parameters and dependency registry

Sprint: #118; Story: #52; Story-ID: d564bada90bb

Go type parameters were converted into ordinary named AST types. Existing
interpreter inference and substitution require explicit type-parameter nodes,
which explains the unchanged Tour Index inference and List declaration errors.
Use go/types object identity to distinguish a parameter T from a package type
with the same spelling, retaining original positions and typed JSON behavior.

The dependency registry previously omitted aliases and every declaration inside
a function, rejected the original name entry because of a protocol declaration,
and had no registration for standalone anonymous private structs. Collect the
program's structural type declarations before dependency startup. Preserve Go
aliases with `type A = T`; emit anonymous shapes as aliases so %T and type identity
remain unnamed. Reuse existing typed field codecs for legal private access;
register nested anonymous shapes during recursive type lookup. Rename only the
protocol entry identifier. No original function or method body enters generated
helper source; methods continue calling the owning interpreter.

The runtime does not yet model separate local type scopes. If a spelling denotes
two type declarations, omit both from the helper registry. Valid Go programs
which need those distinct identities must fail before a dependency sees a
conflated type. Two adversarial Runner controls verify this behavior. Further
unsupported generic instantiations or unrepresentable field shapes remain
unregistered instead of being replaced by invented representations.

Verification uses hash-bound, byte-identical Tour Index, List and slice-literals
programs in three modes: real Go SDK oracle, interpreter, and real compiled
artifact after removing original and generated sources. Six authored programs
exercise aliases, entry, local point, anonymous private tagged fields, and
explicit generic calls, and typed byte/rune-alias conversions evaluated once. A typed-JSON control distinguishes ordinary T from
parameter T. Run the nine programs and scope negatives under race instrumentation,
existing generic/collection/type/codec regressions, the entire unchanged tagged
collection audit, and native Bash confirmation. Raw mismatches remain failures;
no program, case, output comparator or capacity expectation is changed.

Typed collection initializers also need the existing conversion path: explicit
`var x []uint8 = []uint8(text())` previously fell into scalar conversion even
though the slice conversion helper already supports byte and rune aliases.
A GoSource-only typed-element hook preserves the helper's payload, target
metadata and errors rather than evaluating the operand a second time.

Measured final results: nine three-mode programs (27 executions) plus two scope
negatives pass under race instrumentation in 123.913s. Existing generic,
collection, local type and private-codec regressions pass in 144.962s, including
stale-handle and cancellation/reset controls. Full gosource passes in 133.893s
and typed JSON in 2.342s. The unchanged 26-row collection audit completes in
165.692s: 22 PASS, four retained FAIL (append growth capacity, conversion passed
to a function whose body returns len, and the two native panic-text cases).
Native Bash 5.3.15 confirmation remains FAIL in 16.190s on the existing fixture
and environment differences; this is not shell conformance PASS.
