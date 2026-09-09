package lower

import (
	"mvdan.cc/sh/v3/syntax"
	"strings"
)

// Pure Go source uses native Go concurrency. Bash++ sessions, cancellation
// carriers and shell scope are not part of the Go language's execution model.
func (e *emitter) goSourceCommand(c syntax.Command) (string, bool, error) {
	switch n := c.(type) {
	case *syntax.BashPPGo:
		v, err := e.call(n.Call)
		return "go " + v, true, err
	case *syntax.BashPPSend:
		ch, err := e.valueWord(n.Chan)
		if err != nil {
			return "", true, err
		}
		v, err := e.valueWord(n.Value)
		return ch + " <- " + v, true, err
	case *syntax.BashPPReceive:
		ch, err := e.valueWord(n.Chan)
		return "<-" + ch, true, err
	case *syntax.BashPPClose:
		ch, err := e.valueWord(n.Chan)
		return "close(" + ch + ")", true, err
	case *syntax.BashPPSelect:
		var out strings.Builder
		out.WriteString("select {\n")
		for _, arm := range n.Cases {
			e.push()
			if arm.Default {
				out.WriteString("default:\n")
			} else {
				comm, err := e.command(arm.Comm)
				if err != nil {
					e.pop()
					return "", true, err
				}
				out.WriteString("case " + strings.TrimSuffix(comm, e.unused(goSourceCommNames(arm.Comm))) + ":\n")
			}
			for _, stmt := range arm.Stmts {
				v, err := e.statement(stmt)
				if err != nil {
					e.pop()
					return "", true, err
				}
				out.WriteString(v)
			}
			e.pop()
		}
		out.WriteString("}\n")
		return out.String(), true, nil
	case *syntax.BashPPShortDecl:
		if n.MakeChan != nil {
			typ, err := e.typeExpr(n.MakeChan.ChanType)
			if err != nil {
				return "", true, err
			}
			cap := ""
			if n.MakeChan.Capacity != nil {
				v, err := e.valueWord(n.MakeChan.Capacity)
				if err != nil {
					return "", true, err
				}
				cap = "," + v
			}
			ns := names(n.Lhs)
			for _, name := range ns {
				e.bind(name)
			}
			return strings.Join(ns, ",") + " := make(" + typ + cap + ")", true, nil
		}
		if n.Recv != nil {
			ch, err := e.valueWord(n.Recv.Chan)
			ns := names(n.Lhs)
			for _, name := range ns {
				e.bind(name)
			}
			return strings.Join(ns, ",") + " := <-" + ch, true, err
		}
	case *syntax.BashPPAssign:
		if n.Call != nil && len(n.Names) == 0 && n.TargetExpr != nil {
			lhs, err := e.expr(n.TargetExpr)
			if err != nil {
				return "", true, err
			}
			rhs, err := e.call(n.Call)
			return lhs + " = " + rhs, true, err
		}
	}
	return "", false, nil
}
func goSourceCommNames(c syntax.Command) []string {
	if s, ok := c.(*syntax.BashPPShortDecl); ok {
		return names(s.Lhs)
	}
	return nil
}
