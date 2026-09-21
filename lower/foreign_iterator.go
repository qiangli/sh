package lower

import (
	"fmt"
	"mvdan.cc/sh/v3/syntax"
	"strings"
)

func (e *emitter) foreignIteratorRange(n *syntax.BashPPRange) (string, error) {
	if len(n.Names) > 1 {
		return "", e.fail(n, CodeType, "iterator range permits one variable")
	}
	value, err := e.valueWord(n.Chan)
	if err != nil {
		return "", err
	}
	e.push()
	defer e.pop()
	ns := names(n.Names)
	prefix := ""
	if len(ns) > 0 {
		for _, name := range ns {
			e.bind(name)
		}
		prefix = strings.Join(ns, ",") + " := "
	}
	target := e.pushBranchTarget(true)
	body, err := e.block(n.Body)
	e.popBranchTarget()
	ctx := e.prefix + "context.Background()"
	if e.execution {
		ctx = e.runtimeScope().Context
	}
	out := fmt.Sprintf("for %srange %srt.IteratorSequence(%s,%s) {\n%s\n}", prefix, e.prefix, ctx, value, body)
	if target.label != "" {
		out = target.label + ":\n" + out
	}
	return out, err
}
