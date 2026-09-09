// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// bashPPObjectIdentity is shared by the cells created when an object is
// aliased. Marking any root readonly freezes every path through that object.
type bashPPObjectIdentity struct {
	owner      string
	readonly   bool
	collection *bashPPCollectionMeta
}

type bashPPPathPart struct {
	field string
	index any
	text  string
}

func bashPPWordSource(w *syntax.Word) string {
	var b strings.Builder
	_ = syntax.NewPrinter().Print(&b, w)
	return b.String()
}

func bashPPPathValue(value any, parts []bashPPPathPart) (any, bool) {
	for _, part := range parts {
		switch current := value.(type) {
		case map[string]any:
			key := part.field
			if key == "" {
				key, _ = part.index.(string)
			}
			value = current[key]
		case []any:
			i, ok := part.index.(int)
			if !ok || i < 0 || i >= len(current) {
				return nil, false
			}
			value = current[i]
		default:
			return nil, false
		}
	}
	return value, true
}

func (r *Runner) bashPPAssign(ctx context.Context, assign *syntax.BashPPAssign) {
	if !r.objectsEnabled() || r.bashPPScope == nil {
		r.errf("bash++ assignment evaluated with extensions disabled\n")
		r.exit = exitStatus{code: 2}
		return
	}
	if r.bashPPGoSource && assign.Call != nil {
		switch assign.TargetExpr.(type) {
		case *syntax.BashPPIndexExpr, *syntax.BashPPSelectorExpr, *syntax.BashPPDerefExpr:
			r.bashPPStructuredAssign(assign.TargetExpr, assign.Call)
			return
		}
	}
	if len(assign.Names) > 0 {
		if assign.Call != nil {
			r.bashPPTupleAssignCall(ctx, assign)
			return
		}
		r.bashPPTupleAssign(assign)
		return
	}
	if assign.Call != nil {
		r.bashPPBuiltinAssign(assign)
		return
	}
	if assign.TargetExpr != nil {
		if deref, ok := assign.TargetExpr.(*syntax.BashPPDerefExpr); ok {
			r.bashPPDerefAssign(deref, assign.ValueExpr)
			return
		}
		r.bashPPStructuredAssign(assign.TargetExpr, assign.ValueExpr)
		return
	}
	target := bashPPWordSource(assign.Target)
	cell := r.bashPPScope.lookup(target)
	if syntax.BashPPValidIdent(target) && cell != nil && cell.pointer && assign.ValueExpr != nil {
		value, meta, err := r.bashPPEvalTypedValue(assign.ValueExpr, cell.declType)
		if err != nil {
			r.errf("BASHPP-EASSIGN-MISMATCH: %v\n", err)
			r.exit = exitStatus{code: 2}
			return
		}
		if cell.constant || cell.vr.ReadOnly {
			r.errf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value through pointer\n")
			r.exit = exitStatus{code: 2}
			return
		}
		bashPPStoreCellValue(cell, value, meta)
		return
	}
	if !syntax.BashPPValidIdent(target) || cell == nil || cell.object == nil || !cell.object.readonly {
		r.errf("bash++: mutation is only implemented for readonly objects\n")
		r.exit = exitStatus{code: 2}
		return
	}
	r.errf("BASHPP-EREADONLY-MUTATION: cannot assign to readonly value %q\n", cell.object.owner)
	r.exit = exitStatus{code: 2}
}

// bashPPBuiltinAssign stores the single value a predeclared value builtin
// produces into an already declared target, keeping the result cell whole so
// that `s = append(s, 0)` carries the slice's payload, metadata and identity
// rather than a scalar spelling of it.
func (r *Runner) bashPPBuiltinAssign(assign *syntax.BashPPAssign) {
	target := bashPPWordSource(assign.Target)
	if r.bashPPGoSource && target == "_" && assign.Call != nil && bashPPPredeclaredCall(assign.Call) == "make" {
		if _, ok := assign.Call.ArgType.(*syntax.BashPPChanType); ok {
			r.bashPPRunValueBuiltin("make", assign.Call)
			return
		}
	}
	cell := r.bashPPScope.lookup(target)
	if cell == nil || !syntax.BashPPValidIdent(target) {
		r.bashPPBuiltinError("TYPE", "assignment target %q is not declared", target)
		return
	}
	if cell.constant || cell.vr.ReadOnly {
		r.errf("BASHPP-EREADONLY-MUTATION: cannot assign to readonly value %q\n", target)
		r.exit = exitStatus{code: 2}
		return
	}
	name := bashPPPredeclaredCall(assign.Call)
	if !bashPPValueBuiltin(name) {
		r.bashPPBuiltinError("TYPE", "assignment call %s is not a supported value builtin", name)
		return
	}
	result, produced := r.bashPPRunValueBuiltin(name, assign.Call)
	if !produced || result == nil {
		if r.exit.code == 0 {
			r.bashPPBuiltinError("ARITY", "%s produces no value", name)
		}
		return
	}
	owner := cell.object
	*cell = *result
	// The target keeps naming its own storage: append that reused the backing
	// array returns the source identity, and one that reallocated returns a
	// fresh one, but either way this variable is what owns it here.
	if cell.object != nil && cell.object.owner == "" && owner != nil {
		cell.object.owner = owner.owner
	}
}

