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

func (r *Runner) bashPPAssign(_ context.Context, assign *syntax.BashPPAssign) {
	if !r.objectsEnabled() || r.bashPPScope == nil {
		r.errf("bash++ assignment evaluated with extensions disabled\n")
		r.exit = exitStatus{code: 2}
		return
	}
	if assign.Call != nil {
		target := bashPPWordSource(assign.Target)
		cell := r.bashPPScope.lookup(target)
		if cell == nil || !syntax.ValidName(target) {
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
		*cell = *result
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
	if syntax.ValidName(target) && cell != nil && cell.pointer && assign.ValueExpr != nil {
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
	if !syntax.ValidName(target) || cell == nil || cell.object == nil || !cell.object.readonly {
		r.errf("bash++: mutation is only implemented for readonly objects\n")
		r.exit = exitStatus{code: 2}
		return
	}
	r.errf("BASHPP-EREADONLY-MUTATION: cannot assign to readonly value %q\n", cell.object.owner)
	r.exit = exitStatus{code: 2}
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
	value, ok := bashPPPathValue(cell.vr.Obj, parts)
	if !ok {
		return "", false
	}
	return fmt.Sprint(value), true
}

func bashPPParsePathText(text string) (string, []bashPPPathPart, bool) {
	i := 0
	for i < len(text) && (text[i] == '_' || text[i] >= 'a' && text[i] <= 'z' || text[i] >= 'A' && text[i] <= 'Z' || i > 0 && text[i] >= '0' && text[i] <= '9') {
		i++
	}
	root := text[:i]
	if !syntax.ValidName(root) {
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
			if !syntax.ValidName(field) {
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
