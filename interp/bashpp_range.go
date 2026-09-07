// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"fmt"
	"go/constant"
	"sort"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// bashPPRangeScalar handles the two non-container range operands whose values
// live in the scalar namespace: strings and integers. It deliberately leaves
// channels and structured values to their existing paths. Function iterators
// use the profile's deliberately erased `func` parameter type and the existing
// closure-handle transport; the yield arity is checked when it first runs.
func (r *Runner) bashPPRangeScalar(ctx context.Context, rng *syntax.BashPPRange) bool {
	if rng.Expr == nil {
		return false
	}
	if root, ok := bashPPCollectionRoot(rng.Expr); ok && r.bashPPScope != nil {
		if cell := r.bashPPScope.lookup(root); cell != nil {
			if cell.channel != nil || cell.vr.Kind == expand.Object || cell.pointer {
				return false
			}
			if fn, closure := r.bashPPClosure(cell.vr.String()); closure {
				return r.bashPPRangeFunction(ctx, rng, fn)
			}
		}
		if fn := r.bashPPFuncs[root]; fn != nil {
			return r.bashPPRangeFunction(ctx, rng, fn)
		}
	}

	value, err := r.bashPPEvalScalarExpr(rng.Expr)
	if err != nil {
		// An unresolved identifier may still be a channel operand. Preserve the
		// channel path's established task-group diagnostic in that case.
		if _, ok := rng.Expr.(*syntax.BashPPIdent); ok {
			return false
		}
		r.errf("BASHPP-ERANGE-TYPE: %v\n", err)
		r.exit = exitStatus{code: 2}
		return true
	}
	switch value.value.Kind() {
	case constant.String:
		text := constant.StringVal(value.value)
		for offset, runeValue := range text {
			if !r.bashPPRangeIteration(ctx, rng, offset, nil, int64(runeValue), nil,
				&syntax.BashPPNamedType{Name: &syntax.Lit{Value: "rune"}}) {
				return true
			}
		}
		return true
	case constant.Int:
		if len(rng.Names) > 1 {
			r.errf("BASHPP-ERANGE-ARITY: integer range permits at most one iteration variable\n")
			r.exit = exitStatus{code: 2}
			return true
		}
		limit, ok := constant.Int64Val(value.value)
		if !ok {
			r.errf("BASHPP-ERANGE-INTEGER: integer range bound is not representable as int64\n")
			r.exit = exitStatus{code: 2}
			return true
		}
		for i := int64(0); i < limit; i++ {
			if !r.bashPPRangeIteration(ctx, rng, i, nil, nil, nil, nil) {
				return true
			}
		}
		return true
	default:
		r.errf("BASHPP-ERANGE-TYPE: cannot range over %s\n", value.value.Kind())
		r.exit = exitStatus{code: 2}
		return true
	}
}

func (r *Runner) bashPPRangeFunction(ctx context.Context, rng *syntax.BashPPRange, generator *bashPPFunc) bool {
	params := bashppParams(generator.params())
	if len(params) != 1 || params[0].declared != "func" || len(generator.results()) != 0 {
		r.errf("BASHPP-ERANGE-FUNC: range function must take one func yield parameter and return no values\n")
		r.exit = exitStatus{code: 2}
		return true
	}
	stopped := false
	yieldArity := -1
	yield := &bashPPFunc{bound: "range yield"}
	yield.native = func(yieldCtx context.Context, args []string) []string {
		if stopped {
			r.errf("BASHPP-ERANGE-FUNC: range function called yield after it returned false\n")
			r.exit = exitStatus{code: 2}
			return []string{"false"}
		}
		if len(args) < 1 || len(args) > 2 {
			r.errf("BASHPP-ERANGE-FUNC: yield must provide one or two values\n")
			r.exit = exitStatus{code: 2}
			stopped = true
			return []string{"false"}
		}
		if yieldArity < 0 {
			yieldArity = len(args)
		} else if yieldArity != len(args) {
			r.errf("BASHPP-ERANGE-FUNC: yield value count changed from %d to %d\n", yieldArity, len(args))
			r.exit = exitStatus{code: 2}
			stopped = true
			return []string{"false"}
		}
		if len(rng.Names) > len(args) {
			r.errf("BASHPP-ERANGE-ARITY: range declares %d variable(s) for %d yielded value(s)\n", len(rng.Names), len(args))
			r.exit = exitStatus{code: 2}
			stopped = true
			return []string{"false"}
		}
		var second any
		if len(args) == 2 {
			second = args[1]
		}
		keepGoing := r.bashPPRangeIteration(yieldCtx, rng, args[0], nil, second, nil, nil)
		if !keepGoing {
			stopped = true
		}
		r.exit.clear()
		return []string{fmt.Sprint(keepGoing)}
	}
	handle := r.bashPPStoreFunc(yield)
	r.bashPPInvoke(ctx, generator, []string{handle.Str})
	return true
}