func (r *Runner) bashPPTupleAssignCall(ctx context.Context, assign *syntax.BashPPAssign) {
	if r.bashPPGoSource && r.bashPPBridgeHandles(assign.Call) {
		r.goSourceNativeAssignCall(ctx, assign)
		return
	}
	fn, ok := r.bashPPLookupFunc(assign.Call)
	if !ok {
		// `s = append(s, 0)`: the Go front end records the single target in
		// Names too, so a one-target assignment whose RHS is a predeclared
		// value builtin arrives here rather than at the single-target path. It
		// produces one value, not a tuple, and a declared function of the same
		// name has already been preferred by the lookup above.
		if len(assign.Names) == 1 && bashPPValueBuiltin(bashPPPredeclaredCall(assign.Call)) {
			r.bashPPBuiltinAssign(assign)
			return
		}
		r.errf("%sBASHPP-EASSIGN-CALL: tuple assignment requires a declared result-bearing function\n", r.bashErrPrefix(assign.Call.Pos()))
		r.exit = exitStatus{code: 2}
		return
	}
	args, ok := r.bashPPCallValues(assign.Call, fn)
	if !ok {
		return
	}
	failureMark := r.bashPPShortFailureSeq
	results := r.bashPPInvoke(ctx, fn, args)
	resultCells := r.bashPPResultCells
	if r.bashPPPanicking() || r.exit.exiting || r.exit.fatalExit || r.exit.err != nil || r.bashPPShortFailureSeq != failureMark {
		return
	}
	if len(assign.Names) != len(results) {
		r.errf("%sBASHPP-EASSIGN-ARITY: %d variable(s) but %d value(s)\n",
			r.bashErrPrefix(assign.Eq), len(assign.Names), len(results))
		r.exit = exitStatus{code: 2}
		return
	}
	candidates := make([]*bashPPCell, len(results))
	for i, result := range results {
		if i < len(resultCells) && resultCells[i] != nil {
			candidates[i] = bashPPCopyAssignmentCell(resultCells[i])
		} else {
			candidates[i] = &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: result}}
		}
	}
	r.bashPPCommitTupleAssign(assign, candidates)
}

func (r *Runner) bashPPTupleAssign(assign *syntax.BashPPAssign) {
	if r.goSourceReceiveAssign(assign) {
		return
	}
	if len(assign.Values) == 0 || len(assign.ValueExprs) != len(assign.Values) {
		pos := assign.Eq
		if len(assign.Values) > 0 && assign.Values[0] != nil {
			pos = assign.Values[0].Pos()
		}
		r.errf("%sBASHPP-EASSIGN-FORM: tuple RHS is not a supported comma-delimited scalar expression list\n", r.bashErrPrefix(pos))
		r.exit = exitStatus{code: 2}
		return
	}
	if len(assign.Names) != len(assign.Values) {
		r.errf("%sBASHPP-EASSIGN-ARITY: %d variable(s) but %d value(s)\n",
			r.bashErrPrefix(assign.Eq), len(assign.Names), len(assign.Values))
		r.exit = exitStatus{code: 2}
		return
	}
	candidates := make([]*bashPPCell, len(assign.Values))
	for i, expr := range assign.ValueExprs {
		if expr == nil {
			r.errf("%sBASHPP-EASSIGN-FORM: tuple RHS %d is not a supported scalar expression\n",
				r.bashErrPrefix(assign.Values[i].Pos()), i+1)
			r.exit = exitStatus{code: 2}
			return
		}
		// `f = i.(float64)` is an assertion, not a scalar expression, and its
		// one-result form panics rather than yielding a value when it fails.
		if cell, handled, err := r.bashPPAssertCandidate(expr); handled {
			if err != nil {
				r.errf("%s%v\n", r.bashErrPrefix(expr.Pos()), err)
				r.exit = exitStatus{code: 2}
				r.exit.fatal(ExitStatus(2))
				return
			}
			if r.exit.code != 0 {
				return
			}
			candidates[i] = cell
			continue
		}
		// An interface-typed target owns the dynamic type of what it is
		// given, so the value is built against the target's declared
		// interface rather than read as a plain scalar.
		if name := assign.Names[i]; name.Value != "_" {
			cell, handled, err := r.bashPPInterfaceAssignCandidate(r.bashPPScope.lookup(name.Value), expr)
			if err != nil {
				r.errf("%s%v\n", r.bashErrPrefix(expr.Pos()), err)
				r.exit = exitStatus{code: 2}
				return
			}
			if handled {
				candidates[i] = cell
				continue
			}
		}
		if r.bashPPGoSource && r.bashPPNativeExpr(expr) {
			value, err := r.bashPPBridgeExpr(expr)
			if err != nil {
				r.exit.fatal(err)
				return
			}
			candidates[i] = goSourceNativeValueCell(value)
			continue
		}
		if candidate := r.goSourceNativeNilCandidate(r.bashPPScope.lookup(assign.Names[i].Value), expr); candidate != nil {
			candidates[i] = candidate
			continue
		}
		if ident, ok := expr.(*syntax.BashPPIdent); ok {
			source := r.bashPPScope.lookup(ident.Name.Value)
			if source == nil {
				r.errf("%sBASHPP-EASSIGN-UNDECLARED: RHS %s is not declared\n", r.bashErrPrefix(ident.Pos()), ident.Name.Value)
				r.exit = exitStatus{code: 2}
				return
			}
			candidates[i] = bashPPCopyAssignmentCell(source)
			continue
		}
		// `s = s[:0]`, `row = board[i]`: a RHS that reads structured storage has
		// no scalar spelling, so it is assigned as the value it is. A scalar
		// read returns no cell and falls through to the scalar evaluator, which
		// keeps owning that diagnostic.
		if cell, err := r.bashPPStructuredArgCell(assign.Values[i], expr); err == nil && cell != nil {
			candidates[i] = cell
			continue
		}
		value, err := r.bashPPEvalScalarExpr(expr)
		if err != nil {
			r.errf("%s%v\n", r.bashErrPrefix(expr.Pos()), err)
			r.exit = exitStatus{code: 2}
			return
		}
		cell := &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: bashPPScalarString(value.value)}, scalarKind: value.value.Kind()}
		if value.typ != "" {
			cell.declType = &syntax.BashPPNamedType{Name: &syntax.Lit{Value: value.typ}}
			cell.typeName = value.typ
		}
		candidates[i] = cell
	}
	r.bashPPCommitTupleAssign(assign, candidates)
}

