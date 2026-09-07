# Bash++ Story 204 — initialization and assignment

Sprint 116 · story `97322d0e5504`

Inside a committed Bash++ function, identifier-list `=` evaluates every scalar
right-hand expression before validating or mutating any left-hand binding.
Result-bearing declared function calls use the same validate-then-commit path.
Arity, undeclared targets, readonly targets, and assignability failures leave
all target bindings unchanged. Blank identifiers discard their corresponding
values. Compound assignment and increment/decrement are owned by the
expression/control-flow story, not this slice.

The file runtime cannot implement Go's package dependency reordering without
also moving observable shell commands. It therefore preserves source order and
rejects directly referenced forward or self-dependent top-level initializers
with positioned `BASHPP-EINIT-ORDER`. Go `init` functions are likewise rejected with positioned
`BASHPP-EINIT-FUNC`; silently declaring one without automatically invoking it
would give incorrect program-initialization semantics.

Unused-variable errors remain a standing exclusion for the interpreted mixed
shell runtime. A binding can be read dynamically by shell `eval`, indirect
parameter expansion, sourced code, traps, or an external command. Lexical use
analysis would therefore reject valid programs, while runtime read tracking
would disagree with Go's compile-time rule. No diagnostic is claimed until a
closed-world compiled region can prove that those dynamic read paths are absent.
