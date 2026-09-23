package interp

// Sprint: #118; Story: #52; Story-ID: d564bada90bb
import (
	"fmt"
	"go/constant"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func (r *Runner) goSourceCollectionBuiltinCell(expr syntax.BashPPExpr) (*bashPPCell, bool, error) {
	if !r.bashPPGoSource {
		return nil, false, nil
	}
	call, ok := expr.(*syntax.BashPPCall)
	if !ok {
		return nil, false, nil
	}
	name := bashPPPredeclaredCall(call)
	switch name {
	case "make", "append", "copy", "new":
	default:
		return nil, false, nil
	}
	if r.bashPPFuncs[name] != nil || (r.bashPPScope != nil && r.bashPPScope.lookup(name) != nil) {
		return nil, false, nil
	}
	cell, produced := r.bashPPRunValueBuiltin(name, call)
	if !produced || cell == nil {
		return nil, true, errBashPPScalarInterrupted
	}
	return cell, true, nil
}

func (r *Runner) goSourceBuiltinArg(call *syntax.BashPPCall, index int) (bashPPBuiltinArg, error) {
	if !r.bashPPGoSource || index >= len(call.ArgExprs) || call.ArgExprs[index] == nil {
		return r.bashPPBuiltinArg(call.Args[index]), nil
	}
	expr := call.ArgExprs[index]
	var cell *bashPPCell
	if id, ok := expr.(*syntax.BashPPIdent); ok {
		cell = r.bashPPScope.lookup(id.Name.Value)
	}
	if cell == nil {
		var err error
		switch expr.(type) {
		case *syntax.BashPPIndexExpr, *syntax.BashPPSliceExpr, *syntax.BashPPSelectorExpr, *syntax.BashPPDerefExpr:
			// Read once: probing for a structured result then falling back to scalar
			// evaluation would execute an index or receiver expression twice.
			value, meta, readErr := r.bashPPReadExpr(expr)
			if readErr != nil {
				return bashPPBuiltinArg{}, readErr
			}
			cell = r.goSourceCollectionReadCell(expr, value, meta)
		default:
			cell, err = r.goSourceValueCell(expr)
		}
		if err != nil {
			return bashPPBuiltinArg{}, err
		}
	}
	if cell == nil {
		return bashPPBuiltinArg{}, fmt.Errorf("Go builtin argument has no value")
	}
	return r.goSourceBuiltinCellArg(cell, bashPPWordSource(call.Args[index])), nil
}

func (r *Runner) goSourceBuiltinCellArg(cell *bashPPCell, text string) bashPPBuiltinArg {
	arg := bashPPBuiltinArg{cell: cell, typ: cell.declType, channel: cell.channel, text: text}
	if cell.interfaceValue != nil {
		arg.meta = &bashPPCollectionMeta{kind: "interface", typ: cell.declType, interfaceValue: cell.interfaceValue}
		if source := cell.interfaceValue.cell; source != nil {
			arg.value = source.vrValue()
		}
	} else if cell.pointer {
		arg.value, arg.meta = cell.pointerValue, bashPPPointerMeta(cell.declType)
	} else if cell.vr.Kind == expand.Object {
		arg.value, arg.meta = cell.vr.Obj, bashPPCellMeta(cell)
		if arg.typ == nil && arg.meta != nil {
			arg.typ = arg.meta.typ
		}
	} else {
		arg.scalar = r.bashPPScalarFromCell(cell)
		arg.value = bashPPBuiltinExactScalarValue(cell.vr.Str, arg.scalar)
		arg.hasScalar = true
	}
	return arg
}

// goSourceBuiltinTupleArgs implements Go's rule that a call returning multiple
// values may be the sole argument to another call. Builtins do their own arity
// and type checking, so expand the result cells before those checks while
// evaluating the source call exactly once.
func (r *Runner) goSourceBuiltinTupleArgs(call *syntax.BashPPCall) ([]bashPPBuiltinArg, bool, error) {
	if !r.bashPPGoSource || len(call.ArgExprs) != 1 {
		return nil, false, nil
	}
	inner, ok := call.ArgExprs[0].(*syntax.BashPPCall)
	if !ok {
		return nil, false, nil
	}
	fn, ok := r.bashPPLookupFunc(inner)
	if !ok || bashppResultCount(fn.results()) < 2 {
		return nil, false, nil
	}
	cells, err := r.goSourceCallResultCells(inner, fn)
	if err != nil {
		return nil, true, err
	}
	args := make([]bashPPBuiltinArg, len(cells))
	for i, cell := range cells {
		args[i] = r.goSourceBuiltinCellArg(cell, bashPPWordSource(call.Args[0]))
	}
	return args, true, nil
}

func (r *Runner) goSourceCollectionCallValue(expr syntax.BashPPExpr, expected syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta, bool, error) {
	if !r.bashPPGoSource {
		return nil, nil, false, nil
	}
	call, ok := expr.(*syntax.BashPPCall)
	if !ok {
		return nil, nil, false, nil
	}
	if value, meta, claimed, err := r.goSourceUnsafeSliceValue(call); claimed {
		return value, meta, true, err
	}
	if values, claimed, err := r.goSourceUnsafeSliceCall(call); claimed {
		if err != nil {
			return nil, nil, true, err
		}
		if len(values) != 1 {
			return nil, nil, true, fmt.Errorf("unsafe.Slice returned %d values", len(values))
		}
		value, meta, bridged, err := r.bashPPCollectionBridgeValue(&values[0], expected)
		return value, meta, bridged, err
	}
	cell, err := r.goSourceValueCell(expr)
	if err != nil {
		return nil, nil, true, err
	}
	if cell == nil {
		return nil, nil, true, fmt.Errorf("Go collection call has no result")
	}
	if cell.pointer {
		return cell.pointerValue, bashPPPointerMeta(cell.declType), true, nil
	}
	if cell.vr.Kind == expand.Object {
		return cell.vr.Obj, bashPPCellMeta(cell), true, nil
	}
	// Use the call result cell's scalar carrier, then apply the collection's
	// concrete destination. A generic zero result can have textual shell
	// storage ("0") even though its declared result is the active T; treating
	// that text as a string loses the result cell's numeric value.
	scalar := r.bashPPScalarFromCell(cell)
	value := bashPPBuiltinExactScalarValue(cell.vr.Str, scalar)
	return r.bashPPContextualCollectionValue(value, expected), nil, true, nil
}

func (r *Runner) goSourceMapCommaDecl(d *syntax.BashPPShortDecl) bool {
	if !r.bashPPGoSource || len(d.Lhs) != 2 {
		return false
	}
	index, ok := d.Expr.(*syntax.BashPPIndexExpr)
	if !ok {
		return false
	}
	value, found := r.goSourceMapCommaCells(index)
	if value == nil {
		return true
	}
	r.bashPPBindReceivedCell(d.Lhs[0].Value, value)
	r.bashPPBindReceivedCell(d.Lhs[1].Value, found)
	return true
}

// goSourceCommaOkAssign settles the two-target, one-expression assignment
// forms whose second value is Go's comma-ok boolean: `v, ok = m[k]` and
// `v, ok = i.(T)`. The `:=` spellings already have their own paths; the
// plain assignment is the same read, committed to existing targets. It
// reports whether it claimed the statement.
func (r *Runner) goSourceCommaOkAssign(assign *syntax.BashPPAssign) bool {
	if !r.bashPPGoSource || len(assign.Names) != 2 || len(assign.ValueExprs) != 1 {
		return false
	}
	switch expr := assign.ValueExprs[0].(type) {
	case *syntax.BashPPIndexExpr:
		value, found := r.goSourceMapCommaCells(expr)
		if value == nil {
			return true
		}
		r.bashPPCommitTupleAssign(assign, []*bashPPCell{value, found})
		return true
	case *syntax.BashPPTypeAssertExpr:
		if expr.TypeToken != nil {
			r.bashPPGoSendError(expr, fmt.Errorf("BASHPP-EASSERT-TYPE: .(type) is only valid in a type switch"))
			return true
		}
		values, source, err := r.bashPPTypeAssert(expr, true)
		if err != nil {
			r.bashPPGoSendError(expr, err)
			return true
		}
		if r.exit.code != 0 || len(values) != 2 {
			return true
		}
		value := &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: values[0]}}
		if source != nil {
			value = bashPPCopyAssignmentCell(source)
		}
		found := &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: values[1]}, scalarKind: constant.Bool, declType: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "bool"}}, typeName: "bool"}
		r.bashPPCommitTupleAssign(assign, []*bashPPCell{value, found})
		return true
	}
	return false
}

