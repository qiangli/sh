// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"fmt"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

type bashPPEmbedEdge struct {
	name    string
	pointer bool
}

type bashPPSelection struct {
	edges         []bashPPEmbedEdge
	fieldType     syntax.BashPPTypeExpr
	method        *bashPPFunc
	receiverType  syntax.BashPPTypeExpr
	interfaceSpec *syntax.BashPPMethodSpec
	ambiguous     bool
}

type bashPPSelectionNode struct {
	typ       syntax.BashPPTypeExpr
	edges     []bashPPEmbedEdge
	indirect  bool
	ancestors map[string]bool
}

func bashPPEmbeddedFieldName(field *syntax.BashPPField) (string, bool) {
	if field == nil || !field.Embedded || field.FieldTypeExpr == nil {
		return "", false
	}
	typ := field.FieldTypeExpr
	if pointer, ok := typ.(*syntax.BashPPPointerType); ok {
		typ = pointer.Element
	}
	named, ok := typ.(*syntax.BashPPNamedType)
	if !ok || named.Name == nil {
		return "", false
	}
	return named.Name.Value, true
}

// bashPPDeclaredFieldNames lists every selector name one field contributes. An
// embedded field contributes the implicit name of its type; a named field
// contributes each name in its declaration group, so Go's grouped
// `X, Y float64` form is selectable under both names rather than under neither.
func bashPPDeclaredFieldNames(field *syntax.BashPPField) []string {
	if name, ok := bashPPEmbeddedFieldName(field); ok {
		return []string{name}
	}
	if field == nil || len(field.Names) == 0 {
		return nil
	}
	names := make([]string, 0, len(field.Names))
	for _, n := range field.Names {
		names = append(names, n.Value)
	}
	return names
}

// bashPPEmbeddedTarget resolves only aliases while retaining defined type
// identity. An alias may denote a pointer and is then an indirect embedded
// edge; a defined pointer type is not a legal embedded field in Go.
func (r *Runner) bashPPEmbeddedTarget(typ syntax.BashPPTypeExpr) (target syntax.BashPPTypeExpr, indirect, definedPointer, iface bool) {
	seen := make(map[string]bool)
	for {
		if pointer, ok := typ.(*syntax.BashPPPointerType); ok {
			if indirect {
				return nil, false, true, false
			}
			indirect = true
			typ = pointer.Element
			continue
		}
		if _, ok := typ.(*syntax.BashPPInterfaceType); ok {
			return typ, indirect, false, true
		}
		named, ok := typ.(*syntax.BashPPNamedType)
		if !ok || named.Name == nil {
			return typ, indirect, false, false
		}
		if seen[named.Name.Value] {
			return nil, false, true, false
		}
		seen[named.Name.Value] = true
		decl, found := r.bashPPTypes[named.Name.Value]
		if !found {
			return typ, indirect, false, false
		}
		if !decl.alias {
			switch r.bashPPInstantiateNamedType(named).(type) {
			case *syntax.BashPPPointerType:
				return nil, false, true, false
			case *syntax.BashPPInterfaceType:
				return typ, indirect, false, true
			default:
				return typ, indirect, false, false
			}
		}
		typ = r.bashPPInstantiateNamedType(named)
	}
}

func bashPPNamedOwner(typ syntax.BashPPTypeExpr) (string, syntax.BashPPTypeExpr, bool) {
	if pointer, ok := typ.(*syntax.BashPPPointerType); ok {
		typ = pointer.Element
	}
	named, ok := typ.(*syntax.BashPPNamedType)
	if !ok || named.Name == nil {
		return "", nil, false
	}
	return named.Name.Value, typ, true
}

func (r *Runner) bashPPMethodOwner(typ syntax.BashPPTypeExpr) (string, syntax.BashPPTypeExpr, bool) {
	if pointer, ok := typ.(*syntax.BashPPPointerType); ok {
		typ = pointer.Element
	}
	seen := make(map[string]bool)
	for {
		named, ok := typ.(*syntax.BashPPNamedType)
		if !ok || named.Name == nil {
			return "", nil, false
		}
		if seen[named.Name.Value] {
			return "", nil, false
		}
		seen[named.Name.Value] = true
		decl, found := r.bashPPTypes[named.Name.Value]
		if !found || !decl.alias {
			return named.Name.Value, typ, true
		}
		typ = r.bashPPInstantiateNamedType(named)
	}
}

