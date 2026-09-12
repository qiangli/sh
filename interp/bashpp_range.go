// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"fmt"
	"go/constant"
	"sort"
	"strconv"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// goSourceRangeYield is the interpreter-owned callback supplied to an iterator.
// Its body is still the authored range body; this state only carries the bool
// continuation result and an outer return across the iterator's call frame.
type goSourceRangeYield struct {
	rng       *syntax.BashPPRange
	stopped   bool
	returning bool
	ret       bashPPReturnState
}

func (r *Runner) goSourceIteratorYield(fn *bashPPFunc) (*syntax.BashPPFuncType, error) {
	if fn == nil || len(fn.results()) != 0 {
		return nil, fmt.Errorf("iterator callback must have no results")
	}
	params := bashppParams(fn.params())
	if len(params) != 1 || params[0].variadic {
		return nil, fmt.Errorf("iterator callback must accept one yield function")
	}
	yield, ok := params[0].typ.(*syntax.BashPPFuncType)
	if !ok || bashppResultCount(yield.Results) != 1 || len(bashppParams(yield.Results)) != 1 ||
		bashPPTypeText(r.bashPPUnderlyingType(bashppParams(yield.Results)[0].typ)) != "bool" {
		return nil, fmt.Errorf("iterator yield must return one bool")
	}
	yieldParams := bashppParams(yield.Params)
	if len(yieldParams) > 2 {
		return nil, fmt.Errorf("iterator yield accepts at most two parameters")
	}
	for _, param := range yieldParams {
		if param.variadic || !r.bashPPCallbackScalarType(param.typ) {
			return nil, fmt.Errorf("iterator callback requires scalar yield parameters")
		}
	}
	return yield, nil
}

func (r *Runner) bashPPCallbackScalarType(typ syntax.BashPPTypeExpr) bool {
	underlying := r.bashPPUnderlyingType(typ)
	named, ok := underlying.(*syntax.BashPPNamedType)
	if !ok || named.Name == nil {
		return false
	}
	name := named.Name.Value
	return bashPPIntegerType(name) || name == "bool" || name == "string" || name == "float32" || name == "float64"
}

func goSourceRangeFuncType(expr syntax.BashPPExpr) *syntax.BashPPFuncType {
	switch x := expr.(type) {
	case *syntax.BashPPParenExpr:
		return goSourceRangeFuncType(x.X)
	case *syntax.BashPPCall:
		return x.ResultFuncType
	case *syntax.BashPPFuncLit:
		return bashPPFuncLitType(x)
	case *syntax.BashPPSelectorExpr:
		return x.FuncType
	}
	return nil
}

// goSourceRangeFunction executes the Go 1.23 range-over-function protocol. It
// is GoSource-only: Classic/Bash++ retain their existing explicit rejection.
func (r *Runner) goSourceRangeFunction(ctx context.Context, rng *syntax.BashPPRange) bool {
	if !r.bashPPGoSource || rng == nil || rng.Expr == nil {
		return false
	}
	var fn *bashPPFunc
	if cell, handled, err := r.goSourceCallableCell(rng.Expr); handled {
		if err != nil {
			r.bashPPRangeError(rng, "BASHPP-ERANGE-FUNC: %v", err)
			return true
		}
		fn, _ = r.bashPPClosure(cell.vr.Str)
	} else if signature := goSourceRangeFuncType(rng.Expr); signature != nil {
		cell, err := r.goSourceValueCell(rng.Expr)
		if err != nil {
			r.bashPPRangeError(rng, "BASHPP-ERANGE-FUNC: %v", err)
			return true
		}
		if local, ok := r.bashPPClosure(cell.vr.Str); ok {
			fn = local
		} else if value, ok := cell.vr.Obj.(*bashPPBridgeValue); ok && value.Kind == "handle" {
			native := *value
			native.Callable = "range-iterator"
			fn = &bashPPFunc{native: &native, lit: &syntax.BashPPFuncLit{Params: signature.Params, Results: signature.Results}}
		}
	}
	if fn == nil {
		return false
	}
	yieldType, err := r.goSourceIteratorYield(fn)
	if err != nil {
		r.bashPPRangeError(rng, "BASHPP-ERANGE-FUNC: %v", err)
		return true
	}
	if len(rng.Names) > len(bashppParams(yieldType.Params)) {
		r.bashPPRangeError(rng, "BASHPP-ERANGE-ARITY: iterator yields %d value(s)", len(bashppParams(yieldType.Params)))
		return true
	}
	state := &goSourceRangeYield{rng: rng}
	yield := &bashPPFunc{
		rangeYield: state,
		lit:        &syntax.BashPPFuncLit{Params: yieldType.Params, Results: yieldType.Results},
		scope:      r.bashPPScope,
	}
	vr := r.bashPPStoreFunc(yield)
	r.bashPPCallCells = []*bashPPCell{{vr: vr, declType: yieldType}}
	r.bashPPInvoke(ctx, fn, []string{vr.Str})
	if state.returning {
		r.bashPPReturn = state.ret
		r.exit.returning = true
	}
	return true
}