// goSourceMapCommaCells reads `m[k]` in its comma-ok form: the element (or
// the element type's zero value) and the boolean reporting whether the key
// was present. A nil element cell means the read was already reported.
func (r *Runner) goSourceMapCommaCells(index *syntax.BashPPIndexExpr) (*bashPPCell, *bashPPCell) {
	value, meta, err := r.bashPPReadExpr(index.X)
	if err != nil {
		r.bashPPGoSendError(index.X, err)
		return nil, nil
	}
	shape, ok := r.bashPPUnderlyingType(metaType(meta)).(*syntax.BashPPCollectionType)
	if !ok || meta == nil || meta.kind != "map" {
		r.bashPPGoSendError(index, fmt.Errorf("comma-ok index requires a represented map"))
		return nil, nil
	}
	key, keyMeta, err := r.bashPPEvalElement(index.Index, shape.Key)
	if err != nil {
		r.bashPPGoSendError(index.Index, err)
		return nil, nil
	}
	table, valid := value.(map[string]any)
	if !valid && value != nil {
		r.bashPPGoSendError(index, fmt.Errorf("BASHPP-ECOLLECTION-STORAGE: map payload has type %T", value))
		return nil, nil
	}
	storage, _, found, err := r.bashPPSprint165MapLookup(meta, key, keyMeta, shape.Key)
	if err != nil {
		r.bashPPGoSendError(index.Index, err)
		return nil, nil
	}
	result, child := bashPPSprint165MapEntryValueFound(table, meta, storage, found)
	if !found {
		result, child = r.bashPPZeroValue(shape.Element)
	}
	result, child = bashPPCopyArrayValue(result, child)
	cell := &bashPPCell{declType: shape.Element}
	bashPPStoreCellValue(cell, result, child)
	return cell, &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: fmt.Sprint(found)}, scalarKind: constant.Bool, declType: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "bool"}}}
}
func metaType(meta *bashPPCollectionMeta) syntax.BashPPTypeExpr {
	if meta == nil {
		return nil
	}
	return meta.typ
}

