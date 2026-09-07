package lower

import "mvdan.cc/sh/v3/syntax"

// callArgument reads the authoritative typed edge when the parser committed
// a scalar argument. Legacy words retain their ordinary source conversion.
func (e *emitter) callArgument(c *syntax.BashPPCall, index int) (string, error) {
	if c.ArgExprs != nil {
		if index >= len(c.ArgExprs) || c.ArgExprs[index] == nil {
			return "", e.fail(c, CodeExpr, "missing typed call argument")
		}
		return e.expr(c.ArgExprs[index])
	}
	return e.argument(c.Args[index])
}
func (e *emitter) plannedCallArgument(c *syntax.BashPPCall, w *syntax.Word) (string, error) {
	for i, supplied := range c.Args {
		if supplied == w {
			return e.callArgument(c, i)
		}
	}
	return e.argument(w)
}
