// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"fmt"
	"go/constant"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

type bashPPPointerStep struct {
	field string
	index int
	deref bool
}

// bashPPPointer names storage, not a copied value. Keeping the root cell makes
// aliases and closure captures exact; the scope cloner rewrites that edge at
// every process/task snapshot boundary.
type bashPPPointer struct {
	target *bashPPCell
	path   []bashPPPointerStep
	elem   syntax.BashPPTypeExpr
}

func bashPPPointerMeta(typ syntax.BashPPTypeExpr) *bashPPCollectionMeta {
	return &bashPPCollectionMeta{kind: "pointer", typ: typ}
}

func (r *Runner) bashPPPointerType(typ syntax.BashPPTypeExpr) (*syntax.BashPPPointerType, bool) {
	if pointer, ok := typ.(*syntax.BashPPPointerType); ok {
		return pointer, true
	}
	shape := r.bashPPUnderlyingType(typ)
	pointer, ok := shape.(*syntax.BashPPPointerType)
	return pointer, ok
}

func (r *Runner) bashPPPointerExprType(expr syntax.BashPPExpr, ptr *bashPPPointer) syntax.BashPPTypeExpr {
	switch x := expr.(type) {
	case *syntax.BashPPParenExpr:
		return r.bashPPPointerExprType(x.X, ptr)
	case *syntax.BashPPIdent:
		if cell := r.bashPPScope.lookup(x.Name.Value); cell != nil && cell.pointer {
			return cell.declType
		}
	case *syntax.BashPPSelectorExpr, *syntax.BashPPIndexExpr, *syntax.BashPPSliceExpr:
		_, meta, err := r.bashPPReadExpr(expr)
		if err == nil && meta != nil && meta.kind == "pointer" {
			return meta.typ
		}
	case *syntax.BashPPDerefExpr:
		outer, err := r.bashPPPointerExprValue(x.X)
		if err == nil && outer != nil {
			_, meta, _, readErr := outer.read()
			if readErr == nil && meta != nil && meta.kind == "pointer" {
				return meta.typ
			}
		}
	}
	if ptr == nil {
		return nil
	}
	return &syntax.BashPPPointerType{Element: ptr.elem}
}

func (r *Runner) bashPPValidatePointerType(typ syntax.BashPPTypeExpr) error {
	ptr, ok := r.bashPPPointerType(typ)
	if !ok || ptr.Element == nil {
		return fmt.Errorf("BASHPP-EPOINTER-TYPE: invalid pointer type %s", bashPPTypeText(typ))
	}
	if err := r.bashPPValidateValueType(ptr.Element, make(map[string]bool)); err != nil {
		return fmt.Errorf("BASHPP-EPOINTER-TYPE: %v", err)
	}
	return nil
}

func (r *Runner) bashPPPointerExprValue(expr syntax.BashPPExpr) (*bashPPPointer, error) {
	switch x := expr.(type) {
	case *syntax.BashPPParenExpr:
		return r.bashPPPointerExprValue(x.X)
	case *syntax.BashPPAddressExpr:
		return r.bashPPAddress(x.X)
	case *syntax.BashPPNewExpr:
		if err := r.bashPPValidateValueType(x.AllocType, make(map[string]bool)); err != nil {
			return nil, fmt.Errorf("BASHPP-EPOINTER-TYPE: %v", err)
		}
		value, meta := r.bashPPZeroValue(x.AllocType)
		cell := &bashPPCell{declType: x.AllocType}
		bashPPStoreCellValue(cell, value, meta)
		return &bashPPPointer{target: cell, elem: x.AllocType}, nil
	case *syntax.BashPPIdent:
		cell := r.bashPPScope.lookup(x.Name.Value)
		if cell == nil || !cell.pointer {
			return nil, fmt.Errorf("BASHPP-EPOINTER-TARGET: %s is not a pointer", x.Name.Value)
		}
		if cell.pointerValue == nil {
			return nil, nil
		}
		return cell.pointerValue, nil
	case *syntax.BashPPDerefExpr:
		outer, err := r.bashPPPointerExprValue(x.X)
		if err != nil {
			return nil, err
		}
		if outer == nil {
			return nil, fmt.Errorf("BASHPP-ENIL-DEREF: dereference of nil pointer")
		}
		value, _, _, err := outer.read()
		if err != nil {
			return nil, err
		}
		ptr, ok := value.(*bashPPPointer)
		if !ok {
			return nil, fmt.Errorf("BASHPP-EPOINTER-TARGET: dereference result is not a pointer")
		}
		return ptr, nil
	default:
		value, _, err := r.bashPPReadExpr(expr)
		if err != nil {
			return nil, err
		}
		ptr, ok := value.(*bashPPPointer)
		if !ok && value != nil {
			return nil, fmt.Errorf("BASHPP-EPOINTER-TARGET: expression is not a pointer")
		}
		return ptr, nil
	}
}

