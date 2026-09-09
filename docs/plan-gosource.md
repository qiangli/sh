# Unchanged Go source ingestion — Sprint 118

Use `gosource.Load` or `gosource.Parse` for explicit Go input. The original bytes
are parsed and type checked with Go's standard front end, then converted directly
to positioned Bash++ nodes. Both execution modes consume `Program.File`.
`RunMain` requests synthetic calls to ordered init functions and main. Without it,
loading validates all source bodies but never runs them. Source hashes and a
multi-file offset map retain source identity.

The initial vertical slice covers Go comments, Unicode, exact strings/runes and
operators, imports, functions, package variable dependency order, multiple init
functions and main. Package functions use ordinary Go package visibility; the
Bash++ snapshot rule is retained for ordinary Bash++ input. Go-source bridge
arguments are evaluated in the interpreter before transferring scalar values.
The tested program is never sent to native Go for interpreted execution.

Remaining work is driven by unchanged corpus failures. Current explicit gaps
include tuple var declarations/initializers, labeled branches,
complex constants and some callable/type forms. Computed callees are retained in
`BashPPCall.CalleeExpr`; lowering supports them, while runtime bridge support is
an explicit follow-on. Type switches convert to the existing typed node. Native dependency bridging still
needs general structured values and stateful handles. Unsupported applicable
constructs are errors and cannot count as corpus passes. Module imports can be
supplied through `Options.Importer`; the default uses Go export data. Host build
constraints and package-directory selection belong to the caller. Every supplied
source is included, as with an explicit Go file list.

Verification: package tests exercise interpretation, lowering and source errors;
manager independently runs unchanged upstream inputs and builds/runs lowered
artifacts. Expand meaningful regressions as defects are fixed, preserving Classic
shell and Bash++ semantics outside `File.GoSource`.


The Go-source tree now carries its source table through `syntax.File.Sources`,
`lower.Result.Sources` and per-mapping Source/SourceOffset. Typed JSON retains
this metadata and computed callees. Ordinary Go builtin print/println use stderr;
package declarations are cleared by Runner.Reset. Go-source failures abort the
program immediately and scalar diagnostics retain the originating input position.

All 97 unchanged Tour inputs pass ingestion with the pinned helper module
context in a discovery probe. This is an ingestion observation, not runtime or
compiled corpus acceptance. The first 97-row exploratory run showed many runtime
failures and output discrepancies; its raw observations remain in the manager's
worker evidence path. No unsupported applicable row is excluded.
