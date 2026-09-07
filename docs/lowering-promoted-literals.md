# Promoted keys in native struct literals

The helper `emitter.promotedCompositeExpr(literal, declaredTypes)` returns
`(expression, handled, error)`. The compiler supplies its declaration registry.
A false `handled` result leaves positional literals, collections and unresolved
types to the ordinary composite expression dispatcher. This change adds the
helper only; the compiler owner connects the dispatch seam.

Known keyed structs resolve selectors breadth first through ordinary embedded
fields, including instantiated generic structs and aliases. Direct fields win
over deeper promoted fields. Two matches at the same depth are ambiguous.
Repeated keys, unknown keys, mixed keyed/positional elements, pointer traversal
and a whole embedded field conflicting with one of its promoted fields produce
positioned `BASHPP-ESTRUCT-*` diagnostics. Pointer field initialization itself is
still an ordinary direct-field operation; promotion through that pointer is the
rejected form, matching the interpreter.

All keys are validated before values are emitted. The generated expression is a
native Go function literal: it captures each source value once, in source order,
into a temporary with the resolved field type, then constructs nested embedded
literals from those temporaries. Regrouping fields directly would change the
order when promoted keys surround a direct key. Typed capture also preserves
constant representability and native struct/array copying versus slice/map and
pointer aliasing. No input AST nodes are changed.

The declaration registry must reflect the current lexical type scope. Generic
substitution covers named types, pointers, collections, structs and concrete
function types. Broader generic interface field-type substitution and malformed
type-declaration diagnostics remain with the compiler's type machinery.

Tests build ordinary Go artifacts, remove their generated source, and run them
without shell tools. The unchanged public `promoted-literal-keys.bpp` fixture,
generic embedding, generic aliases and direct-field precedence match live
interpretation. An optional `BASHPP_PROMOTED_FIXTURE` path verifies the embedded
public source against an external checkout. Diagnostic cases compare code and
message with the live interpreter and retain the offending source position.
A separate positioned call-node artifact proves source-order single evaluation;
it does not claim that arbitrary call spelling in composite literals is parsed.
