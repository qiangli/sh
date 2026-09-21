package lower

import (
	"fmt"
	"mvdan.cc/sh/v3/syntax"
)

func (e *emitter) foreignIteratorRange(n *syntax.BashPPRange) (string, error) {
	if len(n.Names) > 1 {
		return "", e.fail(n, CodeType, "iterator range permits one variable")
	}
	value, err := e.valueWord(n.Chan)
	if err != nil {
		return "", err
	}
	element := e.foreignIteratorValues[n.Chan.Lit()]
	e.push()
	defer e.pop()
	ns := names(n.Names)
	prefix := ""
	setup := ""
	if len(ns) > 0 {
		for _, name := range ns {
			e.bind(name)
		}
		if ns[0] != "_" {
			prefix = e.prefix + "iteratorValue := "
			setup = ns[0] + " := " + foreignResultExpr(e.prefix+"iteratorValue", element) + "\n" + e.unused(ns) + "\n"
		}
	}
	target := e.pushBranchTarget(true)
	body, err := e.block(n.Body)
	e.popBranchTarget()
	ctx := e.prefix + "context.Background()"
	if e.execution {
		ctx = e.runtimeScope().Context
	}
	out := fmt.Sprintf("for %srange %srt.IteratorSequence(%s,%s) {\n%s\n}", prefix, e.prefix, ctx, value, setup+body)
	if target.label != "" {
		out = target.label + ":\n" + out
	}
	return out, err
}
