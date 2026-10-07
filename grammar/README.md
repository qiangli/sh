# Bash# delta grammar

`delta.ebnf` describes only the Bash# productions added to GNU Bash 5.3.
`delta.gbnf` gives machine-readable recognition rules for their line headers.
The ordinary Bash grammar, word expansion, heredocs, loops, shell functions,
Go type and expression grammar, and foreign fence bodies are outside this delta.
A decoder must compose this delta with Bash and Go grammars. `root` selects
one Bash# header; it is **not** a complete constrained-decoding grammar.
In particular the content following an opening `{` and argument expressions
are opaque here. The EBNF specifies their composition but is not an executable
whole-program recognizer. Sprint 384 owns the combined grammar.

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

`CheckDelta` independently interprets the GBNF headers, checks balanced
signature parentheses and body braces, and rejects malformed committed
extension forms. It does not call the engine parser. Its opaque Bash/Go body
checks are deliberately shallow: accepted text is not a promise that a full
Bash# program parses or runs. The EBNF test checks the published production
graph and its coverage, rather than interpreting the opaque base grammars.

The agreement test compares these delta checks with the engine parser on 40
Tour `.bsh` files and 18 actual `.bpp` heredocs extracted from
`bashsharp-tests/tools/polyglot-gate.sh`. Harness `.sh` wrappers are not
counted as Bash# fixtures. In that corpus the executable inventory covers
fences, decorators, all four agentic forms, and typed functions. It contains
no embed, typed-method-without-agentic, or `@go.error()` fixture; focused
positive and negative checks cover those spellings without claiming corpus
agreement. Full delta-over-Bash/Go constrained decoding remains **OPEN**.
