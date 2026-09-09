# Sprint 118: typed invocation results

Story #53, `99bd1de0093b`.

Go-source calls without a spread now evaluate their positioned arguments through
the typed value evaluator. Computed struct results travel as cells instead of
being reduced to their textual source. Arguments are copied in evaluation order;
channel ownership and interface provenance travel with the argument metadata.
The existing method receiver finalization still happens after argument evaluation.

Return statements recognize unshadowed value-producing builtins, including
len/cap, through the builtin engine. Names declared by the original program keep
their ordinary callable behavior. Unnamed parameters now occupy argument slots
but create no lexical binding. Prepared go-launch arguments keep their evaluated
words and discard the original expression trees on the private prepared clone,
so the child does not evaluate the expressions a second time.

A subsequent original-generator failure revealed typed unsigned complement
using unlimited signed precision. Go-source unary complement now uses the
unsigned operand's width, including defined unsigned types. Signed and untyped
integer complement retain their previous semantics; classic Bash++ is unchanged.

Eight native/interpreter/compiled comparisons cover computed struct arguments,
argument order and copies, builtin returns, builtin shadowing, unnamed parameters,
pointer/interface results, unsigned complement and launched argument evaluation.
Compiled artifacts run after their source files have been removed.

The complete unchanged official `test/64bit.go` generator now succeeds in all
three modes. Every mode emits exactly 1,340,126 bytes with SHA-256
`e27493a0460d2ffbfcc43de268cdc756bb3d5a6bc1be8e329bb70bcf41b78552`.
The complete retained generated child has exactly that SHA and succeeds in all
three modes too. Earlier line 103 and line 520 failures remain in raw evidence.

This does not implement typed variadic spread storage, arbitrary method-expression
receivers, or task capture semantics. Nested computed builtin operands also need
the separately reviewed collection evaluator changes in `f8eb4513`; this change
only routes returns through that shared builtin engine. Lowering unnamed receivers
is separate work and is not changed here.
