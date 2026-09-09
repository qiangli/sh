package interp

// Sprint: #118; Story: #52; Story-ID: d564bada90bb
import (
	"fmt"
	"go/constant"

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
	arg := bashPPBuiltinArg{cell: cell, typ: cell.declType, channel: cell.channel, text: bashPPWordSource(call.Args[index])}
	if cell.pointer {
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
	return arg, nil
}

func (r *Runner) goSourceCollectionCallValue(expr syntax.BashPPExpr) (any, *bashPPCollectionMeta, bool, error) {
	if !r.bashPPGoSource {
		return nil, nil, false, nil
	}
	if _, ok := expr.(*syntax.BashPPCall); !ok {
		return nil, nil, false, nil
	}
	cell, err := r.goSourceValueCell(expr)
	if err != nil {
		return nil, nil, true, err
	}
	if cell == nil {
		return nil, nil, true, fmt.Errorf("Go collection call has no result")
	}
	if cell.vr.Kind == expand.Object {
		return cell.vr.Obj, bashPPCellMeta(cell), true, nil
	}
	return bashPPScalarAny(r.bashPPScalarFromCell(cell).value), nil, true, nil
}

func (r *Runner) goSourceMapCommaDecl(d *syntax.BashPPShortDecl) bool {
	if !r.bashPPGoSource || len(d.Lhs) != 2 {
		return false
	}
	index, ok := d.Expr.(*syntax.BashPPIndexExpr)
	if !ok {
		return false
	}
	value, meta, err := r.bashPPReadExpr(index.X)
	if err != nil {
		r.bashPPGoSendError(index.X, err)
		return true
	}
	shape, ok := r.bashPPUnderlyingType(metaType(meta)).(*syntax.BashPPCollectionType)
	if !ok || meta == nil || meta.kind != "map" {
		r.bashPPGoSendError(index, fmt.Errorf("comma-ok index requires a represented map"))
		return true
	}
	key, _, err := r.bashPPEvalElement(index.Index, shape.Key)
	if err != nil {
		r.bashPPGoSendError(index.Index, err)
		return true
	}
	table, valid := value.(map[string]any)
	if !valid && value != nil {
		r.bashPPGoSendError(index, fmt.Errorf("BASHPP-ECOLLECTION-STORAGE: map payload has type %T", value))
		return true
	}
	canonical := fmt.Sprint(key)
	result, found := table[canonical]
	child := meta.mapping[canonical]
	if !found {
		result, child = r.bashPPZeroValue(shape.Element)
	}
	result, child = bashPPCopyArrayValue(result, child)
	cell := &bashPPCell{declType: shape.Element}
	bashPPStoreCellValue(cell, result, child)
	r.bashPPBindReceivedCell(d.Lhs[0].Value, cell)
	r.bashPPBindReceivedCell(d.Lhs[1].Value, &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: fmt.Sprint(found)}, scalarKind: constant.Bool, declType: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "bool"}}})
	return true
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
	return result, true, err
}
func (r *Runner) goSourceCollectionReadCell(expr syntax.BashPPExpr, value any, meta *bashPPCollectionMeta) *bashPPCell {
	cell := &bashPPCell{declType: r.bashPPExprScalarType(expr)}
	if meta != nil {
		cell.declType = meta.typ
	}
	bashPPStoreCellValue(cell, value, meta)
	if meta == nil {
		switch value.(type) {
		case string:
			cell.scalarKind = constant.String
		case bool:
			cell.scalarKind = constant.Bool
		case int:
			cell.scalarKind = constant.Int
		case float64:
			cell.scalarKind = constant.Float
		}
	}
	return cell
}

func (r *Runner) goSourceCheckNativeElement(value any, expected syntax.BashPPTypeExpr) error {
	native, ok := value.(*bashPPBridgeValue)
	if !ok || native == nil {
		return fmt.Errorf("BASHPP-EASSIGN-MISMATCH: native element requires a dependency value")
	}
	identity, err := r.bashPPNativeTypeRequest("type", expected)
	if err != nil {
		return err
	}
	if native.Type != identity.Text && native.NativeType != identity.Text {
		return fmt.Errorf("BASHPP-EASSIGN-MISMATCH: cannot use native %s as %s", native.Type, identity.Text)
	}
	return nil
}