// bashPPCopyAssignmentCell snapshots the complete source cell while retaining
// Go's value/reference distinction: arrays and structs copy their payload,
// whereas slices, maps, channels, pointers, interfaces, and closures retain
// their reference identity and authoritative runtime metadata.
func bashPPCopyAssignmentCell(source *bashPPCell) *bashPPCell {
	if source == nil {
		return nil
	}
	copyCell := *source
	if source.vr.Kind == expand.Object && source.vr.Obj != nil && source.object != nil && bashPPValueMeta(bashPPCellMeta(source)) {
		value, meta := bashPPCopyArrayValue(source.vr.Obj, bashPPCellMeta(source))
		copyCell.vr = expand.NewObject(value)
		copyCell.valueMeta = meta
	}
	return &copyCell
}

func (r *Runner) bashPPCommitTupleAssign(assign *syntax.BashPPAssign, candidates []*bashPPCell) {
	targets := make([]*bashPPCell, len(assign.Names))
	for i, name := range assign.Names {
		if name.Value == "_" {
			continue
		}
		target := r.bashPPScope.lookup(name.Value)
		if target == nil {
			r.errf("%sBASHPP-EASSIGN-UNDECLARED: assignment target %s is not declared\n", r.bashErrPrefix(name.Pos()), name.Value)
			r.exit = exitStatus{code: 2}
			return
		}
		if target.constant || target.vr.ReadOnly {
			r.errf("%sBASHPP-EASSIGN-CONST: cannot assign to %s\n", r.bashErrPrefix(name.Pos()), name.Value)
			r.exit = exitStatus{code: 2}
			return
		}
		if err := r.bashPPValidateReusedShortValue(target, candidates[i]); err != nil {
			r.errf("%s%v\n", r.bashErrPrefix(name.Pos()), err)
			r.exit = exitStatus{code: 2}
			return
		}
		targets[i] = target
	}
	for i, target := range targets {
		if target == nil {
			continue
		}
		declType, typeName := target.declType, target.typeName
		constantBinding := target.constant
		readonlyBinding, exportedBinding := target.vr.ReadOnly, target.vr.Exported
		*target = *candidates[i]
		target.declType, target.typeName = declType, typeName
		target.constant = constantBinding
		target.vr.ReadOnly, target.vr.Exported = readonlyBinding, exportedBinding
	}
	r.exit.clear()
}

