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

Remaining checked-value work includes indexed writes, checked make sizes, and
statement continuation after a failed local dereference. The engine can report
a nil dereference and continue later statements with the failed binding absent;
the current checked helper unwinds to the entry boundary. The terminal public
nil-dereference fixture matches, but that continuation behavior is not yet
implemented. This slice does not certify the entire lowering profile.