func (r *Runner) bashPPRangeCollection(ctx context.Context, rng *syntax.BashPPRange) bool {
	if rng.Expr == nil || r.bashPPScope == nil {
		return false
	}
	root, ok := bashPPCollectionRoot(rng.Expr)
	if !ok {
		return false
	}
	cell := r.bashPPScope.lookup(root)
	if cell == nil || cell.channel != nil || cell.vr.Kind != expand.Object && !cell.pointer {
		return false
	}
	value, meta, err := r.bashPPReadExpr(rng.Expr)
	if err != nil {
		r.errf("%v\n", err)
		r.exit = exitStatus{code: 2}
		return true
	}
	if meta == nil || meta.kind == "struct" || meta.kind == "pointer" {
		r.errf("BASHPP-ERANGE-TYPE: cannot range over %s\n", bashPPTypeText(meta.typ))
		r.exit = exitStatus{code: 2}
		return true
	}
	collection, ok := r.bashPPUnderlyingType(meta.typ).(*syntax.BashPPCollectionType)
	if !ok {
		r.errf("BASHPP-ERANGE-TYPE: cannot range over %s\n", bashPPTypeText(meta.typ))
		r.exit = exitStatus{code: 2}
		return true
	}
	switch meta.kind {
	case "array", "inferred-array":
		value, meta = bashPPCopyArrayValue(value, meta)
		sequence := value.([]any)
		for i, elem := range sequence {
			if !r.bashPPRangeIteration(ctx, rng, i, nil, elem, meta.sequence[i], collection.Element) {
				return true
			}
		}
	case "slice":
		sequence, _ := value.([]any)
		length := len(sequence)
		for i := 0; i < length; i++ {
			if !r.bashPPRangeIteration(ctx, rng, i, nil, sequence[i], meta.sequence[i], collection.Element) {
				return true
			}
		}
	case "map":
		mapping, _ := value.(map[string]any)
		keys := make([]string, 0, len(mapping))
		for key := range mapping {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			item, exists := mapping[key]
			if !exists {
				continue
			}
			if !r.bashPPRangeIteration(ctx, rng, key, collection.Key, item, meta.mapping[key], collection.Element) {
				return true
			}
		}
	}
	return true
}

func (r *Runner) bashPPRangeIteration(ctx context.Context, rng *syntax.BashPPRange, key any, keyType syntax.BashPPTypeExpr, value any, valueMeta *bashPPCollectionMeta, valueType syntax.BashPPTypeExpr) bool {
	leave := r.bashPPPushScope()
	if len(rng.Names) >= 1 && rng.Names[0].Value != "_" {
		r.bashPPDeclareRangeValue(rng.Names[0].Value, key, keyType, nil)
	}
	if len(rng.Names) == 2 && rng.Names[1].Value != "_" {
		value, valueMeta = bashPPCopyArrayValue(value, valueMeta)
		r.bashPPDeclareRangeValue(rng.Names[1].Value, value, valueType, valueMeta)
	}
	r.cmd(r.bashPPTaskContext(ctx), rng.Body)
	leave()
	if r.exit.exiting || r.exit.returning || r.exit.fatalExit || r.loopControlPending() {
		return false
	}
	switch r.bashPPBranch {
	case bashPPBranchBreak:
		r.bashPPBranch = bashPPBranchNone
		r.exit.clear()
		return false
	case bashPPBranchContinue:
		r.bashPPBranch = bashPPBranchNone
		r.exit.clear()
	case bashPPBranchFallthrough:
		panic("validated fallthrough escaped to Bash++ range")
	}
	return true
}

func (r *Runner) bashPPDeclareRangeValue(name string, value any, typ syntax.BashPPTypeExpr, meta *bashPPCollectionMeta) {
	if meta != nil && meta.kind == "pointer" {
		r.bashPPDeclareName(name, expand.Variable{Set: true, Kind: expand.String})
		cell := r.bashPPScope.lookup(name)
		cell.declType = typ
		bashPPStoreCellValue(cell, value, meta)
		return
	}
	if meta != nil {
		r.bashPPDeclareName(name, expand.NewObject(value))
		cell := r.bashPPScope.lookup(name)
		cell.object = &bashPPObjectIdentity{owner: name, collection: meta}
		cell.valueMeta = meta
		return
	}
	r.bashPPDeclareName(name, expand.Variable{Set: true, Kind: expand.String, Str: fmt.Sprint(value)})
}
