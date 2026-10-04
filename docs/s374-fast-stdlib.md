# In-process standard-library calls (prototype)

The bridge may call a linked symbol directly only when the authenticated
import path and selected top-level function appear in a fixed symbol table,
there is no receiver, callback, spread, or transfer, and every argument and
result has a plain scalar, string, or copied byte-slice type. Values with
handles, pointer origins, storage identity, interfaces, or mutation/writeback
stay on the worker path. The fallback must retain its existing validation and
panic behavior. A direct call encodes its result in the same bridge shape as
the worker, including declared scalar type and string bytes.

Package state has one owner per interpreted program. `strconv` has no package
state. `math/rand` top-level draws and Seed share state, so they must be routed
as a unit; any program that can invoke an uncovered package-level operation
must retain the worker as owner for all its random calls. A fresh interpreter
run needs its own random state. The deprecated Read function, package-level
function values, and dynamic calls make this ownership proof conservative.
The prototype should use a source-level proof of direct covered calls before
placing random state in the interpreter; otherwise every random call stays in
the worker. Method calls on independently created `*rand.Rand` values remain
worker-owned. The prototype limits that proof to one readable source file,
direct selectors, no companions, and imports whose entry points do not draw
from `math/rand`. The interpreter owns a fresh, locked generator for that run;
`GODEBUG=randautoseed=0` and `randseednop=0` are honored.

Direct calls can differ in panic wording, type conversion, concurrency order,
and process-wide environment. A panic retries through the worker so its
existing translation and unwind apply. Tests compare direct results with worker
results, including seeded random sequences, and force the worker fallback for
ineligible values. In-process functions also run in the interpreter's process:
the table must remain small and exclude file, network, and other externally
visible effects.