// bashPPResolveSelection implements breadth-first Go selector lookup.
func (r *Runner) bashPPResolveSelection(root syntax.BashPPTypeExpr, name string, methods, addressable bool) bashPPSelection {
	rootPointer := false
	if pointer, ok := root.(*syntax.BashPPPointerType); ok {
		rootPointer, root = true, pointer.Element
	}
	key := bashPPTypeText(root)
	level := []bashPPSelectionNode{{typ: root, indirect: rootPointer, ancestors: map[string]bool{key: true}}}
	for len(level) > 0 {
		var matches []bashPPSelection
		var next []bashPPSelectionNode
		for _, node := range level {
			if methods {
				if iface, ok := r.bashPPInterfaceType(node.typ); ok {
					set, err := r.bashPPInterfaceMethodSet(bashPPTypeText(node.typ), iface, make(map[string]bool))
					if err == nil {
						if candidate, found := set.byName[name]; found {
							matches = append(matches, bashPPSelection{edges: append([]bashPPEmbedEdge(nil), node.edges...), interfaceSpec: candidate.spec})
						}
					}
				}
			}
			fields, _, isStruct := r.bashPPStructFields(node.typ)
			if isStruct {
				for _, field := range fields {
					for _, fieldName := range bashPPDeclaredFieldNames(field) {
						if fieldName != name {
							continue
						}
						edges := append([]bashPPEmbedEdge(nil), node.edges...)
						edges = append(edges, bashPPEmbedEdge{name: fieldName})
						matches = append(matches, bashPPSelection{edges: edges, fieldType: field.FieldTypeExpr})
					}
				}
			}
			if methods {
				if owner, receiverType, ok := r.bashPPMethodOwner(node.typ); ok {
					fn := r.bashPPMethods[owner][name]
					if fn != nil && (!fn.decl.Receiver.Pointer || addressable || node.indirect) {
						matches = append(matches, bashPPSelection{edges: append([]bashPPEmbedEdge(nil), node.edges...), method: fn, receiverType: receiverType})
					}
				}
			}
			if !isStruct {
				continue
			}
			for _, field := range fields {
				fieldName, ok := bashPPEmbeddedFieldName(field)
				if !ok {
					continue
				}
				child, pointer, invalid, _ := r.bashPPEmbeddedTarget(field.FieldTypeExpr)
				if invalid || child == nil {
					continue
				}
				childKey := bashPPTypeText(child)
				if node.ancestors[childKey] {
					continue
				}
				ancestors := make(map[string]bool, len(node.ancestors)+1)
				for ancestor := range node.ancestors {
					ancestors[ancestor] = true
				}
				ancestors[childKey] = true
				edges := append(append([]bashPPEmbedEdge(nil), node.edges...), bashPPEmbedEdge{name: fieldName, pointer: pointer})
				next = append(next, bashPPSelectionNode{typ: child, edges: edges, indirect: node.indirect || pointer, ancestors: ancestors})
			}
		}
		if len(matches) > 0 {
			if len(matches) != 1 {
				return bashPPSelection{ambiguous: true}
			}
			return matches[0]
		}
		level = next
	}
	return bashPPSelection{}
}

func (r *Runner) bashPPResolveField(typ syntax.BashPPTypeExpr, name string) bashPPSelection {
	return r.bashPPResolveSelection(typ, name, false, false)
}

func bashPPSelectionError(typ syntax.BashPPTypeExpr, name string, sel bashPPSelection) error {
	if sel.ambiguous {
		return fmt.Errorf("BASHPP-ESELECTOR-AMBIGUOUS: ambiguous selector %s.%s", bashPPTypeText(typ), name)
	}
	return fmt.Errorf("BASHPP-ESELECTOR-UNKNOWN: %s has no field or method %q", bashPPTypeText(typ), name)
}

func bashPPDerefEmbedded(value any, meta *bashPPCollectionMeta) (any, *bashPPCollectionMeta, error) {
	if value == nil && meta != nil && meta.kind == "pointer" {
		return nil, nil, fmt.Errorf("BASHPP-ENIL-DEREF: dereference of nil embedded pointer")
	}
	pointer, ok := value.(*bashPPPointer)
	if !ok {
		return value, meta, nil
	}
	if pointer == nil {
		return nil, nil, fmt.Errorf("BASHPP-ENIL-DEREF: dereference of nil embedded pointer")
	}
	value, meta, _, err := pointer.read()
	return value, meta, err
}

