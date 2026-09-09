package interp

import (
	"fmt"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// bashPPGoSourceTupleCall evaluates an interpreted callable exactly once and
// retains every result cell for Go's sole-multi-value-argument rule.
func (r *Runner) bashPPGoSourceTupleCall(call *syntax.BashPPCall) ([]*bashPPCell, error) {
	fn, ok := r.bashPPLookupFunc(call)
	if !ok {
		return nil, fmt.Errorf("gosource: undefined interpreted callable")
	}
	return r.goSourceCallResultCells(call, fn)
}

func (r *Runner) goSourceCallResultCells(call *syntax.BashPPCall, fn *bashPPFunc) ([]*bashPPCell, error) {
	var ok bool
	var args []string
	if call.ArgExprs != nil {
		var err error
		if args, ok, err = r.bashPPTypedCallArgs(call, fn); err != nil {
			return nil, err
		}
	} else {
		args, ok = r.bashPPCallValues(call, fn)
	}
	if !ok {
		return nil, errBashPPScalarInterrupted
	}
	previous := r.bashPPResultCells
	defer func() { r.bashPPResultCells = previous }()
	failure := r.bashPPShortFailureSeq
	values := r.bashPPInvoke(r.ectx, fn, args)
	if r.bashPPPanicking() || r.exit.exiting || r.exit.fatalExit || r.exit.err != nil || r.bashPPShortFailureSeq != failure || len(values) != len(r.bashPPResultCells) {
		return nil, errBashPPScalarInterrupted
	}
	results := make([]*bashPPCell, len(values))
	for i, c := range r.bashPPResultCells {
		results[i] = bashPPCopyAssignmentCell(c)
	}
	return results, nil
}

// bashPPGoSourceChanCall answers the channel builtins a Go region spells as an
// ordinary call. `close(ch)` has no shell spelling to recognize at parse time,
// so it arrives as a call node; it is the channel operation only where the
// session declared nothing of that name, exactly as a Go declaration shadows a
// predeclared identifier.
func (r *Runner) bashPPGoSourceChanCall(c *syntax.BashPPCall) bool {
	if !r.bashPPGoSource || len(c.Fun) != 1 || c.Fun[0].Value != "close" || len(c.Args) != 1 {
		return false
	}
	if r.bashPPFuncs["close"] != nil || (r.bashPPScope != nil && r.bashPPScope.lookup("close") != nil) {
		return false
	}
	r.bashPPClose(&syntax.BashPPClose{Kw: c.Fun[0], Chan: c.Args[0], Lparen: c.Lparen, Rparen: c.Rparen})
	return true
}

// bashPPGoSourceEvaluatedCall returns call with every computed scalar argument
// replaced by the value it evaluates to. A Go region carries an argument as its
// original expression text, so `f(cap(c))` would otherwise bind the string
// "cap(c)". Only an argument the scalar runtime can answer is rewritten;
// anything else — an identifier, a channel, a slice expression — keeps the word
// the established binding already understands.
//
// The rewrite is also the evaluation order `go f(...)` requires: Go evaluates a
// launched call's arguments in the goroutine that launches it, not in the task.
func (r *Runner) bashPPGoSourceEvaluatedCall(call *syntax.BashPPCall) *syntax.BashPPCall {
	if !r.bashPPGoSource || call == nil || len(call.ArgExprs) != len(call.Args) {
		return call
	}
	out := call
	for i, expr := range call.ArgExprs {
		if !bashPPGoComputedArg(expr, call.Args[i]) {
			continue
		}
		replacement, ok := r.bashPPGoArgWord(call, i, expr)
		if !ok {
			continue
		}
		if out == call {
			copied := *call
			copied.Args = append([]*syntax.Word(nil), call.Args...)
			out = &copied
		}
		out.Args[i] = replacement
	}
	if out != call {
		// This clone carries values prepared in the launching goroutine.
		// Original expression trees must not execute again in the child.
		out.ArgExprs = nil
	}
	return out
}

// bashPPGoArgWord answers the word that carries an evaluated argument.
//
// A scalar becomes its own quoted value. A collection cannot be spelled as a
// word at all, so it is bound to a reserved name derived from the call site and
// the argument's position — a name no Go source can spell, and one that is
// reused rather than accumulated when the same site runs again — and the word
// becomes that name, which is the spelling the established argument binding
// already resolves to a cell.
func (r *Runner) bashPPGoArgWord(call *syntax.BashPPCall, i int, expr syntax.BashPPExpr) (*syntax.Word, bool) {
	word := call.Args[i]
	if value, err := r.bashPPEvalScalarExpr(expr); err == nil && value.value != nil {
		return &syntax.Word{Parts: []syntax.WordPart{&syntax.SglQuoted{
			Left: word.Pos(), Right: word.End(), Value: bashPPScalarString(value.value),
		}}}, true
	}
	switch expr.(type) {
	case *syntax.BashPPIndexExpr, *syntax.BashPPSliceExpr:
	default:
		return nil, false
	}
	value, meta, err := r.bashPPReadExpr(expr)
	if err != nil || r.bashPPScope == nil {
		return nil, false
	}
	name := fmt.Sprintf("bashPPGoArg_%d_%d", uint(call.Pos().Offset()), i)
	if meta == nil {
		r.bashPPDeclareName(name, expand.Variable{Set: true, Kind: expand.String, Str: fmt.Sprint(value)})
	} else {
		// A slice shares the array it was taken from; its meta carries the
		// bounds. Re-basing the value would strand that meta, so the binding
		// keeps the value and identity the read produced.
		r.bashPPDeclareName(name, expand.NewObject(value))
		cell := r.bashPPScope.lookup(name)
		if cell == nil {
			return nil, false
		}
		cell.object = &bashPPObjectIdentity{owner: name, collection: meta}
		if root, ok := bashPPCollectionRoot(expr); ok {
			if source := r.bashPPScope.lookup(root); source != nil && source.object != nil {
				cell.object = source.object
			}
		}
		cell.valueMeta = meta
	}
	return &syntax.Word{Parts: []syntax.WordPart{&syntax.Lit{
		Value: name, ValuePos: word.Pos(), ValueEnd: word.End(),
	}}}, true
}

// bashPPGoComputedArg reports an argument whose word text is not already its
// own value. A bare identifier keeps its established meaning — the binding path
// resolves it, and a channel argument must reach that path by name to carry its
// capability — so only a genuinely computed operand is evaluated here.
func bashPPGoComputedArg(expr syntax.BashPPExpr, word *syntax.Word) bool {
	if expr == nil || word == nil || len(word.Parts) != 1 {
		return false
	}
	if _, ok := word.Parts[0].(*syntax.Lit); !ok {
		return false
	}
	switch expr.(type) {
	case *syntax.BashPPIdent, *syntax.BashPPBasicLit:
		return false
	}
	return true
}
