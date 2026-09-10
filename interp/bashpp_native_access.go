// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

// Sprint: #118; Story: #52; Story-ID: d564bada90bb
//
// Structured reads over values that live in the dependency process. An
// imported call or package variable answers with a session handle, so the
// original program's index, slice and range expressions must read through that
// handle instead of requiring an interpreter-side copy. Only the access is
// forwarded: the surrounding expression, statement and loop bodies stay on the
// interpreter path.

import (
	"context"
	"fmt"
	"go/constant"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// bashPPNativeExpr reports whether expr denotes a value owned by the
// dependency process. Index and slice expressions inherit the answer from
// their base so that os.Args[1:] is recognised without evaluating anything.
func (r *Runner) bashPPNativeExpr(expr syntax.BashPPExpr) bool {
	switch x := expr.(type) {
	case *syntax.BashPPCompositeLit:
		return r.bashPPNativeType(x.LitType)
	case *syntax.BashPPAddressExpr:
		if lit, ok := x.X.(*syntax.BashPPCompositeLit); ok {
			return r.bashPPNativeType(lit.LitType)
		}
		return false
	case *syntax.BashPPDerefExpr:
		return r.bashPPNativeExpr(x.X)
	case *syntax.BashPPParenExpr:
		return r.bashPPNativeExpr(x.X)
	case *syntax.BashPPIndexExpr:
		return r.bashPPNativeExpr(x.X)
	case *syntax.BashPPSliceExpr:
		return r.bashPPNativeExpr(x.X)
	case *syntax.BashPPCall:
		return r.bashPPBridgeHandles(x)
	case *syntax.BashPPSelectorExpr:
		if r.bashPPNativeLocalField(x) != nil {
			return true
		}
		if id, ok := x.X.(*syntax.BashPPIdent); ok {
			if _, imported := r.bashPPImports[id.Name.Value]; imported {
				return true
			}
		}
		return r.bashPPNativeExpr(x.X)
	case *syntax.BashPPIdent:
		return r.bashPPNativeCellValue(x.Name.Value) != nil
	}
	return false
}

// bashPPNativeCellValue returns the native value bound to name, if any.
func (r *Runner) bashPPNativeCellValue(name string) *bashPPBridgeValue {
	if r.bashPPScope == nil {
		return nil
	}
	cell := r.bashPPScope.lookup(name)
	for cell != nil && cell.interfaceValue != nil && !cell.interfaceValue.nilIface {
		cell = cell.interfaceValue.cell
	}
	if cell == nil || cell.vr.Kind != expand.Object {
		return nil
	}
	value, _ := cell.vr.Obj.(*bashPPBridgeValue)
	return value
}

// bashPPNativeAccess performs one structured read against a native value.
func (r *Runner) bashPPNativeAccess(ctx context.Context, op string, base bashPPBridgeValue, selector string, args ...bashPPBridgeValue) (bashPPBridgeValue, error) {
	req, err := r.bashPPEvalRequest()
	if err != nil {
		return bashPPBridgeValue{}, err
	}
	values, err := r.bashPPNativeRequest(ctx, req, bashPPBridgeRequest{Op: op, Selector: selector, Receiver: &base, Args: args})
	if err != nil {
		return bashPPBridgeValue{}, err
	}
	if len(values) != 1 {
		return bashPPBridgeValue{}, fmt.Errorf("gosource: native %s returned %d values", op, len(values))
	}
	return values[0], nil
}

// bashPPNativeIndex evaluates base[index] in the dependency process.
func (r *Runner) bashPPNativeIndex(x *syntax.BashPPIndexExpr) (bashPPBridgeValue, error) {
	base, err := r.bashPPBridgeExpr(x.X)
	if err != nil {
		return bashPPBridgeValue{}, err
	}
	index, err := r.bashPPBridgeExpr(x.Index)
	if err != nil {
		return bashPPBridgeValue{}, err
	}
	return r.bashPPNativeAccess(r.ectx, "index", base, "", index)
}

// bashPPNativeSlice evaluates base[low:high] in the dependency process. Go's
// optional capacity bound stays on that native slice header and is checked
// against the actual backing capacity by the dependency worker.
func (r *Runner) bashPPNativeSlice(x *syntax.BashPPSliceExpr) (bashPPBridgeValue, error) {
	base, err := r.bashPPBridgeExpr(x.X)
	if err != nil {
		return bashPPBridgeValue{}, err
	}
	bounds := []bashPPBridgeValue{{Kind: "nil"}, {Kind: "nil"}}
	if x.Max != nil {
		bounds = append(bounds, bashPPBridgeValue{Kind: "nil"})
	}
	for i, bound := range []syntax.BashPPExpr{x.Low, x.High, x.Max} {
		if bound == nil {
			continue
		}
		if bounds[i], err = r.bashPPBridgeExpr(bound); err != nil {
			return bashPPBridgeValue{}, err
		}
	}
	return r.bashPPNativeAccess(r.ectx, "slice", base, "", bounds...)
}

// bashPPNativeLen answers len(value) for a native value.
func (r *Runner) bashPPNativeLen(ctx context.Context, base bashPPBridgeValue) (int, error) {
	value, err := r.bashPPNativeAccess(ctx, "len", base, "")
	if err != nil {
		return 0, err
	}
	scalar, err := value.scalar()
	if err != nil {
		return 0, err
	}
	length, ok := constant.Int64Val(scalar.value)
	if !ok {
		return 0, fmt.Errorf("gosource: native length is not representable")
	}
	return int(length), nil
}

// bashPPNativeRange iterates a native sequence. Each element is read back
// through the same handle, so the dependency keeps ownership of the value and
// the loop body executes on the interpreter path unchanged.
func (r *Runner) bashPPNativeRange(ctx context.Context, rng *syntax.BashPPRange) bool {
	if rng.Expr == nil || !r.bashPPGoSource || !r.bashPPNativeExpr(rng.Expr) {
		return false
	}
	base, err := r.bashPPBridgeExpr(rng.Expr)
	if err != nil {
		r.bashPPRangeError(rng, "BASHPP-ERANGE-TYPE: %v", err)
		return true
	}
	if channel, ok := r.goSourceNativeChannel(goSourceNativeValueCell(base)); ok {
		r.goSourceRangeNativeChannel(ctx, rng, channel)
		return true
	}
	if base.Kind != "handle" {
		// A native scalar or nil is not a range operand the bridge owns; the
		// ordinary scalar range path reports it with Go's own wording.
		return false
	}
	kind, err := r.bashPPNativeKind(ctx, base)
	if err != nil {
		r.bashPPRangeError(rng, "BASHPP-ERANGE-TYPE: %v", err)
		return true
	}
	if kind == "map" {
		r.bashPPNativeRangeMap(ctx, rng, base)
		return true
	}
	length, err := r.bashPPNativeLen(ctx, base)
	if err != nil {
		r.bashPPRangeError(rng, "BASHPP-ERANGE-TYPE: %v", err)
		return true
	}
	for i := range length {
		if (base.Type == "[]uint8" || base.Type == "[]byte") && (len(rng.Names) < 2 || rng.Names[1].Value == "_") {
			if !r.bashPPRangeIteration(ctx, rng, i, bashPPRangeNamedType("int"), nil, nil, nil) {
				return true
			}
			continue
		}
		element, err := r.bashPPNativeAccess(ctx, "index", base, "", bashPPBridgeValue{Kind: "int", Text: fmt.Sprint(i)})
		if err != nil {
			r.bashPPRangeError(rng, "BASHPP-ERANGE-TYPE: %v", err)
			return true
		}
		value, meta, typ, err := r.bashPPNativeIterationValue(element)
		if err != nil {
			r.bashPPRangeError(rng, "BASHPP-ERANGE-TYPE: %v", err)
			return true
		}
		if !r.bashPPRangeIteration(ctx, rng, i, bashPPRangeNamedType("int"), value, meta, typ) {
			return true
		}
	}
	return true
}

// bashPPNativeKind asks the dependency for the reflect kind of a value it
// owns. A range must know whether it iterates keys or indexes before it asks
// for either; only the kind name crosses.
func (r *Runner) bashPPNativeKind(ctx context.Context, base bashPPBridgeValue) (string, error) {
	value, err := r.bashPPNativeAccess(ctx, "kind", base, "")
	if err != nil {
		return "", err
	}
	if value.Kind != "string" {
		return "", fmt.Errorf("gosource: native kind is not reported")
	}
	return value.Text, nil
}

// bashPPNativeRangeMap iterates a dependency-owned map. Go's own map order is
// unspecified, and the keys arrive in the order the dependency's own range
// produced them. Each key and value is read back through the same handle.
func (r *Runner) bashPPNativeRangeMap(ctx context.Context, rng *syntax.BashPPRange, base bashPPBridgeValue) {
	req, err := r.bashPPEvalRequest()
	if err != nil {
		r.bashPPRangeError(rng, "BASHPP-ERANGE-TYPE: %v", err)
		return
	}
	keys, err := r.bashPPNativeRequest(ctx, req, bashPPBridgeRequest{Op: "keys", Receiver: &base})
	if err != nil {
		r.bashPPRangeError(rng, "BASHPP-ERANGE-TYPE: %v", err)
		return
	}
	for _, key := range keys {
		element, err := r.bashPPNativeAccess(ctx, "index", base, "", key)
		if err != nil {
			r.bashPPRangeError(rng, "BASHPP-ERANGE-TYPE: %v", err)
			return
		}
		if !r.bashPPNativeIteration(ctx, rng, &key, &element) {
			return
		}
	}
}

// bashPPNativeIteration runs one loop body with dependency-owned bindings. A
// scalar binds as an ordinary typed value and anything else keeps its handle,
// which is what bashPPBindNativeValue already decides for every other native
// binding site.
func (r *Runner) bashPPNativeIteration(ctx context.Context, rng *syntax.BashPPRange, key, value *bashPPBridgeValue) bool {
	leave := r.bashPPPushScope()
	if len(rng.Names) >= 1 && rng.Names[0].Value != "_" && key != nil {
		r.bashPPBindNativeValue(rng.Names[0].Value, *key)
	}
	if len(rng.Names) == 2 && rng.Names[1].Value != "_" && value != nil {
		r.bashPPBindNativeValue(rng.Names[1].Value, *value)
	}
	r.cmd(r.bashPPTaskContext(ctx), rng.Body)
	leave()
	return r.bashPPRangeControl()
}

// bashPPNativeIterationValue converts one element into the interpreter's
// iteration binding. Scalars bind by value; anything else keeps its handle.
func (r *Runner) bashPPNativeIterationValue(element bashPPBridgeValue) (any, *bashPPCollectionMeta, syntax.BashPPTypeExpr, error) {
	typeName := element.Type
	if element.Interface != "" {
		typeName = element.Interface
	}
	typ := bashPPRangeNamedType(typeName)
	scalar, err := element.scalar()
	if err != nil {
		if element.Kind == "handle" || element.Kind == "nil" {
			copy := element
			return &copy, &bashPPCollectionMeta{kind: "native", typ: typ}, typ, nil
		}
		return nil, nil, nil, err
	}
	return bashPPScalarString(scalar.value), nil, typ, nil
}

// bashPPNativeRead answers a structured read whose base is dependency-owned.
// A scalar element becomes an ordinary interpreter value; anything else keeps
// its handle so that the dependency retains ownership and identity. The
// boolean reports whether this path applies at all.
func (r *Runner) bashPPNativeRead(expr syntax.BashPPExpr) (any, *bashPPCollectionMeta, error, bool) {
	if !r.bashPPGoSource {
		return nil, nil, nil, false
	}
	switch x := expr.(type) {
	case *syntax.BashPPIndexExpr:
		if !r.bashPPNativeExpr(x.X) {
			return nil, nil, nil, false
		}
	case *syntax.BashPPSliceExpr:
		if !r.bashPPNativeExpr(x.X) {
			return nil, nil, nil, false
		}
	case *syntax.BashPPSelectorExpr:
		if !r.bashPPNativeExpr(x) {
			return nil, nil, nil, false
		}
	case *syntax.BashPPDerefExpr:
		if !r.bashPPNativeExpr(x.X) {
			return nil, nil, nil, false
		}
	default:
		return nil, nil, nil, false
	}
	value, err := r.bashPPBridgeExpr(expr)
	if err != nil {
		return nil, nil, err, true
	}
	result, err := bashPPNativeReadValue(value)
	return result, nil, err, true
}

// bashPPNativeReadValue projects one native value into the interpreter's
// untyped value space without losing a handle's identity.
func bashPPNativeReadValue(value bashPPBridgeValue) (any, error) {
	switch value.Kind {
	case "handle", "nil":
		copy := value
		return &copy, nil
	}
	scalar, err := value.scalar()
	if err != nil {
		return nil, err
	}
	switch scalar.value.Kind() {
	case constant.String:
		return constant.StringVal(scalar.value), nil
	case constant.Bool:
		return constant.BoolVal(scalar.value), nil
	case constant.Int:
		n, ok := constant.Int64Val(scalar.value)
		if !ok {
			return bashPPScalarString(scalar.value), nil
		}
		return n, nil
	case constant.Float:
		n, _ := constant.Float64Val(scalar.value)
		return n, nil
	}
	return bashPPScalarString(scalar.value), nil
}

// bashPPNativeShortDecl binds `name := <native read>`. Without it the general
// short-declaration path would stringify a session handle and lose the
// dependency's ownership of the value, so os.Args could not be indexed later.
// Only reads whose base is dependency-owned are claimed here; method values
// and interpreter collections keep their existing paths.
func (r *Runner) bashPPNativeShortDecl(d *syntax.BashPPShortDecl) bool {
	if !r.bashPPGoSource || d.Expr == nil || len(d.Lhs) != 1 || r.bashPPScope == nil {
		return false
	}
	switch x := d.Expr.(type) {
	case *syntax.BashPPCompositeLit, *syntax.BashPPAddressExpr:
		if !r.bashPPNativeExpr(d.Expr) {
			return false
		}
	case *syntax.BashPPIndexExpr:
		if !r.bashPPNativeExpr(x.X) {
			return false
		}
	case *syntax.BashPPSliceExpr:
		if !r.bashPPNativeExpr(x.X) {
			return false
		}
	case *syntax.BashPPSelectorExpr:
		if !r.bashPPNativeExpr(x) {
			return false
		}
	default:
		return false
	}
	value, err := r.bashPPBridgeExpr(d.Expr)
	if err != nil {
		r.errf("%s%v\n", r.bashErrPrefix(d.Expr.Pos()), err)
		r.exit = exitStatus{code: 2}
		return true
	}
	if d.Lhs[0].Value != "_" {
		r.bashPPBindNativeValue(d.Lhs[0].Value, value)
	}
	return true
}

// bashPPNativeBuiltinLength answers len/cap for a dependency-owned value. The
// dependency reports the length of the value it still owns, so no interpreter
// copy of the sequence has to exist. The last result reports whether the
// argument was native at all.
func (r *Runner) bashPPNativeBuiltinLength(name string, c *syntax.BashPPCall, arg bashPPBuiltinArg) (int, error, bool) {
	value, ok := arg.value.(*bashPPBridgeValue)
	if !ok && arg.cell != nil && arg.cell.vr.Kind == expand.Object {
		value, ok = arg.cell.vr.Obj.(*bashPPBridgeValue)
	}
	if !ok && c != nil && len(c.ArgExprs) == 1 && c.ArgExprs[0] != nil && r.bashPPNativeExpr(c.ArgExprs[0]) {
		// A builtin argument reaches here as source text, so a native read
		// such as len(os.Args) must be evaluated from its expression.
		read, err := r.bashPPBridgeExpr(c.ArgExprs[0])
		if err != nil {
			return 0, err, true
		}
		value, ok = &read, true
	}
	if !ok || value == nil {
		return 0, nil, false
	}
	if value.Kind == "nil" {
		// Go's len and cap of a nil slice or map are zero, not an error.
		return 0, nil, true
	}
	result, err := r.bashPPNativeAccess(r.ectx, name, *value, "")
	if err != nil {
		return 0, fmt.Errorf("BASHPP-EBUILTIN-TYPE: %v", err), true
	}
	scalar, err := result.scalar()
	if err != nil {
		return 0, err, true
	}
	size, exact := constant.Int64Val(scalar.value)
	if !exact {
		return 0, fmt.Errorf("gosource: native %s is not representable", name), true
	}
	return int(size), nil, true
}

// Inspect only a local identifier/field chain. This evaluates no index, call or
// user expression and therefore cannot repeat argument effects during dispatch.
func (r *Runner) bashPPNativeLocalField(expr *syntax.BashPPSelectorExpr) *bashPPBridgeValue {
	value, _, ok := r.bashPPNativeLocalBase(expr)
	if !ok {
		return nil
	}
	native, _ := value.(*bashPPBridgeValue)
	return native
}

// bashPPNativeLocalBase walks that chain and reports the value and collection
// metadata it lands on. Callers wanting the dependency-owned value use
// [Runner.bashPPNativeLocalField]; callers that must promote an embedded
// receiver out of the enclosing struct need the metadata too.
func (r *Runner) bashPPNativeLocalBase(expr syntax.BashPPExpr) (any, *bashPPCollectionMeta, bool) {
	if !r.bashPPGoSource {
		return nil, nil, false
	}
	root := expr
	var names []string
	for {
		field, ok := root.(*syntax.BashPPSelectorExpr)
		if !ok {
			break
		}
		names = append(names, field.Sel.Value)
		root = field.X
	}
	id, ok := root.(*syntax.BashPPIdent)
	if !ok || r.bashPPScope == nil {
		return nil, nil, false
	}
	cell := r.bashPPScope.lookup(id.Name.Value)
	if cell == nil || r.bashPPNativeCellValue(id.Name.Value) != nil {
		return nil, nil, false
	}
	value, meta := cell.vr.Obj, bashPPCellMeta(cell)
	if cell.pointer {
		if cell.pointerValue == nil {
			return nil, nil, false
		}
		var err error
		value, meta, _, err = cell.pointerValue.read()
		if err != nil {
			return nil, nil, false
		}
	}
	for i := len(names) - 1; i >= 0; i-- {
		if ptr, ok := value.(*bashPPPointer); ok {
			if ptr == nil {
				return nil, nil, false
			}
			var err error
			value, meta, _, err = ptr.read()
			if err != nil {
				return nil, nil, false
			}
		}
		if meta == nil || meta.kind != "struct" {
			return nil, nil, false
		}
		sel := r.bashPPResolveField(meta.typ, names[i])
		if sel.ambiguous || len(sel.edges) == 0 {
			return nil, nil, false
		}
		var err error
		value, meta, err = bashPPReadSelection(value, meta, sel.edges)
		if err != nil {
			return nil, nil, false
		}
	}
	return value, meta, true
}
