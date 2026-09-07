# Checked dispatch and task calls

The emitter routes native dereferences and scalar/comma-ok assertions through
the checked-value helpers. Assertions compare the source and target method sets
independently of the current interface payload, retaining impossible-assertion
diagnostics at execution time. Pure checked programs use a small entry error
boundary; programs with task or agentic state use their explicit Program.
Source recover calls rethrow runtime-owned guard and channel aborts while
remaining direct Go recover calls inside deferred functions.

Task calls capture typed arguments before launch. Their child Program reports
source panics and command failures, joins descendants, and returns the source
exit status to the owning session. The generated program boundary translates
task failures to the language's task diagnostic; ordinary Go Session clients
keep the detailed task error API.

Deferred calls with defaults or keyword arguments first capture arguments in
source evaluation order and then directly defer the resolved function. No
invocation wrapper intervenes between Go's defer and the source function.

Promoted struct literal keys now use the native nested-literal helper, preserving
field initializer evaluation order.

Checked local short declarations now preserve continuation: a failed dereference
leaves the binding absent, reports the positioned value error, and allows later
statements to run. The emitter tracks presence separately from native storage,
so typed printing uses the absent name and shell expansion uses an empty value.
Implicit pointer field reads use the same checked path. Typed Print/Println
preserve the preceding command status, including a failed initializer.

Tuple function results are evaluated once into typed temporaries. The runtime
retains each result's static type, validates every target, and commits only if
the whole assignment is valid. A rejected tuple reports its error and continues
with every original binding unchanged.

Remaining checked-value work includes indexed writes, checked make sizes, and
complete continuation for other statement forms and unresolved field types.
These changes do not certify the entire lowering profile.
