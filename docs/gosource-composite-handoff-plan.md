# Typed composite handoffs

Keep the existing cell/bridge model and repair each reproduced boundary:

- Collection call results must retain pointer payload and type metadata.
- Imported composite addresses assigned to imported interface elements use
  native typed assignment, including the interface's dynamic value metadata.
- Addressable imported interface receivers read the interface; they do not
  acquire an extra pointer layer. Native scalar results use returned cells
  when an imported signature has no locally parsed declaration.
- Generic interface substitution retains declaration tokens; use that origin
  for unexported-method package identity instead of requiring the copied
  interface node to equal its original declaration node.
- A structured assignment error ends evaluation. Preserve its wrapped control
  flow error rather than evaluating the failed operand again as a scalar.

Validate pointer identity, operand order/count, panic-before-commit, imported
interface method/assertion behavior, named native results, generic mapped
packages and rejection of another package's private method. Invalid pointer
keys, map element addresses, and interface conversions remain checker errors.
Classic evaluation remains covered by existing focused regression tests.

The ABI map corpus case also calls runtime.SetFinalizer with an original
pointer callback. Its existing explicit lifetime-policy refusal remains;
repairing pointer key transport does not establish that root as passing.
No heap-lifetime redesign or finalizer substitution belongs to this change.
