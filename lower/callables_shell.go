package lower

import (
	"bytes"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func (e *emitter) needsShell(file *syntax.File) {
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.FuncDecl, *syntax.IfClause, *syntax.BinaryCmd:
			e.mixedShell = true
		case *syntax.CallExpr:
			if len(n.Args) > 0 {
				name := n.Args[0].Lit()
				if name != "echo" && name != "printf" {
					e.mixedShell = true
				}
			}
		}
		return true
	})
	if e.mixedShell {
		e.execution = true
		e.bridge = true
	}
}
func (e *emitter) dynamicShell(stmt *syntax.Stmt) bool {
	if !e.mixedShell || stmt == nil {
		return false
	}
	switch n := stmt.Cmd.(type) {
	case *syntax.FuncDecl, *syntax.IfClause, *syntax.BinaryCmd:
		return true
	case *syntax.CallExpr:
		if len(n.Assigns) > 0 || len(n.Args) == 0 || stmt.Negated || stmt.Background || len(stmt.Redirs) > 0 {
			return true
		}
		name := n.Args[0].Lit()
		return name != "echo" && name != "printf"
	}
	return false
}
func (e *emitter) shellStatement(stmt *syntax.Stmt) (string, error) {
	var unsupported syntax.Node
	syntax.Walk(stmt, func(node syntax.Node) bool {
		if node == nil {
			return false
		}
		if _, scope := node.(*syntax.BashPPAgenticBlock); !scope && strings.HasPrefix(nodeName(node), "BashPP") {
			unsupported = node
			return false
		}
		return unsupported == nil
	})
	if unsupported != nil {
		return "", e.fail(unsupported, CodeUnsupported, "dynamic shell region contains a typed node requiring native dispatch")
	}
	var source bytes.Buffer
	if err := syntax.NewPrinter().Print(&source, stmt); err != nil {
		return "", err
	}
	return e.mark(stmt.Cmd) + e.program() + ".ShellRegion(" + strconv.Quote(source.String()) + ")\n", nil
}
