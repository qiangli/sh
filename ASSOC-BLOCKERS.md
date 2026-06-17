# ASSOC blockers after assoc=56

Verified gate:

```sh
GOCACHE=$PWD/.gocache sh ./measureALL.sh assoc
```

Last committed state reduces `assoc` from the 119-line baseline to 56 diff-lines with all guards passing:

- `errors`, `nameref`, `arith`, `quotearray`, `varenv`, `new-exp`: PASS
- `array<=132`: OK(120)

Remaining clusters:

1. Subscript validity for declare/read/printf targets
   - Examples: `declare myarray["foo[bar"]=bleh`, `read a[$b]` where `b="80's"`, `printf -v a[$b]`, and `typeset foo["foo]bar"]=bax`.
   - A narrow attempt to reject declare-family keys containing `[` did not reduce assoc and worsened the array guard, so it was reverted.
   - This needs a single parser/word-level validity model for array-reference operands, not another error-prefix or source-text special case.

2. `assoc_expand_once` for builtin assignment targets and parameter subscripts
   - `assoc16.sub` and `assoc18.sub` still show double expansion or invalid target parsing around command-substitution subscripts and `A[$rkey]`.
   - `unset` has a dedicated `assoc_expand_once` path, but `read`, `printf -v`, and parameter expansion do not share a raw operand representation that preserves the original subscript word.

3. Tilde expansion in associative keys
   - `assoc19.sub` uses keys like `[~/key]`.
   - The current parser treats `[~/key]` as arithmetic before the interpreter can know the destination is associative, yielding `~: arithmetic syntax error`.
   - A root fix likely belongs in `syntax` so compound assignment indices that are words remain available for associative declarations.
