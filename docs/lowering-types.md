# Native type and composite emitter helpers

`lower/types.go` provides focused emitters over the positioned Bash++ type and
expression nodes. These helpers extend the compiler foundation; accepting a
program still requires the statement and expression dispatcher to call them.
They do not embed an interpreter, run source initializers, resolve shell text,
or implement an alternative compiler frontend.

The helpers preserve Go representations for arrays, slices, maps, structs,
pointers, named types and aliases. Consequently array and struct assignment
copies values; slice, map and pointer assignment preserves references. Struct
embedding and interface method sets use Go's checker and native dispatch.
Type assertions retain their native comma-ok capability when their containing
assignment supplies two targets. Single-result assertion failure needs the
language's panic/diagnostic boundary before claiming failure-path parity.

Generic declarations retain parameters, constraint interfaces, approximation
terms, unions and explicit type arguments. Go checks method sets, comparability,
constraint satisfaction, type inference and constant representability on the
emitted program. Interface elements are emitted in their original order;
`Methods` is treated as a compatibility projection when `Elems` is present,
so methods are never accidentally duplicated. Interface parameter/result names
do not create local bindings in the enclosing source scope.

Nested inferred composite literals preserve omitted type spelling. Identifier
keys in composites are emitted without resolving them as local variables:
Go resolves struct field keys and checks map key expressions using the actual
composite type. This also handles named and aliased map types without guessing
from their spelling.

## Integration hooks

All methods return `(string, error)`:

- `typeExpr(syntax.BashPPTypeExpr)` emits the closed positioned type vocabulary.
- `typeDecl(*syntax.BashPPDecl)` emits named definitions and aliases. Top-level
  types must be emitted at package scope for receiver methods to refer to them.
- `fieldType(*syntax.BashPPField)` reads the structured type first, with a
  validated compatibility fallback for already classified legacy type fields.
- `typeParams([]*syntax.BashPPTypeParam)` and
  `typeArgs([]*syntax.BashPPTypeArg)` include their brackets when nonempty.
- `compositeExpr(*syntax.BashPPCompositeLit)`,
  `selectorExpr(*syntax.BashPPSelectorExpr)`, and
  `typeAssertExpr(*syntax.BashPPTypeAssertExpr)` emit value expressions.

`nativeBuiltin` identifies predeclared operations for the callable dispatcher.
The dispatcher must lower make/new type arguments, spreads and ordinary value
arguments correctly. This identifier predicate alone does not implement calls.
Printing needs its existing language-specific stream and formatting adapter.

The dispatcher must predeclare type names for conversions, use the rich type
helpers in declarations and signatures, and support indexing, slicing,
address-taking and dereference in its expression recursion. Missing types remain
positioned errors. The callable slice decides the language convention for an
untyped parameter and infers a bare `func` result from its returned callable;
those are not valid ordinary struct/interface field types.

## Verification and limits

`TestTypeHelpersArtifactParity` parses real Bash++ source, emits its type
declarations and composite initializer expressions, and builds a native binary.
Its remaining Go-compatible function body is shared verbatim between the
interpreter and artifact fixture. The binary runs with the emitted source
removed and no executable tools on PATH. Streams and successful status must
match the interpreter. These are helper integration tests; they intentionally
do not claim that `Compile` already dispatches every node used in the fixture.

Cases cover array copies, slice/map aliases, pointer embedding and method sets,
interface assertions, generic named slices and aliases, nested composite values,
slicing and nil comparisons, and applicable collection builtins. Additional
static tests reject incompatible defined types, invalid generic constraints,
noncomparable map keys and ambiguous promoted fields. Generic function tests
check union constraints, inference and retention of defined argument types.

Bash# null and deep readonly semantics still require their own lowering.
For example, printing a nil collection through a shell-facing word boundary
must use the language's `null` spelling; Go's ordinary formatter is insufficient.
Native maps also have unspecified iteration order, while some interpreter
observations are ordered. Exact map-iteration observations need the established
language contract before a native range loop can be certified.

The helper tests avoid claiming equivalence for interpreter forms that currently
produce different observations, including direct selector text in a return word,
some numeric map-key observations, and a constrained generic-alias substitution.
Those require explicit regression cases and resolution in the compiler/runtime
integration work. Success here establishes the emitted type representation and
its tested value semantics, not whole-language or failure-diagnostic parity.
