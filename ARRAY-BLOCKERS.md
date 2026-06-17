# array fixture — RESOLVED (0 diff-lines, fixture PASSES)

The `array` fixture now passes with 0 diff-lines, all guards (errors,
nameref, arith, quotearray, varenv, new-exp, attr, builtins) PASS, and
assoc held at its baseline of 2.

Cluster A (arithmetic command-substitution in array subscripts) was
resolved earlier (60 → 8). The final two clusters below are now resolved.

## B2 — empty subscript in `(( ))` prints "bad array subscript" twice (array27.sub, RESOLVED)

```
(( y[$none] ))      # none unset → y[]  (empty subscript)
```

bash's arithmetic evaluator parses the array reference's name part twice
(once via `array_variable_part` to resolve the variable, once via
`get_array_value` to read it); `array_variable_name` rejects the empty
`[]` each time, so `y[]: bad array subscript` is emitted twice and the
reference evaluates to 0.

Fix: added an `OnBadArraySubscript` callback to `expand.Config` (wired
by the runner) and detected the empty post-expansion subscript in
`arithmIndexedParamLiteral`, firing the callback twice and yielding 0.
Commit: "interp+expand: error on empty array subscript in arithmetic read".

## B1 — `let` escaped-quote associative key (array25.sub, RESOLVED)

```
let "a[\" \"]=11"   # key:  " "   (literal quote-space-quote, 3 chars)
let "a[\"$v\"]=13"  # v=" "  → key " "
```

The divergence was the `assoc_expand_once` shell option, not the `let`
construct alone:

- option OFF (quotearray2.sub:20): key is quote-removed → `[" "]` (space).
- option ON  (array25.sub §2):     key keeps the literal quotes → `["\" \""]`.

We stripped in both cases (matching OFF, which is why quotearray passed).
Under `assoc_expand_once` the subscript is expanded only once, so the
surviving quotes are literal key characters.

Fix: plumbed the option through `expand.Config.AssocExpandOnce` (set
around `let` evaluation) and, in `resolveAritLvalue`'s associative
branch, kept the once-expanded quotes verbatim — the printed source text
for a re-parsed double-quoted subscript (`a[" "]`), or a single
double-quoted re-expansion for a raw escaped-quote operand (`a[\"$v\"]`).
Updated the one interp_test expectation that had encoded the old
quote-stripping behaviour. Commit: "expand+interp: preserve let
assoc-key quotes under assoc_expand_once".
