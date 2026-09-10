package interp

// Sprint: #118; Story: #67; Story-ID: 83b5cdc6fca6
import (
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// goSourceBindVariadic binds a Go-source variadic parameter to the slice its
// body actually receives.
//
// Go states the two call spellings separately and so does this. `f(a, b)`
// allocates a fresh []T holding the trailing arguments, which the callee may
// reslice or overwrite without the caller observing it. `f(xs...)` passes xs
// itself: no copy is made, the callee writes through to the caller's backing
// array, and a nil xs arrives nil rather than as an empty slice. Collapsing the
// two would quietly drop that aliasing.
//
// The Bash++ dialect binds the same parameter to an indexed shell variable,
// because its bodies reach the arguments through `${rest[@]}`. An original Go
// body reaches them as a slice — `fmt.Print(nums)`, `len(nums)`, `nums[0]`,
// `for range nums` — none of which the indexed binding answers to.
func (r *Runner) goSourceBindVariadic(param bashPPParam, args []string, cells []*bashPPCell, spread bool) bool {
	typ := &syntax.BashPPCollectionType{Kind: "slice", Element: param.typ}
	if spread {
		return r.goSourceBindSpreadVariadic(param, cells, typ)
	}
	sequence := make([]any, len(args))
	metas := make([]*bashPPCollectionMeta, len(args))
	for i := range args {
		var cell *bashPPCell
		if i < len(cells) {
			cell = cells[i]
		}
		sequence[i], metas[i] = r.goSourceVariadicElement(args[i], cell)
	}
	meta := &bashPPCollectionMeta{kind: "slice", typ: typ, sequence: metas}
	r.bashPPScope.entries[param.name] = &bashPPCell{
		vr:        expand.NewObject(sequence),
		object:    &bashPPObjectIdentity{collection: meta},
		valueMeta: meta,
		declType:  typ,
	}
	return true
}

// goSourceBindSpreadVariadic binds `f(xs...)`, where the callee's parameter and
// the caller's xs are one slice. The cell is copied — the callee has its own
// name for it and may reslice — while the payload it points at is shared,
// which is what makes a write through the parameter visible to the caller.
func (r *Runner) goSourceBindSpreadVariadic(param bashPPParam, cells []*bashPPCell, typ syntax.BashPPTypeExpr) bool {
	if len(cells) != 1 || cells[0] == nil {
		r.errf("BASHPP-EARG-SPREAD: %s... has no slice value to spread\n", param.name)
		r.exit = exitStatus{code: 2}
		return false
	}
	cell := bashPPCopyAssignmentCell(cells[0])
	cell.channel, cell.channelOwner = nil, nil
	cell.constant, cell.vr.ReadOnly, cell.vr.Exported = false, false, false
	if cell.declType == nil {
		cell.declType = typ
	}
	r.bashPPScope.entries[param.name] = cell
	return true
}

// goSourceVariadicElement renders one trailing argument as a slice element.
// The cell is what carries pointer, interface and aggregate identity; the
// string is only the fallback for an argument that never produced one.
func (r *Runner) goSourceVariadicElement(arg string, cell *bashPPCell) (any, *bashPPCollectionMeta) {
	switch {
	case cell == nil:
		return arg, nil
	case cell.interfaceValue != nil:
		return cell.vrValue(), &bashPPCollectionMeta{
			kind: "interface", typ: cell.declType, interfaceValue: cell.interfaceValue,
		}
	case cell.pointer:
		return cell.pointerValue, bashPPPointerMeta(cell.declType)
	case cell.vr.Kind == expand.Object:
		return bashPPCopyArrayValue(cell.vr.Obj, bashPPCellMeta(cell))
	}
	return bashPPScalarAny(r.bashPPScalarFromCell(cell).value), nil
}
