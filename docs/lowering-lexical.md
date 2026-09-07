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
