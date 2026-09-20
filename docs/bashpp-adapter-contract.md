# Bash# adapter contract

Bash# has one ordered boundary for commands, typed functions, language islands,
and later process adapters:

1. bind and validate declared inputs;
2. convert boundary inputs to the target representation;
3. invoke the Bash or foreign target exactly once;
4. convert successful outputs back to Bash# values;
5. run contracts and append attestation at the outer decorated-call boundary.

The implementation is deliberately not a middleware registry. Typed calls use
the existing callable lowering wrappers; `@decorator` chains use their existing
`Call.Next` continuation; Python/Rust fences use the generated foreign wrapper.
An absent decorator or adapter is identity. A failed phase stops the remaining
phases and preserves the boundary's existing status/error behavior.

The first structured-value consumer is Python: a declared `list[...]` or
`dict[str, ...]` result crosses the foreign wrapper as an Object, while scalar
signatures retain their existing wrappers. The corresponding input conversion
accepts JSON-compatible Objects and refuses non-JSON values. Future streaming,
pipeline, and `(T, error)` adapters extend these phases rather than adding a
second evaluator or invocation path.

Executable gates:

- `go test -tags full ./lower -run TestDecoratedCallableExecution` covers
  deterministic order, identity preservation, and error short-circuiting in
  lowered programs.
- `go test -tags full ./interp -run 'TestBashPPDecorator(TypedNoopPreservation|Trace)'`
  covers the same continuation contract in the interpreter.
- `go test -tags full ./lower -run 'TestPythonFenceInterpretedNativeParity/structured_object_round_trip'`
  covers the first structured foreign-wrapper consumer in both modes.
- `go test ./syntax -run TestBashPPDecoratorDialectIsolation` proves Bash and
  POSIX modes remain inert when Bash# is off.

