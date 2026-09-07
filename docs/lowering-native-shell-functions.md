# Shell functions containing native typed code

A shell function containing a certified typed statement is emitted as a native callback registered on its Program when the declaration executes. The registry belongs to one execution; derived sequential scopes share it and task/subshell children copy its definition map. Redefinition replaces the callback at the source statement. Calls made before registration fall back to ordinary shell lookup.

Invocation checks the function's assistance marker using the caller's frame. Native panic payloads stay on the calling goroutine and unwind through source deferred functions. Pure dynamic shell functions remain on the persistent Session backend. Positional arguments and calls originating inside an uninterpreted dynamic region still require the separate native callback bridge; they produce explicit diagnostics in this slice.

Malformed parameter expansion remains a runtime source operation. In particular, the parser's BadSubst node for `${v.N}` is not reinterpreted as a valid typed selector. Emitted code aborts that shell statement with `bad substitution`, retains status 1, and allows the next statement to run. An unused method with such a body can therefore compile without executing the malformed expansion.

Same-source tests include the public panic-shell-unwind and value-copy-assertion-zero fixtures, definition activation/redefinition, marked and unmarked invocation, and an actually invoked malformed expansion with subsequent output. The panic fixture also builds and runs with the Go race detector after generated source removal.