func (r *Runner) bashPPAddress(expr syntax.BashPPExpr) (*bashPPPointer, error) {
	for {
		paren, ok := expr.(*syntax.BashPPParenExpr)
		if !ok {
			break
		}
		expr = paren.X
	}
	if deref, ok := expr.(*syntax.BashPPDerefExpr); ok {
		return r.bashPPPointerExprValue(deref.X)
	}
	root, ok := bashPPCollectionRoot(expr)
	if !ok || r.bashPPScope == nil {
		return nil, fmt.Errorf("BASHPP-ENONADDRESSABLE: operand is not addressable")
	}
	cell := r.bashPPScope.lookup(root)
	if cell == nil {
		return nil, fmt.Errorf("BASHPP-EPOINTER-TARGET: undefined pointer target %s", root)
	}
	if cell.constant {
		return nil, fmt.Errorf("BASHPP-ENONADDRESSABLE: constant %s is not addressable", root)
	}
	ptr := &bashPPPointer{target: cell}
	typ := cell.declType
	meta := bashPPCellMeta(cell)
	if cell.pointer {
		if _, direct := expr.(*syntax.BashPPIdent); !direct {
			if cell.pointerValue == nil {
				return nil, fmt.Errorf("BASHPP-ENIL-DEREF: dereference of nil pointer")
			}
			base := cell.pointerValue
			ptr = &bashPPPointer{target: base.target, path: append([]bashPPPointerStep(nil), base.path...), elem: base.elem}
			typ = base.elem
			_, meta, _, _ = base.read()
		}
	}
	if typ == nil && meta != nil {
		typ = meta.typ
	}
	if typ == nil {
		typ = bashPPInferredCellType(cell)
	}
	var descend func(syntax.BashPPExpr) error
	descend = func(node syntax.BashPPExpr) error {
		switch x := node.(type) {
		case *syntax.BashPPIdent:
			return nil
		case *syntax.BashPPSelectorExpr:
			if err := descend(x.X); err != nil {
				return err
			}
			sel := r.bashPPResolveField(typ, x.Sel.Value)
			if sel.ambiguous || len(sel.edges) == 0 {
				return bashPPSelectionError(typ, x.Sel.Value, sel)
			}
			for i, edge := range sel.edges {
				ptr.path = append(ptr.path, bashPPPointerStep{field: edge.name})
				if meta != nil {
					meta = meta.mapping[edge.name]
				}
				if edge.pointer && i+1 < len(sel.edges) {
					ptr.path = append(ptr.path, bashPPPointerStep{deref: true})
					meta = nil
				}
			}
			typ = sel.fieldType
			return nil
		case *syntax.BashPPIndexExpr:
			if err := descend(x.X); err != nil {
				return err
			}
			collection, found := typ.(*syntax.BashPPCollectionType)
			if !found {
				return fmt.Errorf("BASHPP-EPOINTER-TARGET: indexed target is not a collection")
			}
			if collection.Kind == "map" {
				return fmt.Errorf("BASHPP-ENONADDRESSABLE: map elements are not addressable")
			}
			i, err := r.bashPPCollectionIndex(x.Index)
			if err != nil {
				return err
			}
			value, _, err := r.bashPPReadExpr(x.X)
			if err != nil {
				return err
			}
			seq := value.([]any)
			if i < 0 || i >= len(seq) {
				return fmt.Errorf("BASHPP-ECOLLECTION-BOUNDS: index %d out of bounds for length %d", i, len(seq))
			}
			ptr.path = append(ptr.path, bashPPPointerStep{index: i})
			typ = collection.Element
			if meta != nil {
				meta = meta.sequence[i]
			}
			return nil
		default:
			return fmt.Errorf("BASHPP-ENONADDRESSABLE: operand is not addressable")
		}
	}
	if err := descend(expr); err != nil {
		return nil, err
	}
	ptr.elem = typ
	return ptr, nil
}

