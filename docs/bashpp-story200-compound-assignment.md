# Bash++ Story 200 — compound assignment and inc-dec

Sprint 116 · story `0d36792e0026`

Inside a committed Bash++ function, compound assignment and standalone
increment/decrement evaluate their left-hand target once and commit only after
the right-hand expression and typed operation succeed. Identifier, pointer,
slice/array index, struct field, and map-index targets are supported. Map
indices use Go's assignable-but-not-addressable exception, including the zero
value for a missing key.

The interpreted scalar carrier preserves finite booleans, strings, arbitrary
precision integer text, and finite floating-point values. It does not yet
preserve IEEE non-finite values as reusable typed cells across every scalar,
map, and collection path. A runtime floating-point compound division by zero
is therefore rejected at the operator with `BASHPP-EUPDATE-NONFINITE`, leaving
the target unchanged. Integer and constant-invalid division by zero retain the
ordinary `BASHPP-EEXPR-DIVZERO` diagnostic. This is a deliberate fail-closed
compatibility exclusion; this slice does not claim Go's `Inf`/`NaN` result.
