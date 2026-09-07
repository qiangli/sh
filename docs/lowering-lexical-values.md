# Checked lexical values

The lexical value postpass runs after native storage and registration, before
final Go checking. It resolves runtime Cell/Register calls and local variable
objects using go/types. A same-spelled field or shadowed declaration cannot
select another binding's storage.

Scalar typed reads use checked Load or LoadAddress. Typed printing observes
PrintValue, preserving raw text and wider integer observations. Shell word
projection reads the shell spelling. Generated absent-binding observations
remain lazy, so a canceled receive cannot be replaced by an undefined-value
error while printing its empty binding.

Scalar binary operations retain operands before narrowing their result. Thus a
raw int8 value of 128 compares greater than zero and divides by two to 64.
Floating results can retain their exact constant alongside the native value
through a short declaration and subsequent shell projection. Operators preserve
the current source engine's diagnostics, including a fractional integer result
rejected during conversion.

A failed new scalar short declaration leaves its registration absent and reports
once while later statements continue. Source callable frames compare a shared
short-declaration failure sequence after their defers. This restores status 2
without making ordinary command failures sticky. Task children and separate
entries have independent sequences. Callable result-presence propagation is a
separate compiler obligation.

A successful native scalar write clears raw spelling even when the numeric
value compares equal. Pointer assignments capture the address once before the
right operand and notify the binding index after committing. Rich native values
retain their existing addressability and identity.

The acceptance tests build actual compiler output with this postpass, remove the
generated source, and compare execution with the same interpreted source. A
separate compiled entry runs concurrent independent instances under the race
detector. Dispatcher installation remains an explicit compiler integration step.

This slice does not complete compound-update or tuple alias notifications,
rich shell mutation, callable token projection, every scalar conversion context,
generic type-parameter scalar reads, or result-presence propagation. Backend declaration-attribute and unset policy
remain at their existing shell boundary.
