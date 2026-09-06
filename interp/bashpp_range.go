// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"fmt"
	"sort"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

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
	collection := meta.typ.(*syntax.BashPPCollectionType)
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
	if len(rng.Names) >= 1 {
		r.bashPPDeclareRangeValue(rng.Names[0].Value, key, keyType, nil)
	}
	if len(rng.Names) == 2 {
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