func bashPPInferredCellType(cell *bashPPCell) syntax.BashPPTypeExpr {
	name := cell.typeName
	if name == "" {
		value := bashPPScalarFromString(cell.vr.String()).value
		switch value.Kind() {
		case constant.Bool:
			name = "bool"
		case constant.Int:
			name = "int"
		case constant.Float:
			name = "float64"
		default:
			name = "string"
		}
	}
	return &syntax.BashPPNamedType{Name: &syntax.Lit{Value: name}}
}

func (p *bashPPPointer) read() (any, *bashPPCollectionMeta, syntax.BashPPTypeExpr, error) {
	if p == nil || p.target == nil {
		return nil, nil, nil, fmt.Errorf("BASHPP-ENIL-DEREF: dereference of nil pointer")
	}
	var value any
	meta := bashPPCellMeta(p.target)
	if p.target.pointer {
		value = p.target.pointerValue
	} else if p.target.vr.Kind == expand.Object {
		value = p.target.vr.Obj
	} else {
		value = bashPPScalarValue(p.target.vr.String())
	}
	for _, step := range p.path {
		if step.deref {
			pointer, ok := value.(*bashPPPointer)
			if !ok || pointer == nil {
				return nil, nil, nil, fmt.Errorf("BASHPP-ENIL-DEREF: dereference of nil embedded pointer")
			}
			var err error
			value, meta, _, err = pointer.read()
			if err != nil {
				return nil, nil, nil, err
			}
			continue
		}
		if step.field != "" {
			mapping, ok := value.(map[string]any)
			if !ok {
				return nil, nil, nil, fmt.Errorf("BASHPP-EPOINTER-TARGET: pointer field path no longer names struct storage")
			}
			var found bool
			value, found = mapping[step.field]
			if !found {
				return nil, nil, nil, fmt.Errorf("BASHPP-EPOINTER-TARGET: pointer field %q no longer exists", step.field)
			}
			if meta != nil {
				meta = meta.mapping[step.field]
			}
		} else {
			seq, ok := value.([]any)
			if !ok || step.index < 0 || step.index >= len(seq) {
				return nil, nil, nil, fmt.Errorf("BASHPP-EPOINTER-TARGET: pointer index no longer names collection storage")
			}
			value = seq[step.index]
			if meta != nil {
				meta = meta.sequence[step.index]
			}
		}
	}
	return value, meta, p.elem, nil
}

func bashPPScalarValue(text string) any {
	v := bashPPScalarFromString(text).value
	switch v.Kind() {
	case constant.Bool:
		return v.String() == "true"
	case constant.String:
		return constant.StringVal(v)
	case constant.Int:
		i, _ := constant.Int64Val(v)
		return int(i)
	case constant.Float:
		f, _ := constant.Float64Val(v)
		return f
	}
	return text
}

func bashPPStoreCellValue(cell *bashPPCell, value any, meta *bashPPCollectionMeta) {
	if meta != nil && meta.interfaceValue != nil {
		cell.pointer, cell.pointerValue, cell.nilPointer = false, nil, false
		cell.interfaceValue = meta.interfaceValue
		cell.valueMeta = nil
		cell.object = nil
		if meta.interfaceValue.nilIface || meta.interfaceValue.cell == nil {
			cell.vr = expand.Variable{Set: true, Kind: expand.String}
		} else {
			cell.vr = meta.interfaceValue.cell.vr
		}
		return
	}
	cell.interfaceValue = nil
	if ptr, ok := value.(*bashPPPointer); ok || value == nil {
		if _, pointerType := cell.declType.(*syntax.BashPPPointerType); pointerType || meta != nil && meta.kind == "pointer" {
			cell.pointer, cell.pointerValue, cell.nilPointer = true, ptr, ptr == nil
			cell.vr = expand.Variable{Set: true, Kind: expand.String}
			return
		}
	}
	cell.pointer, cell.pointerValue, cell.nilPointer = false, nil, false
	if meta != nil {
		cell.vr, cell.valueMeta = expand.NewObject(value), meta
		if cell.object == nil {
			cell.object = &bashPPObjectIdentity{collection: meta}
		}
		return
	}
	cell.vr = expand.Variable{Set: true, Kind: expand.String, Str: fmt.Sprint(value)}
}