// Native argument preparation must not probe a scalar index, then read it a
// second time. Collection keys and receiver/index calls may have side effects.
func (r *Runner) goSourceBridgeCollectionRead(expr syntax.BashPPExpr) (bashPPBridgeValue, bool, error) {
	if !r.bashPPGoSource {
		return bashPPBridgeValue{}, false, nil
	}
	var scalarBase syntax.BashPPExpr
	switch path := expr.(type) {
	case *syntax.BashPPIndexExpr:
		if path.GoString {
			scalarBase = path.X
		}
	case *syntax.BashPPSliceExpr:
		if path.GoString {
			scalarBase = path.X
		}
	}
	if scalarBase != nil {
		// A string lives in the scalar carrier, so its byte index must be
		// evaluated there before the general structured-read path tries to find
		// collection metadata. GoString comes from go/types, so a computed base
		// can be evaluated once without speculative execution.
		scalar, err := r.bashPPEvalScalarExpr(expr)
		if err != nil {
			return bashPPBridgeValue{}, true, err
		}
		value, err := bridgeScalar(scalar)
		return value, true, err
	}
	switch expr.(type) {
	case *syntax.BashPPIndexExpr, *syntax.BashPPSliceExpr, *syntax.BashPPSelectorExpr, *syntax.BashPPDerefExpr:
	default:
		return bashPPBridgeValue{}, false, nil
	}
	value, meta, err := r.bashPPReadExpr(expr)
	if err != nil {
		return bashPPBridgeValue{}, true, err
	}
	if meta != nil {
		result, err := r.bashPPBridgeCollection(value, meta, meta.typ)
		return result, true, err
	}
	cell := r.goSourceCollectionReadCell(expr, value, meta)
	result, err := bridgeScalar(r.bashPPScalarFromCell(cell))
	if err != nil {
		return result, true, err
	}
	result, err = r.bashPPBridgeDefinedScalar(result)
	return bashPPBridgeInstantiatedScalar(result, cell.declType), true, err
}
func (r *Runner) goSourceCollectionReadCell(expr syntax.BashPPExpr, value any, meta *bashPPCollectionMeta) *bashPPCell {
	cell := &bashPPCell{declType: r.bashPPExprScalarType(expr)}
	if meta != nil {
		cell.declType = meta.typ
	}
	bashPPStoreCellValue(cell, value, meta)
	if meta == nil {
		switch v := value.(type) {
		case string:
			if special, ok := bashPPNonFiniteComplexText(v); ok && r.bashPPStringCarriesComplex(cell.declType, v) {
				cell.nonFiniteComplex, cell.hasNonFiniteComplex = special, true
				cell.typeName = bashPPTypeText(cell.declType)
				cell.scalarKind = constant.Complex
				break
			}
			// A large unsigned element stored as its decimal spelling keeps the
			// integer identity of its declared type: leave the scalar kind
			// unset so bashPPScalarFromCell reconstructs it as the integer it
			// is. An ordinary string element still carries as a string.
			if !r.bashPPStringCarriesInteger(cell.declType, v) && !r.bashPPStringCarriesComplex(cell.declType, v) {
				cell.scalarKind = constant.String
			}
		case bool:
			cell.scalarKind = constant.Bool
		case int:
			cell.scalarKind = constant.Int
		case float64:
			cell.scalarKind = constant.Float
		case complex64:
			cell.scalarKind = constant.Complex
			cell.typeName = bashPPTypeText(cell.declType)
			cell.nonFiniteComplex, cell.hasNonFiniteComplex = complex128(v), true
		case complex128:
			cell.scalarKind = constant.Complex
			cell.typeName = bashPPTypeText(cell.declType)
			cell.nonFiniteComplex, cell.hasNonFiniteComplex = v, true
		}
	}
	return cell
}

