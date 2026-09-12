package lower

import (
	"fmt"
	"mvdan.cc/sh/v3/syntax"
	"strings"
)

// inferredDeclType reports a var declaration whose type the converter
// inferred rather than the input wrote: the converter anchors a synthesized
// type at the declared name, where a written type can never start. A const
// keeps its inferred type: the converter materializes a constant by value,
// and the defined type it inherits (`C2 = C1` with C1 of type E) travels
// only through that spelling.
func inferredDeclType(n *syntax.BashPPDecl) bool {
	return n.Kw.Value == "var" && n.DeclType != nil && len(n.Init) > 0 && n.DeclType.Pos().IsValid() && n.DeclType.Pos() == n.Name.Pos()
}

// forwardedCallee unwraps the converter's forwarding closure for a generic
// function used as a value — `func(a0 T0) R { return F[T](a0) }` for the
// input's `F[T]` — back to the callee's own spelling, which Go accepts as a
// function value directly. The closure is synthesized entirely at the
// expression's position, so a literal whose keyword and closing brace share
// one position is never a written one. Its body is the single call, bare or
// returned; the parameters are what the call forwards.
func (e *emitter) forwardedCallee(f *syntax.BashPPFuncLit) (string, bool, error) {
	if f.Kw == nil || f.Body == nil || !f.Kw.Pos().IsValid() || f.Kw.Pos() != f.Body.Rbrace || len(f.Body.Stmts) != 1 {
		return "", false, nil
	}
	var call *syntax.BashPPCall
	switch n := f.Body.Stmts[0].Cmd.(type) {
	case *syntax.BashPPCall:
		call = n
	case *syntax.BashPPReturn:
		call = n.Call
	}
	if call == nil || len(call.Fun) == 0 || call.CalleeExpr != nil || call.FuncLit != nil {
		return "", false, nil
	}
	// A method expression on a type parameter forwards through its first
	// parameter as the receiver; that closure has no callee to name.
	for _, group := range f.Params {
		for _, name := range group.Names {
			if name.Value == call.Fun[0].Value {
				return "", false, nil
			}
		}
	}
	typeargs, err := e.typeArgs(call.TypeArgs)
	if err != nil {
		return "", true, err
	}
	return strings.Join(names(call.Fun), ".") + typeargs, true, nil
}

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
//
// Positions are the adjusted ones the Go compiler would have reported for the
// original input: when the input's own line directive is in effect at a
// mapped position, the emitted directive reproduces the user's filename (from
// the source's LineDirectives table) and adjusted line. A position whose
// column is unknown — one governed by a line-only user directive — emits the
// line-only "//line file:N" form; a ":0" column would be rejected by the Go
// compiler. The map keeps both halves: Source/SourceOffset and Pos.Offset are
// physical, Pos line/column are adjusted.
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
			directive := false
			if adjusted, ok := adjustedSourceName(result.Sources, m.Pos.Offset()); ok {
				// The user's directive may legitimately clear the filename;
				// "//line :N" reproduces that (reported as "??").
				name, directive = adjusted, true
			}
			if strings.ContainsAny(name, "\r\n") {
				return fmt.Errorf("source filename cannot be represented in Go line directive")
			}
			if (name != "" || directive) && m.Pos.Line() > 0 {
				// A marker in doc-comment position (column 1) followed by a
				// directive is one comment group; gofmt separates the two
				// with a bare "//" line. Emit it so the output is gofmt-stable.
				if i > 0 && strings.HasPrefix(lines[i-1], "// lower:") {
					out.WriteString("//\n")
					physical++
				}
				if m.Pos.Col() > 0 {
					fmt.Fprintf(&out, "//line %s:%d:%d\n", name, m.Pos.Line(), m.Pos.Col())
				} else {
					fmt.Fprintf(&out, "//line %s:%d\n", name, m.Pos.Line())
				}
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

// adjustedSourceName returns the filename a line directive in the original
// input puts in effect at the global offset, when the covering source records
// one. The reported filename may be empty: a "//line :N" directive clears it.
func adjustedSourceName(sources []syntax.SourceFile, offset uint) (string, bool) {
	for _, src := range sources {
		if offset < src.Base || offset > src.Base+src.Size {
			continue
		}
		rel := offset - src.Base
		name, ok := "", false
		for _, d := range src.LineDirectives {
			if d.Offset > rel {
				break
			}
			name, ok = d.Filename, true
		}
		return name, ok
	}
	return "", false
}

// ValidateMappings rejects a result whose map no longer matches the generated
// source or the recorded inputs: every generated marker must have exactly one
// mapping, every mapping must anchor a nonempty generated line, and for Go
// input every mapping must resolve to the one recorded source containing its
// position. A consumer of the external map fails closed on a corrupted or
// truncated map instead of degrading to guessed positions.
func (r *Result) ValidateMappings() error {
	lines := strings.Split(string(r.Source), "\n")
	markers := 0
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "// lower:") {
			markers++
		}
	}
	if markers != len(r.Mappings) {
		return fmt.Errorf("source map: %d mappings for %d generated markers", len(r.Mappings), markers)
	}
	for i, m := range r.Mappings {
		if m.GoLine < 1 || m.GoLine > len(lines) {
			return fmt.Errorf("source map: mapping %d anchors line %d outside the generated source", i, m.GoLine)
		}
		line := lines[m.GoLine-1]
		if strings.TrimSpace(line) == "" {
			return fmt.Errorf("source map: mapping %d anchors empty generated line %d", i, m.GoLine)
		}
		if m.GoCol < 1 || m.GoCol > len(line)+1 {
			return fmt.Errorf("source map: mapping %d anchors column %d outside generated line %d", i, m.GoCol, m.GoLine)
		}
		if len(r.Sources) == 0 {
			continue
		}
		found := false
		for _, src := range r.Sources {
			if m.Pos.Offset() < src.Base || m.Pos.Offset() > src.Base+src.Size {
				continue
			}
			found = true
			if m.Source != src.Name {
				return fmt.Errorf("source map: mapping %d names %q but offset %d is in %q", i, m.Source, m.Pos.Offset(), src.Name)
			}
			if m.SourceOffset != m.Pos.Offset()-src.Base {
				return fmt.Errorf("source map: mapping %d file offset %d does not match position offset %d", i, m.SourceOffset, m.Pos.Offset()-src.Base)
			}
			break
		}
		if !found {
			return fmt.Errorf("source map: mapping %d offset %d resolves to no recorded source", i, m.Pos.Offset())
		}
	}
	return nil
}

func (e *emitter) goSourceReceiveOperand(recv *syntax.BashPPReceive) (string, error) {
	if recv.ChanExpr != nil {
		return e.expr(recv.ChanExpr)
	}
	return e.valueWord(recv.Chan)
}