func (r *Runner) bashPPBindPointerExpr(name string, expr syntax.BashPPExpr) bool {
	var value any
	var meta *bashPPCollectionMeta
	var typ syntax.BashPPTypeExpr
	switch x := expr.(type) {
	case *syntax.BashPPAddressExpr, *syntax.BashPPNewExpr:
		ptr, err := r.bashPPPointerExprValue(expr)
		if err != nil {
			r.errf("%v\n", err)
			r.exit.code = 2
			return true
		}
		value, typ = ptr, &syntax.BashPPPointerType{Element: ptr.elem}
	case *syntax.BashPPIdent:
		cell := r.bashPPScope.lookup(x.Name.Value)
		if cell == nil || !cell.pointer {
			return false
		}
		value, typ = cell.pointerValue, cell.declType
	case *syntax.BashPPDerefExpr:
		ptr, err := r.bashPPPointerExprValue(x.X)
		if err != nil {
			r.errf("%v\n", err)
			r.exit.code = 2
			return true
		}
		if ptr == nil {
			r.errf("BASHPP-ENIL-DEREF: dereference of nil pointer\n")
			r.exit.code = 2
			return true
		}
		value, meta, typ, err = ptr.read()
		if err != nil {
			r.errf("%v\n", err)
			r.exit.code = 2
			return true
		}
	default:
		return false
	}
	r.bashPPDeclareName(name, expand.Variable{Set: true, Kind: expand.String})
	cell := r.bashPPScope.lookup(name)
	cell.declType = typ
	if ptrType, ok := typ.(*syntax.BashPPPointerType); ok {
		if named, ok := ptrType.Element.(*syntax.BashPPNamedType); ok {
			cell.typeName = named.Name.Value
		}
	}
	bashPPStoreCellValue(cell, value, meta)
	if cell.object != nil && cell.object.owner == "" {
		cell.object.owner = name
	}
	return true
}

func (r *Runner) bashPPDerefAssign(target *syntax.BashPPDerefExpr, rhs syntax.BashPPExpr) {
	ptr, err := r.bashPPPointerExprValue(target.X)
	if err == nil && ptr == nil {
		err = fmt.Errorf("BASHPP-ENIL-DEREF: dereference of nil pointer")
	}
	if err != nil {
		r.errf("%v\n", err)
		r.exit.code = 2
		return
	}
	value, meta, err := r.bashPPEvalTypedValue(rhs, ptr.elem)
	if err != nil {
		r.errf("BASHPP-EASSIGN-MISMATCH: %v\n", err)
		r.exit.code = 2
		return
	}
	if ptr.target.object != nil && ptr.target.object.readonly {
		r.errf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value %q through pointer\n", ptr.target.object.owner)
		r.exit.code = 2
		return
	}
	if ptr.target.constant || ptr.target.vr.ReadOnly {
		r.errf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value through pointer\n")
		r.exit.code = 2
		return
	}
	if len(ptr.path) == 0 {
		bashPPStoreCellValue(ptr.target, value, meta)
		return
	}
	parent, parentMeta, _, err := ptr.readParent()
	if err != nil {
		r.errf("%v\n", err)
		r.exit.code = 2
		return
	}
	last := ptr.path[len(ptr.path)-1]
	if last.field != "" {
		mapping, ok := parent.(map[string]any)
		if !ok {
			r.errf("BASHPP-EPOINTER-TARGET: pointer field path no longer names struct storage\n")
			r.exit.code = 2
			return
		}
		mapping[last.field] = value
		if parentMeta != nil {
			parentMeta.mapping[last.field] = meta
		}
	} else {
		sequence, ok := parent.([]any)
		if !ok || last.index < 0 || last.index >= len(sequence) {
			r.errf("BASHPP-EPOINTER-TARGET: pointer index no longer names collection storage\n")
			r.exit.code = 2
			return
		}
		sequence[last.index] = value
		if parentMeta != nil {
			parentMeta.sequence[last.index] = meta
		}
	}
}

func (p *bashPPPointer) readParent() (any, *bashPPCollectionMeta, syntax.BashPPTypeExpr, error) {
	copy := *p
	copy.path = p.path[:len(p.path)-1]
	return copy.read()
}
