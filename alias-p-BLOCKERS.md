# alias-p deferred cases (parser/lexer reentrancy — pure-Go streaming-parser ceiling)

Real bash 5.3 performs alias expansion **at the lexer level**, token-by-token,
before the grammar is applied. An alias word in command position can therefore
become a reserved word (`if`, `do`, `case`, `{`, …) or an operator (`<`, `;;`),
merge with surrounding tokens, introduce a here-document, span line
continuations, or chain into following words via a trailing blank — all while
the parser is still building structure.

This engine (`mvdan.cc/sh`) uses a **streaming, non-backtracking parser** that
builds the full AST first; `interp` then re-parses only a *command-position
simple-command* alias body together with the trailing args of that same
`CallExpr` (see `interp/runner.go`, the `als.raw`/`als.file` reparse paths).
By the time `interp` sees the AST, the structure for any alias that should have
become a reserved word / operator / multi-statement boundary is already wrong
(e.g. `begin a; end` with `alias begin={ end=}` is parsed as two ordinary
simple commands `begin a` and `end`, never as `{ a; }`). Fixing these requires
moving alias expansion into the lexer/parser — out of scope for `interp/expand`
and a fundamental design change to the parser. Deferred:

- **using aliases in compound commands** — alias → `{` / `}` reserved words.
- **alias substitution to empty string** — empty alias must drop the command node and chain across the pipe/newline.
- **alias substitution to blank before if / before newline** — blank alias must vanish so the following reserved word is recognized.
- **alias substitution to blank should not change exit status** — blank alias must yield *no* command node so `$?` is preserved (interp builds an empty command that resets `$?` to 0).
- **alias substitution to here-document / here-document operand** — alias body introduces `<<WORD`; the here-doc body is read by the lexer.
- **alias substitution to `!`** — alias → pipeline `!` reserved word.
- **alias substitution to parenthesis** — alias → `(` / `)` subshell operators spanning statements.
- **alias substitution to if/then/elif/else/fi keywords** — alias → control-flow reserved words.
- **alias substitution to while/until/do/done keywords** — alias → loop reserved words.
- **alias substitution to for** / **word (for)** / **in (for)** / **do/done (for with in)** / **do/done (for w/o in)** / **inapplicable alias substitution of do (for)** — alias → `for`/`in`/`do`/`done` and the `for` grammar.
- **alias substitution to case/esac keywords** / **in (case)** / **case pattern** / **( (case)** / **| (case)** / **) (case)** / **;;** — alias → `case` grammar tokens and pattern fragments.
- **alias substitution to function definition** / **parentheses in function definition** / **command in function definition** — alias → `name()` function-definition syntax.
- **alias ending with blank** (multi-step chaining where a later word is itself a command-position alias mid-line) — trailing-blank chaining must re-enter the lexer for the *next* word, including non-leading positions.
- **alias substitution to line continuation** — alias body ending in `\`/`\<newline>` must splice with following input lines at lex time.

All of the above are genuine bash 5.3 behaviours (gosh diverges); none can be
satisfied without lexer-integrated alias expansion.

## Fixed in this change (interp only)

- **reusing printed alias (complex quotation)** — `alias`/`command -v` now quote
  alias bodies with bash's `sh_single_quote()` (embedded `'` → `'\''`) so the
  printed form re-parses under `eval`.
- **using alias after assignment (complex)** — variable-assignment prefixes
  (`a=A b`) are now prepended to the alias reparse source so they attach to the
  resulting command, as bash does.
