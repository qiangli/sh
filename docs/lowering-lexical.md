# Native lexical storage

Runtime-backed programs keep script variables in the current execution's native
lexical cells. The compiler uses resolved Go object identities to replace only
script storage; parameters, fields, named results and shadowing locals retain
native Go storage. Initialization remains at the original source statement.
The transformed program is checked again before Compile returns it.

Shell functions capture the names visible at their definition and share those
cells' later writes. Each shell region temporarily projects its captured cells
into the persistent shell session and restores the underlying shell variables
on completion or unwind. This prevents a later typed declaration from becoming
visible to an earlier function. Native shell returns preserve source status.

Acceptance includes the public later-declaration and later-write scope cases,
source-removed pointer-result artifacts, and 24 concurrent invocations of one
compiled exported entry under the race detector.

Local and parameter cells, rich shell writes, raw scalar spelling and returned
callable adapters require their respective continuation hooks. This initial
storage change does not establish parity for those additional cases.

Native shell call arguments are captured before invocation and restored on
return or unwind. Positional parameters `$1`, `$#`, `${1-default}` and standalone
quoted `"$@"` use the session's copied argument vector; the quoted all-arguments
form retains empty strings and embedded whitespace. Pure shell functions without
native binding references stay in the persistent shell, including source/eval
lookup. Functions referencing later typed names still require native capture of
an empty definition-time view.
