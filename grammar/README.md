# Bash# delta grammar

`delta.ebnf` describes only the Bash# productions added to GNU Bash 5.3.
`delta.gbnf` gives machine-readable recognition rules for their line headers.
The ordinary Bash grammar, word expansion, heredocs, loops, shell functions,
Go type and expression grammar, and foreign fence bodies are outside this delta.
A decoder must compose this delta with a Bash grammar; `root` selects one
Bash# header and does not claim to be a complete shell grammar.

A recognized fence starts in column one with at least three tildes, followed
by a format name and an optional alias and runner. Its closing line consists
of exactly the same number of tildes. The body is uninterpreted bytes.
`embed` uses a quoted `./` or `../` path and the same alias/runner tail;
relative paths are checked for existence by the engine, beyond grammar.

A decorator stack ends at a function declaration. `@go.error()` is a
special decorator marker whose semantic restrictions (single use, typed
receiverless nongeneric function, no existing trailing `error`) are checked
by the evaluator. `agentic` introduces a block or modifies a shell or typed
function. Typed signatures and bodies belong to Go and mixed Bash# grammar.
Near misses in the Bash-accepted Class E forms remain ordinary shell text.
The parser's Class R forms may produce diagnostics after a committed prefix.

The agreement test interprets the GBNF header rules independently and compares
whole-file acceptance with the engine parser over the Tour and tool fixtures.
It also checks negative fence cases. It is a delta agreement check, not a
proof that the opaque Bash base grammar is complete.
