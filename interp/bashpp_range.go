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
// are rejected explicitly: the current erased `func` type cannot encode Go's
// required func(func(...) bool) signature, so executing one would be an unsafe
// second semantics rather than Go-compatible range.
func (r *Runner) bashPPRangeScalar(ctx context.Context, rng *syntax.BashPPRange) bool {
	if rng.Expr == nil {
		return false
	}
	if root, ok := bashPPCollectionRoot(rng.Expr); ok && r.bashPPScope != nil {
		cell := r.bashPPScope.lookup(root)
		if cell != nil {
			if cell.channel != nil {
				return false
			}
			if _, closure := r.bashPPClosure(cell.vr.String()); closure {
				r.bashPPRangeError(rng, "BASHPP-ERANGE-FUNC: range over function requires a typed func(func(...) bool) iterator")
				return true
			}
		}
		if cell == nil && r.bashPPFuncs[root] != nil {
			r.bashPPRangeError(rng, "BASHPP-ERANGE-FUNC: range over function requires a typed func(func(...) bool) iterator")
			return true
		}
	}

	// A dependency-owned sequence is read through its handle; see
	// bashPPNativeRange in bashpp_native_access.go.
	if r.bashPPNativeRange(ctx, rng) {
		return true
	}

	value, err := r.bashPPEvalScalarExpr(rng.Expr)
	if err != nil {
		r.bashPPRangeError(rng, "BASHPP-ERANGE-TYPE: %v", err)
		return true
	}
	switch value.value.Kind() {
	case constant.String:
		text := constant.StringVal(value.value)
		for offset, runeValue := range text {
			if !r.bashPPRangeIteration(ctx, rng, offset, bashPPRangeNamedType("int"), int64(runeValue), nil,
				&syntax.BashPPNamedType{Name: &syntax.Lit{Value: "rune"}}) {
				return true
			}
		}
		return true
	case constant.Int:
		if len(rng.Names) > 1 {
			r.bashPPRangeError(rng, "BASHPP-ERANGE-ARITY: integer range permits at most one iteration variable")
			return true
		}
		limit, ok := constant.Int64Val(value.value)
		if !ok {
			r.bashPPRangeError(rng, "BASHPP-ERANGE-INTEGER: integer range bound is not representable as int64")
			return true
		}
		iterationType := bashPPRangeNamedType("int")
		if value.typ != "" {
			iterationType = bashPPRangeNamedType(value.typ)
		}
		if ident, ok := rng.Expr.(*syntax.BashPPIdent); ok && r.bashPPScope != nil {
			if cell := r.bashPPScope.lookup(ident.Name.Value); cell != nil && cell.declType != nil {
				iterationType = cell.declType
			}
		}
		for i := int64(0); i < limit; i++ {
			if !r.bashPPRangeIteration(ctx, rng, i, iterationType, nil, nil, nil) {
				return true
			}
		}
		return true
	default:
		r.bashPPRangeError(rng, "BASHPP-ERANGE-TYPE: cannot range over %s", value.value.Kind())
		return true
	}
}

func bashPPRangeNamedType(name string) syntax.BashPPTypeExpr {
	return &syntax.BashPPNamedType{Name: &syntax.Lit{Value: name}}
}

func (r *Runner) bashPPRangeError(rng *syntax.BashPPRange, format string, args ...any) {
	pos := rng.Range
	if rng.Expr != nil {
		pos = rng.Expr.Pos()
	}
	r.errf("%s%s\n", r.bashErrPrefix(pos), fmt.Sprintf(format, args...))
	r.exit = exitStatus{code: 2}
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
	if _, native := r.goSourceNativeChannel(cell); native {
		return false
	}
	value, meta, err := r.bashPPReadExpr(rng.Expr)
	if err != nil {
		r.bashPPRangeError(rng, "%v", err)
		return true
	}
	// A path rooted in a collection can still select an ordinary scalar, for
	// example matrix[0][0] or cfg.Limit. Let the scalar range path evaluate
	// those values; metadata is only present for structured results.
	if meta == nil {
		return false
	}
	if meta.kind == "struct" || meta.kind == "pointer" {
		r.bashPPRangeError(rng, "BASHPP-ERANGE-TYPE: cannot range over %s", bashPPTypeText(meta.typ))
		return true
	}
	collection, ok := r.bashPPUnderlyingType(meta.typ).(*syntax.BashPPCollectionType)
	if !ok {
		r.bashPPRangeError(rng, "BASHPP-ERANGE-TYPE: cannot range over %s", bashPPTypeText(meta.typ))
		return true
	}
	switch meta.kind {
	case "array", "inferred-array":
		value, meta = bashPPCopyArrayValue(value, meta)
		sequence := value.([]any)
		for i, elem := range sequence {
			if !r.bashPPRangeIteration(ctx, rng, i, bashPPRangeNamedType("int"), elem, meta.sequence[i], collection.Element) {
				return true
			}
		}
	case "slice":
		sequence, _ := value.([]any)
		length := len(sequence)
		for i := 0; i < length; i++ {
			if !r.bashPPRangeIteration(ctx, rng, i, bashPPRangeNamedType("int"), sequence[i], meta.sequence[i], collection.Element) {
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
		cell.declType = typ
		if named, ok := typ.(*syntax.BashPPNamedType); ok {
			cell.typeName = named.Name.Value
		}
		return
	}
	r.bashPPDeclareName(name, expand.Variable{Set: true, Kind: expand.String, Str: fmt.Sprint(value)})
	cell := r.bashPPScope.lookup(name)
	cell.declType = typ
	if named, ok := typ.(*syntax.BashPPNamedType); ok {
		cell.typeName = named.Name.Value
	}
}