func bashPPReadSelection(value any, meta *bashPPCollectionMeta, edges []bashPPEmbedEdge) (any, *bashPPCollectionMeta, error) {
	for i, edge := range edges {
		var err error
		value, meta, err = bashPPDerefEmbedded(value, meta)
		if err != nil {
			return nil, nil, err
		}
		mapping, ok := value.(map[string]any)
		if !ok || meta == nil || meta.kind != "struct" {
			return nil, nil, fmt.Errorf("BASHPP-ESELECTOR-TYPE: promoted path no longer names struct storage")
		}
		var found bool
		value, found = mapping[edge.name]
		if !found {
			return nil, nil, fmt.Errorf("BASHPP-ESELECTOR-UNKNOWN: embedded field %q is missing", edge.name)
		}
		meta = meta.mapping[edge.name]
		if edge.pointer && i+1 < len(edges) {
			value, meta, err = bashPPDerefEmbedded(value, meta)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	return value, meta, nil
}

func bashPPEmbeddedAddress(rootCell *bashPPCell, edges []bashPPEmbedEdge, elem syntax.BashPPTypeExpr) (*bashPPPointer, error) {
	ptr := &bashPPPointer{target: rootCell, elem: elem}
	if rootCell.pointer {
		if rootCell.pointerValue == nil {
			return nil, fmt.Errorf("BASHPP-ENIL-DEREF: dereference of nil pointer")
		}
		base := rootCell.pointerValue
		ptr.target = base.target
		ptr.path = append([]bashPPPointerStep(nil), base.path...)
	}
	for i, edge := range edges {
		ptr.path = append(ptr.path, bashPPPointerStep{field: edge.name})
		if edge.pointer && i+1 < len(edges) {
			ptr.path = append(ptr.path, bashPPPointerStep{deref: true})
		}
	}
	return ptr, nil
}

func (r *Runner) bashPPEmbeddedReceiver(rootCell *bashPPCell, sel bashPPSelection) (*bashPPCell, error) {
	if len(sel.edges) == 0 {
		return rootCell, nil
	}
	var value any
	meta := bashPPCellMeta(rootCell)
	if rootCell.pointer {
		if rootCell.pointerValue == nil {
			return nil, fmt.Errorf("BASHPP-ENIL-DEREF: dereference of nil pointer")
		}
		var err error
		value, meta, _, err = rootCell.pointerValue.read()
		if err != nil {
			return nil, err
		}
	} else if rootCell.vr.Kind == expand.Object {
		value = rootCell.vr.Obj
	} else {
		value = bashPPScalarValue(rootCell.vr.String())
	}
	value, meta, err := bashPPReadSelection(value, meta, sel.edges)
	if err != nil {
		return nil, err
	}
	cell := &bashPPCell{declType: sel.receiverType, object: rootCell.object}
	if owner, _, ok := bashPPNamedOwner(sel.receiverType); ok {
		cell.typeName = owner
	}
	bashPPStoreCellValue(cell, value, meta)
	if cell.vr.Kind == expand.Object && cell.object == nil {
		cell.object = rootCell.object
	}
	return cell, nil
}

func (r *Runner) bashPPBindPromotedMethod(rootCell *bashPPCell, method string, sel bashPPSelection, addressable bool) (*bashPPFunc, bool) {
	if sel.interfaceSpec != nil {
		var value any
		meta := bashPPCellMeta(rootCell)
		var err error
		if rootCell.pointer {
			if rootCell.pointerValue == nil {
				r.errf("BASHPP-ENIL-DEREF: dereference of nil pointer\n")
				r.exit.code = 2
				return nil, false
			}
			value, meta, _, err = rootCell.pointerValue.read()
		} else {
			value = rootCell.vrValue()
		}
		if err != nil {
			r.errf("%v\n", err)
			r.exit.code = 2
			return nil, false
		}
		value, meta, err = bashPPReadSelection(value, meta, sel.edges)
		_ = value
		if err != nil {
			r.errf("%v\n", err)
			r.exit.code = 2
			return nil, false
		}
		if meta == nil || meta.interfaceValue == nil {
			r.errf("BASHPP-EINTERFACE-VALUE: promoted interface method %s has no interface storage\n", method)
			r.exit.code = 2
			return nil, false
		}
		return r.bashPPBindInterfaceMethod(meta.interfaceValue, method)
	}
	receiver, err := r.bashPPEmbeddedReceiver(rootCell, sel)
	if err != nil {
		r.errf("%v\n", err)
		r.exit.code = 2
		return nil, false
	}
	methodOwner := sel.method.decl.Receiver.RecvType.Value
	if receiver.typeName != methodOwner {
		copyCell := *receiver
		copyCell.typeName = methodOwner
		copyCell.declType = sel.receiverType
		if receiver.pointer {
			copyCell.declType = &syntax.BashPPPointerType{Element: sel.receiverType}
		}
		receiver = &copyCell
	}
	if sel.method.decl.Receiver.Pointer && !receiver.pointer && len(sel.edges) > 0 {
		ptr, err := bashPPEmbeddedAddress(rootCell, sel.edges, sel.receiverType)
		if err != nil {
			r.errf("%v\n", err)
			r.exit.code = 2
			return nil, false
		}
		owner, _, _ := bashPPNamedOwner(sel.receiverType)
		receiver = &bashPPCell{
			vr:           expand.Variable{Set: true, Kind: expand.String},
			declType:     &syntax.BashPPPointerType{Element: sel.receiverType},
			typeName:     owner,
			pointer:      true,
			pointerValue: ptr,
		}
	}
	return r.bashPPBindMethod(receiver, method, addressable)
}
