package lower

import (
	"bytes"
	"mvdan.cc/sh/v3/syntax"
	"strconv"
	"strings"
)

func (e *emitter) findNativeShells(file *syntax.File) {
	e.nativeShellNames = map[string]bool{}
	syntax.Walk(file, func(node syntax.Node) bool {
		f, ok := node.(*syntax.FuncDecl)
		if !ok || f.Name == nil {
			return true
		}
		syntax.Walk(f.Body, func(n syntax.Node) bool {
			_, scope := n.(*syntax.BashPPAgenticBlock)
			if n != nil && !scope && strings.HasPrefix(nodeName(n), "BashPP") {
				e.nativeShellNames[f.Name.Value] = true
			}
			return true
		})
		return true
	})
}
func (e *emitter) nativeShellStatement(s *syntax.Stmt) (string, bool, error) {
	if s == nil {
		return "", false, nil
	}
	switch n := s.Cmd.(type) {
	case *syntax.FuncDecl:
		if n.Name == nil || !e.nativeShellNames[n.Name.Value] {
			return "", false, nil
		}
		if err := e.statementFlags(s); err != nil {
			return "", true, err
		}
		block, ok := n.Body.Cmd.(*syntax.Block)
		if !ok {
			return "", true, e.fail(n, CodeUnsupported, "native shell callable needs a block body")
		}
		priorProgram, priorFunc, priorResults := e.programExpr, e.inFunc, e.resultTypes
		e.programExpr = e.prefix + "program"
		e.inFunc = true
		e.resultTypes = nil
		e.push()
		body, err := e.block(block)
		e.pop()
		e.programExpr, e.inFunc, e.resultTypes = priorProgram, priorFunc, priorResults
		if err != nil {
			return "", true, err
		}
		return e.mark(n) + e.program() + ".DefineNativeShell(" + strconv.Quote(n.Name.Value) + "," + strconv.FormatBool(n.Agentic != nil) + ",func(" + e.prefix + "program *" + e.prefix + "rt.Program){\n" + body + "\n})\n", true, nil
	case *syntax.CallExpr:
		if len(n.Args) == 0 || !e.nativeShellNames[n.Args[0].Lit()] {
			return "", false, nil
		}
		if err := e.statementFlags(s); err != nil {
			return "", true, err
		}
		if len(n.Args) != 1 || len(n.Assigns) > 0 {
			return "", true, e.fail(n, CodeUnsupported, "native shell callable positional arguments need the shell parameter bridge")
		}
		var raw bytes.Buffer
		if err := syntax.NewPrinter().Print(&raw, s); err != nil {
			return "", true, err
		}
		return e.mark(n) + "if !" + e.program() + ".CallNativeShell(" + e.callSite(n, n.Args[0].Lit()) + "){" + e.program() + ".ShellRegion(" + strconv.Quote(raw.String()) + ")}\n", true, nil
	}
	return "", false, nil
}

func hasBadSubstitution(node syntax.Node) bool {
	found := false
	syntax.Walk(node, func(n syntax.Node) bool {
		if p, ok := n.(*syntax.ParamExp); ok && p.BadSubst != nil {
			found = true
		}
		return !found
	})
	return found
}
