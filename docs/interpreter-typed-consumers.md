# Typed expression consumers

The interpreter consumes positioned scalar `len` and `cap` calls and scalar
return expression trees directly. Logical `&&` and `||` evaluate their right
operand only when needed, so nil guards can safely precede pointer dereferences.
The existing diagnostic for an obviously non-boolean logical operand remains.
Scalar calls currently require the unshadowed `len` or `cap` builtin.

Returned declared calls propagate all result values and their typed cells.
Concrete callback parameter signatures are checked against the supplied closure.
Argument cells are captured in source evaluation order, preserving collection
and pointer identity; channel authority still travels through the separate,
owner-checked channel binding path. Deferred calls retain the captured cells.

The predeclared `error` name uses the ordinary interface machinery with one
`Error() string` method. It supports assignment, method dispatch, embedding and
type assertions. A nil interface compares equal to nil; an interface containing
a typed nil pointer does not. An explicitly declared type named `error` retains
normal shadowing precedence.

Tests execute nonempty and nil slice guards, a guarded nil pointer dereference,
a concrete callback, a forwarded tuple return, error interface method dispatch,
embedding and assertions, and a typed nil pointer inside an error interface.
These checks establish the covered consumers, not complete Go compatibility.
