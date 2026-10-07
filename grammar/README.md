# Bash# delta grammar

`delta.gbnf` is the executable grammar for the Bash# productions added to GNU
Bash 5.3; `delta.ebnf` is its specification twin. `grammar.go` interprets the
GBNF as a parsing expression grammar (ordered alternatives, greedy repetition)
over a whole newline-terminated program: `root ::= item*`, where an item is a
fence, an embed line, a decorator stack with its declaration, an `agentic`
form, a typed function or method, a bare-brace block, or the opaque base-Bash
fragment. Every accept or reject verdict follows from those productions.
Editing a rule changes verdicts; the tests pin the published shape.

## What the delta owns and what stays opaque

The delta owns structure, not Bash: same-count tilde fences with opaque
bodies (any count of three or more; `fence-pair` recursively pairs each
additional opener tilde with its closer), the
`embed` line with its quoted `./` or `../` path and the fence alias/runner
tail, decorator lines committed at `@name(` and ending at a newline or `;`,
the `@name()` + compound-body spelling that Bash already owns as a function
named `@name`, the four `agentic` forms plus the near-miss `agentic` command,
Go-shaped typed signatures (balanced receiver, parameter and type-parameter
text, a result type or parenthesized result list), and blocks as balanced
bare braces around nested items.

Exactly one nonterminal is bound to Go code: `bash-fragment`, listed in
`OpaqueRules` and declared as an external `? ... ?` production in the EBNF.
It consumes ordinary Bash text up to the next delta-owned boundary and
supplies the boundaries the base grammar defines: `#` comments at word
start, single and double quotes across lines, backslash-newline
continuation, heredoc bodies (`<<`, `<<-`, quoted delimiters), and bare
`{` / `}` words. A `{` is bare at command position or after a Bash# block
head (`if`, `for`, `switch`, `func() `, `} else`, ...); an attached `{` as
in `T{` is a literal with a partner; a standalone `{` after an ordinary
command is a plain word. Command substitutions are deliberately not
tracked: the engine recognizes a column-one fence inside `$( )`, and so does
the fragment. The EBNF's other externals are the byte alphabet (`letter`,
`digit`, `space`, `any_but_newline`, ...).

Near misses stay Bash, as in the engine (Class E): a fence header with a
trailing comment or a bad tail, an unquoted or absolute embed path, `func f`
without parentheses, `agentic echo`. Committed prefixes (Class R) must
complete: `@name(` at a line start, `func f(`, `agentic {`, a valid fence
header. The engine's semantic checks are not grammar: `@go.error()` is
syntactically a decorator (the evaluator enforces single use and a typed
receiverless target), and a missing embed target is reported by
`CheckDeltaFile` after recognition, exactly as the engine does.

## Tests (`go test -tags full ./grammar`)

- `TestPublishedProductions` pins each production on positive and negative spellings, including the fence closer, decorator endings and the result-type rule.
- `TestAgreementWithPublishedCorpora` runs 40 Tour `.bsh` files and the 18 `.bpp` heredocs in `bashsharp-tests/tools/polyglot-gate.sh` through the grammar and the engine, requires the same verdict, and compares the recorded sites with the engine's AST extension nodes line by line. It reports the accept/reject split; the published corpus is all engine-accepted and exercises fences, decorators, all four `agentic` forms and typed functions, but no embed, typed method or `@name()` function.
- `TestFixtureAgreement` covers `testdata/accept` and `testdata/reject`: embed, typed method, `@name()` function, and boundary cases on the accept side; unclosed fence, dangling and indented decorators, missing embed target, unclosed and malformed `agentic` and typed forms on the reject side. The directory is a third voice: the engine and the grammar must both agree with it.
- `TestSpellingAgreement` is the inline table of committed and near-miss spellings for every production, each compared with the engine.
- `TestKnownDivergences` asserts the forms where the opaque fragment is coarser than the engine: a standalone `}` or `{ x }` word after an ordinary command, a brace group after `;` on the same line, and a multi-line backtick body.
- `TestEBNFMirrorsGBNF` parses the EBNF into a production graph and requires the same production set, the same referenced nonterminals per production, and no undefined nonterminal; the opaque rule must be declared external.

Full delta-over-Bash/Go constrained decoding remains **OPEN**: a decoder
composes this delta with Bash and Go grammars (Sprint 384 owns the combined
grammar). The published `bash-fragment` shape is the loose upper bound for
that composition, not a Bash grammar.
