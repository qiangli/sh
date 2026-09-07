package lower

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func (e *emitter) findCheckedValues(file *syntax.File) {
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.BashPPDerefExpr, *syntax.BashPPSelectorExpr:
			e.guarded = true
		case *syntax.BashPPTypeAssertExpr:
			e.guarded = e.guarded || n.TypeToken == nil
		}
		return true
	})
	if e.guarded {
		e.bridge = true
		e.output = true
	}
}

func (e *emitter) userRecover() string {
	if e.guarded || e.execution {
		return e.prefix + "rt.PreserveAbort(recover())"
	}
	return "recover()"
}

func (e *emitter) guardBoundary() string {
	return strings.ReplaceAll(`defer func(){if v:=recover();v!=nil{if failure:=PREFIXrt.GuardError(v);failure!=nil{PREFIXfmt.Fprintln(PREFIXos.Stderr,failure);PREFIXos.Exit(failure.ExitStatus())};panic(v)}}()
`, "PREFIX", e.prefix)
}

func (e *emitter) valueAssertion(n *syntax.BashPPTypeAssertExpr, commaOK bool) (string, error) {
	if n.TypeToken != nil {
		return e.typeAssertExpr(n)
	}
	value, err := e.expr(n.X)
	if err != nil {
		return "", err
	}
	info := e.projectionExpr(n.X)
	source := info.sourceType
	if source == "" {
		source = "any"
	}
	text, err := e.checkedAssertion(n, value, source, false, commaOK)
	if err != nil {
		return "", err
	}
	target, err := e.typeExpr(n.Assert)
	if err != nil {
		return "", err
	}
	return strings.Replace(text, "Impossible:false", "Impossible:!"+e.prefix+"rt.AssertionPossible["+source+","+target+"]()", 1), nil
}
