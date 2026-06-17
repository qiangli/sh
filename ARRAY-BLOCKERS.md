# array fixture — remaining blockers (8 diff-lines)

Cluster A (arithmetic command-substitution in array subscripts) is
**fully resolved**: 60 → 8 diff-lines, all guards (errors, nameref,
arith, quotearray, varenv, new-exp, attr, builtins) PASS, assoc=2.

The 8 lines that remain are two distinct issues **outside Cluster A**
(neither involves an un-pre-expanded `$( … )` operand), each higher-risk
to the shared `let`/arith-read paths than the reward of the lines:

## B1 — `let` escaped-quote associative key (array25.sub, 6 lines)

`array.right` "arithmetic:" section, items 6/7/8 (test lines 81–83):

```
let "a[\" \"]=11"   # bash key:  " "   (literal quote-space-quote, 3 chars)
let "a[\"$v\"]=13"  # v=" "  → key " "
```

bash preserves the **escaped** double-quotes as literal characters in the
associative key, producing `["\" \""]="11"`. We strip them, yielding key
` ` (`[" "]`). This is `let`-specific escaped-quote subscript-key quoting
in `arithLetEscapedQuotedIndex` / `assocAssignKey`, not command
substitution. Touching it risks the `quotearray`/`arith` guards (both
exercise the same escaped-quote key path).

## B2 — empty subscript in `(( ))` prints "bad array subscript" twice (array27.sub, 2 lines)

```
(( y[$none] ))      # none unset → y[]  (empty subscript)
```

bash prints `./array27.sub: line 81: y[]: bad array subscript` **twice**
(exit 1). We already exit 1 but emit no message: the arith *read* path
(`arithmIndexedParamLiteral`) treats an empty post-expansion subscript as
index 0 rather than rejecting it, and bash's double emission implies a
two-pass evaluation we don't mirror. Adding the rejection to the arith
read path risks the `arith` guard (empty/`0` subscripts are common
there); the doubled output needs separate investigation.

## Resolved Cluster A commits (this branch)

- expand: report full `$(...)` operand token in arithmetic, no cmdsub re-exec (60→46)
- interp: drop `((: )` frame for `$(…)` expansion operand arith errors (46→32)
- interp: keep operand trailing whitespace in `$(…)` arith operand errors (32→26)
- interp: don't re-execute cmdsub in unset of expanded array subscript (26→22)
- expand: attribute `$(…)` operand arith error to the operand's source span (22→14)
- interp: drop `let:` prefix for `$(…)` expansion operand errors in let (14→12)
- interp: error on `test -v` of expanded array subscript with cmdsub (12→10)
- interp: apply `declare -i` attribute when element subscript errors (10→8)
