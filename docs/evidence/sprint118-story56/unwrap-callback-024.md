# Original Unwrap callback execution

Sprint: #118; Story: #56; Story-ID: 3ef468f4e831

Base: manager014 `a72ced4a`. The helper mirrors only the exact
`Unwrap() error` signature as a typed protocol stub. Every original statement
runs through the interpreter. Only authenticated stdlib `errors.Unwrap` and
`errors.Is` calls receive the new synchronous transport permission, after
existing reference and omitted-method checks. First-class function callbacks
and arbitrary retained consumers remain refused.

Callback receiver reconstruction distinguishes a nil interface from an
interface holding a typed nil. Imported nil pointer fields retain authenticated
native handles, preserving their actual type and avoiding display-name
resolution. Typed nil descriptors are checked for assignability before being
boxed back into an original interface.

Six authored programs run unchanged in native Go, interpreted, and source-free
compiled modes: nested wrapper/error identity, pointer mutation with nested
native reentry, imported typed nil error results, local typed nil error results,
native outer wrappers, and original panic/recover. Additional controls verify
cancellation/Reset, stale callback fields, malformed callback arity, impossible
nil dynamic types, and refusal of retained/wrong-signature consumers.

`TestGoSourceOriginalUnwrap` hashes all four pinned Go1.27 errors companion
files, checks the native SDK TestUnwrap oracle, loads all four unchanged files,
registers all ten original roots, and runs the original TestUnwrap body in the
interpreter. It rechecks source hashes afterward. The separate complete root
replay invokes all ten roots and now reports **4 PASS / 6 FAIL**; TestUnwrap is
the one newly passing root. TestJoin, TestJoinErrorMethod, TestIs, TestAs,
TestAsValidation and TestAsType remain actual failures.

`Unwrap() []error` stays explicitly unsupported in this slice: copying the
returned slice could lose live alias or sibling mutation effects during native
traversal. Its negative control remains in the denominator for this boundary.
No As/AsType or generic behavior is changed.

One authored initializer `wrapped{fmt.Errorf(...), 0}` encountered the existing
native-call-as-interface-field initializer gap. Its failing source/output is
retained. The reentry control binds that imported value before initializing the
wrapper, so it tests the callback operation without claiming that separate
initializer gap was repaired. No upstream source was changed.

Raw logs, source identity evidence and the exact final gate manifest are under
`~/.local/state/bashy/sprint118-evidence/unwrap-callback-024/`.
