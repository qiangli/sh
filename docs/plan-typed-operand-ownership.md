# Typed operand ownership

The parser claims raw string and parenthesized short-declaration operands only
inside a committed Bash++ function, after recognizing the complete `name :=`
prefix. It reads positioned tokens and uses the existing bounded expression
converter. Shell arguments and top-level Class-E syntax retain their shell
backquote and grouping behavior.

Scalar operator returns in that same typed body retain a positioned
`BashPPReturn.Expr` tree and a complete legacy `Results` word. Returned calls and
escaping function literals retain their separate existing fields. A closing
function brace terminates an inline returned call.

Validation covers the existing positive nil guard spelling, inline call
forwarding, Unicode and multiline raw literals, parenthesized arithmetic,
byte-at-a-time input, positions, walking, stable parse/print, typed JSON, and
Classic/POSIX/Bash++ shell isolation. Runtime evaluation and native lowering
consume the AST separately and require their own tests.
