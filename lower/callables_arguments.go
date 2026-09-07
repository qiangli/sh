package lower

import (
	"mvdan.cc/sh/v3/syntax"
	"strconv"
)

// callArgument reads the authoritative typed edge when the parser committed
// a scalar argument. Legacy words retain their ordinary source conversion.
func (e *emitter) callArgument(c *syntax.BashPPCall, index int) (string, error) {
	if c.ArgExprs != nil {
		if index >= len(c.ArgExprs) || c.ArgExprs[index] == nil {
			return "", e.fail(c, CodeExpr, "missing typed call argument")
		}
		return e.expr(c.ArgExprs[index])
	}
	if name := c.Args[index].Lit(); e.funcs[name] && e.callParameterType(c, index) == "string" {
		return strconv.Quote(name), nil
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

func (e *emitter) callParameterType(c *syntax.BashPPCall, index int) string {
	var fields []*syntax.BashPPField
	if c.FuncLit != nil {
		fields = c.FuncLit.Params
	} else if len(c.Fun) == 1 {
		if f := e.functionDecls[c.Fun[0].Value]; f != nil {
			fields = f.Params
		}
	} else if len(c.Fun) > 1 && e.imports[c.Fun[0].Value] == "" {
		_, receiverType := e.methodReceiver(c.Fun[:len(c.Fun)-1])
		method := c.Fun[len(c.Fun)-1].Value
		if e.typeNames[c.Fun[0].Value] || c.PointerMethodExpr {
			index--
		}
		if index < 0 {
			return ""
		}
		if iface, ok := e.interfaceDecl(receiverType); ok {
			params, _, found, err := e.interfaceMethodTypes(iface, method, map[*syntax.BashPPInterfaceType]bool{})
			if err == nil && found && index < len(params) {
				return params[index]
			}
			return ""
		}
		if f := e.methodDeclaration(receiverType, method); f != nil {
			fields = f.Params
		}
	}
	for _, field := range fields {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		if index < count {
			if typ, err := e.fieldType(field); err == nil {
				return typ
			}
			return ""
		}
		index -= count
	}
	return ""
}