func (r *Runner) goSourceInvokeRangeYield(ctx context.Context, fn *bashPPFunc, args []string, cells []*bashPPCell) []string {
	state := fn.rangeYield
	if state.stopped {
		r.bashPPRaise("runtime error: range function continued iteration after function for loop body returned false")
		return nil
	}
	more := !state.stopped
	if more {
		params := bashppParams(fn.params())
		if len(args) != len(params) {
			r.exit.fatal(fmt.Errorf("gosource: iterator yield argument count mismatch"))
			return nil
		}
		values := make([]string, len(args))
		for i := range args {
			values[i] = args[i]
			if i < len(cells) && cells[i] != nil {
				values[i] = cells[i].vr.String()
			}
		}
		savedScope := r.bashPPScope
		r.bashPPScope = fn.scope
		var key, value any
		var keyType, valueType syntax.BashPPTypeExpr
		if len(values) > 0 {
			key, keyType = values[0], params[0].typ
		}
		if len(values) > 1 {
			value, valueType = values[1], params[1].typ
		}
		more = r.bashPPRangeIteration(ctx, state.rng, key, keyType, value, nil, valueType)
		r.bashPPScope = savedScope
		if r.exit.returning {
			state.returning, state.ret = true, r.bashPPReturn
			r.bashPPReturn = bashPPReturnState{}
			r.exit.returning = false
			more = false
		}
		state.stopped = !more
	}
	text := strconv.FormatBool(more)
	cell := &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: text}, scalarKind: constant.Bool, typeName: "bool", declType: bashPPRangeNamedType("bool")}
	r.bashPPResultCells = []*bashPPCell{cell}
	return []string{text}
}

func (r *Runner) goSourceInvokeCollectYield(fn *bashPPFunc, args []string, cells []*bashPPCell) []string {
	if len(args) != 1 || len(cells) != 1 || cells[0] == nil {
		r.exit.fatal(fmt.Errorf("gosource: slices.Collect iterator yield argument count mismatch"))
		return nil
	}
	value, err := r.bashPPBridgeCell(cells[0])
	if err != nil {
		r.exit.fatal(err)
		return nil
	}
	*fn.collectYield = append(*fn.collectYield, value)
	cell := &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: "true"}, scalarKind: constant.Bool, typeName: "bool", declType: bashPPRangeNamedType("bool")}
	r.bashPPResultCells = []*bashPPCell{cell}
	return []string{"true"}
}

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
		// A composite is a range value in its own right. It has no lexical root
		// cell, so materialize it exactly once below instead of passing it to the
		// scalar evaluator. The resulting metadata preserves array-copy and
		// slice/map reference semantics.
		if _, composite := rng.Expr.(*syntax.BashPPCompositeLit); !composite {
			return false
		}
	}
	var cell *bashPPCell
	if ok {
		cell = r.bashPPScope.lookup(root)
		if cell == nil || cell.channel != nil {
			return false
		}
	}
	// A variadic parameter binds as an indexed variable rather than an
	// object — see [Runner.bashPPInvoke] — so it carries its collection meta
	// on the side instead of behind cell.object. A bare `range rest` is the
	// only shape that meta describes; a path rooted in one more scalars would
	// still need the Object machinery below, which a variadic parameter never
	// has.
	if _, isIdent := rng.Expr.(*syntax.BashPPIdent); isIdent && cell != nil && cell.vr.Kind == expand.Indexed && cell.valueMeta != nil {
		return r.bashPPRangeIndexed(ctx, rng, cell)
	}
	if cell != nil && cell.vr.Kind != expand.Object && !cell.pointer {
		return false
	}
	if cell != nil {
		if _, native := r.goSourceNativeChannel(cell); native {
			return false
		}
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

// bashPPRangeIndexed ranges the indexed binding a variadic parameter gets in
// [Runner.bashPPInvoke], using the collection meta attached there instead of
// the string list's length: element i's value and declared element type come
// from cell.valueMeta, exactly as they would for a slice held behind an
// Object cell.
func (r *Runner) bashPPRangeIndexed(ctx context.Context, rng *syntax.BashPPRange, cell *bashPPCell) bool {
	meta := cell.valueMeta
	collection, ok := r.bashPPUnderlyingType(meta.typ).(*syntax.BashPPCollectionType)
	if !ok {
		r.bashPPRangeError(rng, "BASHPP-ERANGE-TYPE: cannot range over %s", bashPPTypeText(meta.typ))
		return true
	}
	for i, elem := range cell.vr.List {
		var elemMeta *bashPPCollectionMeta
		if i < len(meta.sequence) {
			elemMeta = meta.sequence[i]
		}
		if !r.bashPPRangeIteration(ctx, rng, i, bashPPRangeNamedType("int"), elem, elemMeta, collection.Element) {
			return true
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
	return r.bashPPRangeControl()
}

// bashPPRangeControl reduces the runner state one executed loop body leaves to
// whether the range should continue. Every range shape shares it, so break,
// continue, return and abandonment behave identically across them.
func (r *Runner) bashPPRangeControl() bool {
	if r.exit.exiting || r.exit.returning || r.exit.fatalExit || r.loopControlPending() {
		return false
	}
	switch r.bashPPBranch {
	case bashPPBranchBreak:
		if r.bashPPBranchEscapesEligible() {
			return false
		}
		r.bashPPClearBranch()
		return false
	case bashPPBranchContinue:
		if r.bashPPBranchEscapesEligible() {
			return false
		}
		r.bashPPClearBranch()
	case bashPPBranchFallthrough:
		panic("validated fallthrough escaped to Bash++ range")
	}
	return true
}

func (r *Runner) bashPPDeclareRangeValue(name string, value any, typ syntax.BashPPTypeExpr, meta *bashPPCollectionMeta) {
	if meta != nil && (meta.kind == "pointer" || r.bashPPGoSource && meta.interfaceValue != nil) {
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
