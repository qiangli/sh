package lower

import (
	"fmt"
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
		var ch string
		var err error
		if n.ChanExpr != nil {
			ch, err = e.expr(n.ChanExpr)
		} else {
			ch, err = e.valueWord(n.Chan)
		}
		if err != nil {
			return "", true, err
		}
		var v string
		if n.ValueExpr != nil {
			v, err = e.expr(n.ValueExpr)
		} else {
			v, err = e.valueWord(n.Value)
		}
		return ch + " <- " + v, true, err
	case *syntax.BashPPReceive:
		ch, err := e.goSourceReceiveOperand(n)
		return "<-" + ch, true, err
	case *syntax.BashPPClose:
		ch, err := e.valueWord(n.Chan)
		return "close(" + ch + ")", true, err
	case *syntax.BashPPSelect:
		var out strings.Builder
		target := e.pushBranchTarget(false)
		defer e.popBranchTarget()
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
		if target.label != "" {
			return target.label + ":\n" + out.String(), true, nil
		}
		return out.String(), true, nil
	case *syntax.BashPPShortDecl:
		if n.MakeChan != nil {
			typ, err := e.typeExpr(n.MakeChan.ChanType)
			if err != nil {
				return "", true, err
			}
			cap := ""
			if n.MakeChan.CapacityExpr != nil {
				v, err := e.expr(n.MakeChan.CapacityExpr)
				if err != nil {
					return "", true, err
				}
				cap = "," + v
			} else if n.MakeChan.Capacity != nil {
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
			ch, err := e.goSourceReceiveOperand(n.Recv)
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

// goSourcePositions preserves caller positions in native stack and logging APIs.
// Add directives only after checking emitted Go, while retaining physical output
// lines in the external source map used by transpile consumers.
func goSourcePositions(result *Result, origin string) error {
	byLine := make(map[int]int, len(result.Mappings))
	for i, m := range result.Mappings {
		byLine[m.GoLine] = i
	}
	var out strings.Builder
	physical := 1
	lines := strings.Split(string(result.Source), "\n")
	for i, line := range lines {
		if mi, ok := byLine[i+1]; ok {
			m := &result.Mappings[mi]
			name := m.Source
			if name == "" {
				name = origin
			}
			if strings.ContainsAny(name, "\r\n") {
				return fmt.Errorf("source filename cannot be represented in Go line directive")
			}
			if name != "" && m.Pos.Line() > 0 {
				fmt.Fprintf(&out, "//line %s:%d:%d\n", name, m.Pos.Line(), m.Pos.Col())
				physical++
			}
			m.GoLine = physical
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
		physical++
	}
	result.Source = []byte(out.String())
	// The public map contract anchors each marker at the next nonempty
	// physical line, including compiler directives. Runtime line directives
	// do not change these generated-file coordinates.
	pending, index := false, 0
	for i, line := range strings.Split(string(result.Source), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "// lower:") {
			pending = true
			continue
		}
		if pending && strings.TrimSpace(line) != "" {
			if index >= len(result.Mappings) {
				return fmt.Errorf("generated marker count exceeds source mappings")
			}
			result.Mappings[index].GoLine = i + 1
			result.Mappings[index].GoCol = len(line) - len(strings.TrimLeft(line, "\t ")) + 1
			index++
			pending = false
		}
	}
	if pending || index != len(result.Mappings) {
		return fmt.Errorf("generated marker count differs from source mappings")
	}
	return nil
}

func (e *emitter) goSourceReceiveOperand(recv *syntax.BashPPReceive) (string, error) {
	if recv.ChanExpr != nil {
		return e.expr(recv.ChanExpr)
	}
	return e.valueWord(recv.Chan)
}
