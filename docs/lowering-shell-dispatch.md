# Shell regions and readonly dispatch

Dynamic shell statements use one persistent Session backend. Shell function
definitions, ordinary calls, eval, source, conditionals, and shell status share
that backend across regions. Explicit agentic scope is applied at the region
boundary; ordinary shell function calls retain the interpreter's own scope
rules. An explicit shell exit stops generated statements without printing a
second exit diagnostic.

The compiler refuses a dynamic shell region containing a typed node that needs
native dispatch. Typed function bodies remain generated Go. In particular,
unwinding a native panic through a shell-defined function still needs a native
callback bridge; it is not implemented by serializing that typed body.

Mixed artifacts declare a dependency on `lower/shellrt/shellexec` and its module
dependencies. Build them in a module with the matching sh version or an explicit
local replacement and resolved checksums. Pure native output retains its
standard-library-only path.

Readonly declarations now mark live native locations. Rebinding, rooted
field/index mutations, collection builtins, and pointer writes invoke the
runtime guards. Pointer writes evaluate the checked pointer and RHS before the
readonly check. Runtime-owned readonly failures survive source recover calls.
Native subshell graph snapshots and continued execution after readonly failure
remain separate work.

`Options.Entry` opts into an exported injectable entry. The compiler validates
the name and rejects source declaration collisions. `Result.Entry` reports the
emitted entry; without an explicit option, execution units use a private
hygienic name and ordinary native units need no runtime entry. Supplying an
entry does not yet isolate native package-level mutable script bindings across
concurrent invocations; programs using those bindings still need generated
per-invocation storage.

Native bindings changed by arbitrary dynamic shell text also need a complete
state bridge. The implemented shell-state persistence tests cover variables
owned by shell regions; they do not certify arbitrary native-object mutation
through eval or source.