func (r *Runner) bashPPResolveWord(w *syntax.Word) (string, bool) {
	if !r.objectsEnabled() || r.bashPPScope == nil {
		return "", false
	}
	root, parts, ok := bashPPParsePathText(bashPPWordSource(w))
	if !ok || len(parts) == 0 {
		return "", false
	}
	cell := r.bashPPScope.lookup(root)
	if cell == nil || cell.vr.Kind != expand.Object {
		return "", false
	}
	value := cell.vr.Obj
	meta := bashPPCellMeta(cell)
	for _, part := range parts {
		if part.field != "" {
			if meta == nil {
				mapping, ok := value.(map[string]any)
				if !ok {
					return "", false
				}
				value = mapping[part.field]
				continue
			}
			sel := r.bashPPResolveField(meta.typ, part.field)
			if sel.ambiguous || len(sel.edges) == 0 {
				return "", false
			}
			var err error
			value, meta, err = bashPPReadSelection(value, meta, sel.edges)
			if err != nil {
				return "", false
			}
			continue
		}
		switch current := value.(type) {
		case map[string]any:
			key, _ := part.index.(string)
			value = current[key]
			if meta != nil {
				meta = meta.mapping[key]
			}
		case []any:
			i, ok := part.index.(int)
			if !ok || i < 0 || i >= len(current) {
				return "", false
			}
			value = current[i]
			if meta != nil {
				meta = meta.sequence[i]
			}
		default:
			return "", false
		}
	}
	return fmt.Sprint(value), true
}

func bashPPParsePathText(text string) (string, []bashPPPathPart, bool) {
	i := 0
	for i < len(text) && (text[i] == '_' || text[i] >= 'a' && text[i] <= 'z' || text[i] >= 'A' && text[i] <= 'Z' || i > 0 && text[i] >= '0' && text[i] <= '9') {
		i++
	}
	root := text[:i]
	if !syntax.BashPPValidIdent(root) {
		return "", nil, false
	}
	var parts []bashPPPathPart
	for i < len(text) {
		switch text[i] {
		case '.':
			start := i
			i++
			fieldStart := i
			for i < len(text) && (text[i] == '_' || text[i] >= 'a' && text[i] <= 'z' || text[i] >= 'A' && text[i] <= 'Z' || i > fieldStart && text[i] >= '0' && text[i] <= '9') {
				i++
			}
			field := text[fieldStart:i]
			if !syntax.BashPPValidIdent(field) {
				return "", nil, false
			}
			parts = append(parts, bashPPPathPart{field: field, text: text[start:i]})
		case '[':
			start := i
			close := strings.IndexByte(text[i:], ']')
			if close < 0 {
				return "", nil, false
			}
			close += i
			raw := text[i+1 : close]
			var index any
			if quoted, err := strconv.Unquote(raw); err == nil {
				index = quoted
			} else if number, err := strconv.Atoi(raw); err == nil {
				index = number
			} else {
				return "", nil, false
			}
			i = close + 1
			parts = append(parts, bashPPPathPart{index: index, text: text[start:i]})
		default:
			return "", nil, false
		}
	}
	return root, parts, len(parts) > 0
}

func (r *Runner) bashPPShortDeclImported(ctx context.Context, d *syntax.BashPPShortDecl) bool {
	if d.Call == nil || len(d.Call.Fun) < 2 {
		return false
	}
	if _, imported := r.bashPPImports[d.Call.Fun[0].Value]; !imported {
		return false
	}
	evaluator, ok := r.bashPPTools.eval.(bashPPValuesEvaluator)
	if !ok {
		r.errf("bash++: selected evaluator cannot return object values\n")
		r.exit = exitStatus{code: 2}
		return true
	}
	req, err := r.bashPPEvalRequest()
	if err == nil {
		req.Results = len(d.Lhs)
		req.Selector = make([]string, len(d.Call.Fun))
		for i, part := range d.Call.Fun {
			req.Selector[i] = part.Value
		}
		req.Args = make([]string, len(d.Call.Args))
		for i, arg := range d.Call.Args {
			req.Args[i] = bashPPWordSource(arg)
		}
		var values []any
		values, err = evaluator.Values(ctx, req)
		if err == nil {
			if len(values) != len(d.Lhs) {
				err = fmt.Errorf("assignment mismatch: %d variable(s) but %d value(s)", len(d.Lhs), len(values))
			} else {
				for i, lhs := range d.Lhs {
					if lhs.Value == "_" {
						continue
					}
					if validateErr := expand.ValidObject(values[i]); validateErr != nil {
						err = validateErr
						break
					}
				}
			}
			if err == nil {
				for i, lhs := range d.Lhs {
					if lhs.Value == "_" {
						continue
					}
					r.bashPPDeclareName(lhs.Value, expand.NewObject(values[i]))
					r.bashPPScope.lookup(lhs.Value).object = &bashPPObjectIdentity{owner: lhs.Value}
				}
				return true
			}
		}
	}
	if err != nil {
		r.errf("%v\n", err)
		r.exit = exitStatus{code: 2}
	}
	return true
}