// A finite complex element crosses collection storage as its Go spelling.
// Leave its scalar kind unset so the declared destination type reconstructs
// the complex carrier rather than mistaking that spelling for a Go string.
func (r *Runner) bashPPStringCarriesComplex(typ syntax.BashPPTypeExpr, text string) bool {
	shape, ok := r.bashPPUnderlyingType(typ).(*syntax.BashPPNamedType)
	if !ok || (shape.Name.Value != "complex64" && shape.Name.Value != "complex128") {
		return false
	}
	if _, special := bashPPNonFiniteComplexText(text); special {
		return true
	}
	return bashPPParseComplex(text).Kind() == constant.Complex
}

// goSourceNativeSequenceContents materialises a dependency-owned array or
// slice as interpreter-owned storage for a local composite destination — a
// reflect-built [8]string asserted into a map key has only a handle here, and
// the handle cannot serve typed collection storage. Elements are read back
// one by one through the handle; each arrives as the transported value the
// dependency encodes, which bashPPBridgeContents already rebuilds.
func (r *Runner) goSourceNativeSequenceContents(native *bashPPBridgeValue, expected syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta, bool, error) {
	if !r.bashPPGoSource || native == nil || native.Kind != "handle" || r.bashPPNativeType(expected) {
		return nil, nil, false, nil
	}
	shape, ok := r.bashPPUnderlyingType(expected).(*syntax.BashPPCollectionType)
	if !ok || shape.Kind == "map" || !strings.HasPrefix(native.Type, "[") {
		return nil, nil, false, nil
	}
	length, err := r.bashPPNativeAccess(r.ectx, "len", *native, "")
	if err != nil {
		return nil, nil, true, err
	}
	n, err := strconv.Atoi(length.Text)
	if err != nil || n < 0 {
		return nil, nil, true, fmt.Errorf("BASHPP-ECOLLECTION-ELEMENT: native length %q is not a length", length.Text)
	}
	out := make([]any, 0, n)
	meta := &bashPPCollectionMeta{kind: shape.Kind, typ: expected}
	for i := range n {
		element, err := r.bashPPNativeAccess(r.ectx, "index", *native, "", bashPPBridgeValue{Kind: "int", Type: "int", Text: strconv.Itoa(i)})
		if err != nil {
			return nil, nil, true, err
		}
		value, child, err := r.bashPPBridgeContents(element, shape.Element)
		if err != nil {
			return nil, nil, true, fmt.Errorf("BASHPP-ECOLLECTION-ELEMENT: %v", err)
		}
		out = append(out, value)
		meta.sequence = append(meta.sequence, child)
	}
	return out, meta, true, nil
}

func (r *Runner) goSourceCheckNativeElement(value any, expected syntax.BashPPTypeExpr) error {
	native, ok := value.(*bashPPBridgeValue)
	if !ok || native == nil {
		return fmt.Errorf("BASHPP-EASSIGN-MISMATCH: native element requires a dependency value")
	}
	_, _, err := r.goSourceNativeAssignedValue(*native, expected)
	return err
}

func (r *Runner) goSourceNativeAssignedValue(native bashPPBridgeValue, expected syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta, error) {
	// The helper checks the actual authenticated value's Go type. Name equality
	// rejects legitimate concrete implementations of imported interfaces.
	assignable, err := r.bashPPNativeTypeRequest("assignable", expected, native)
	if err != nil {
		return nil, nil, err
	}
	if assignable.Kind != "bool" || assignable.Text != "true" {
		return nil, nil, fmt.Errorf("BASHPP-EASSIGN-MISMATCH: cannot use native %s as %s", native.Type, bashPPTypeText(expected))
	}
	// Preserve the static interface on a copy, never on the source binding.
	if assignable.Interface != "" {
		native.Interface = assignable.Interface
	}
	return &native, &bashPPCollectionMeta{typ: expected, interfaceValue: goSourceNativeValueCell(native).interfaceValue}, nil
}
