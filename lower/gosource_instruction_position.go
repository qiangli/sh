package lower

import "mvdan.cc/sh/v3/syntax"

// goSourceInstructionPos identifies the instruction whose runtime fault a Go
// stack frame reports. Bounds checks belong to the '[' token, while an
// interface method lookup belongs to the selected method identifier. Keeping
// this position on the generated statement lets the Go backend reproduce the
// compiler's PC position without rewriting tracebacks.
func goSourceInstructionPos(command syntax.Command) syntax.Pos {
	if command == nil {
		return syntax.Pos{}
	}
	exprPos := func(expr syntax.BashPPExpr) syntax.Pos {
		switch x := expr.(type) {
		case *syntax.BashPPIndexExpr:
			return x.Lbrack
		case *syntax.BashPPSliceExpr:
			return x.Lbrack
		case *syntax.BashPPSelectorExpr:
			return x.Sel.Pos()
		}
		return syntax.Pos{}
	}
	switch x := command.(type) {
	case *syntax.BashPPCall:
		if pos := exprPos(x.CalleeExpr); pos.IsValid() {
			return pos
		}
		if len(x.Fun) > 1 {
			return x.Fun[len(x.Fun)-1].Pos()
		}
	case *syntax.BashPPAssign:
		if pos := exprPos(x.TargetExpr); pos.IsValid() {
			return pos
		}
		if pos := exprPos(x.ValueExpr); pos.IsValid() {
			return pos
		}
	case *syntax.BashPPShortDecl:
		if pos := exprPos(x.Expr); pos.IsValid() {
			return pos
		}
	}
	return command.Pos()
}
