// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"errors"
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
	if target, nilConversion := r.bashPPNilPointerConversion(expr); nilConversion {
		return target
	}
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

// bashPPPointerConversion settles `(*T)(p)` — a conversion whose target is
// a pointer type — to the same pointer retyped: it names the storage p
// names, and dereferences and method selection read it as a T. It reports
// whether the expression was such a conversion; a nil operand stays nil.
func (r *Runner) bashPPPointerConversion(expr syntax.BashPPExpr) (*bashPPPointer, *syntax.BashPPPointerType, bool, error) {
	for {
		paren, ok := expr.(*syntax.BashPPParenExpr)
		if !ok {
			break
		}
		expr = paren.X
	}
	conv, ok := expr.(*syntax.BashPPConvertExpr)
	if !ok {
		return nil, nil, false, nil
	}
	target, ok := r.bashPPPointerType(r.bashPPConvertTarget(conv))
	if !ok || target.Element == nil {
		return nil, nil, false, nil
	}
	if goSourceNilLiteral(conv.X) {
		return nil, target, true, nil
	}
	if ptr, converted, err := r.bashPPSliceToArrayPointer(conv, target); converted {
		return ptr, target, true, err
	}
	ptr, err := r.bashPPPointerExprValue(conv.X)
	if err != nil || ptr == nil {
		return nil, target, true, err
	}
	retyped := *ptr
	retyped.elem = target.Element
	return &retyped, target, true, nil
}

// bashPPSliceToArrayPointer applies Go's `(*[N]T)(s)` conversion of a slice
// to a pointer to its underlying array: the result aliases the slice's
// storage (a write through it is a write to the slice), a nil slice converts
// to a nil pointer, and a slice shorter than N raises Go's runtime error
// rather than a refusal. It reports whether the conversion was such a shape;
// a pointer operand is left to the retyping path, since `(*T)(p)` keeps its
// own meaning when T is an array type.
func (r *Runner) bashPPSliceToArrayPointer(conv *syntax.BashPPConvertExpr, target *syntax.BashPPPointerType) (*bashPPPointer, bool, error) {
	array, ok := r.bashPPUnderlyingType(target.Element).(*syntax.BashPPCollectionType)
	if !ok || array.Kind != "array" || array.Length == nil || r.bashPPScope == nil {
		return nil, false, nil
	}
	operand := conv.X
	for {
		paren, ok := operand.(*syntax.BashPPParenExpr)
		if !ok {
			break
		}
		operand = paren.X
	}
	root, ok := bashPPCollectionRoot(operand)
	if !ok {
		return nil, false, nil
	}
	cell := r.bashPPScope.lookup(root)
	if cell == nil {
		return nil, false, nil
	}
	if _, direct := operand.(*syntax.BashPPIdent); direct && cell.pointer {
		return nil, false, nil
	}
	ptr, err := r.bashPPAddress(operand)
	if err != nil {
		return nil, false, nil
	}
	value, meta, _, err := ptr.read()
	if err != nil || meta == nil || meta.kind != "slice" {
		return nil, false, nil
	}
	n, err := r.bashPPArrayLength(array.Length.Value)
	if err != nil {
		return nil, true, fmt.Errorf("BASHPP-EPOINTER-TYPE: %v", err)
	}
	seq, _ := value.([]any)
	if seq == nil {
		return nil, true, nil
	}
	if len(seq) < n {
		message := fmt.Sprintf("runtime error: cannot convert slice with length %d to array or pointer to array with length %d", len(seq), n)
		if r.bashPPGoSource {
			return nil, true, r.bashPPRaiseRuntimeError("runtime.errorString", message)
		}
		return nil, true, fmt.Errorf("BASHPP-ECOLLECTION-BOUNDS: %s", message)
	}
	ptr.elem = target.Element
	return ptr, true, nil
}

func (r *Runner) bashPPPointerExprValue(expr syntax.BashPPExpr) (ptr *bashPPPointer, err error) {
	defer func() { err = r.goSourceRuntimeFaultAt(err, expr) }()
	if _, nilConversion := r.bashPPNilPointerConversion(expr); nilConversion {
		return nil, nil
	}
	if ptr, _, converted, err := r.bashPPPointerConversion(expr); converted {
		return ptr, err
	}
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
		if x.Init != nil {
			initialized, initializedMeta, err := r.bashPPEvalTypedValue(x.Init, x.AllocType)
			if err != nil {
				return nil, err
			}
			value, meta = initialized, initializedMeta
		}
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
			return nil, errBashPPNilDereference
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

func (r *Runner) bashPPAddress(expr syntax.BashPPExpr) (result *bashPPPointer, err error) {
	defer func() { err = r.goSourceRuntimeFaultAt(err, expr) }()
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
	// Go's `&T{…}` is addressable even though it names no variable: the
	// literal is a fresh allocation whose address the expression yields. It
	// gets an anonymous cell to live in, exactly as `new(T)` does above.
	if lit, ok := expr.(*syntax.BashPPCompositeLit); ok {
		return r.bashPPCompositeAddress(lit)
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
	// Taking a typed variable's address needs its storage and declared type,
	// not its current value metadata. Atomic operations may be updating that
	// metadata concurrently before this address reaches the atomic lock.
	if _, direct := expr.(*syntax.BashPPIdent); direct && typ != nil {
		ptr.elem = typ
		return ptr, nil
	}
	meta := bashPPCellMeta(cell)
	if cell.pointer {
		if _, direct := expr.(*syntax.BashPPIdent); !direct {
			if cell.pointerValue == nil {
				return nil, errBashPPNilDereference
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
		case *syntax.BashPPParenExpr:
			return descend(x.X)
		case *syntax.BashPPDerefExpr:
			// The setup above already followed the root pointer once, so the
			// dereference naming that root contributes no further step -- this
			// is how `(*p)[i]` reaches the same address as `p[i]` would if Go
			// allowed that spelling. A deeper dereference would need a step
			// this walker does not build, so it is refused rather than
			// silently resolved to the wrong address.
			inner, isIdent := x.X.(*syntax.BashPPIdent)
			if isIdent && cell.pointer && inner.Name.Value == root {
				return nil
			}
			return fmt.Errorf("BASHPP-ENONADDRESSABLE: operand is not addressable")
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
					meta = bashPPLayoutGet(meta.mapping, edge.name)
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
			// A defined type such as `type bag []int` indexes exactly as its
			// underlying collection does, so the shape has to be read through
			// the definition rather than off the declared name.
			collection, found := r.bashPPUnderlyingType(typ).(*syntax.BashPPCollectionType)
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
			var value any
			if r.bashPPGoSource {
				// descend has already evaluated every inner index. Read the
				// saved path instead of executing those operands again.
				value, _, _, err = ptr.read()
			} else {
				value, _, err = r.bashPPReadExpr(x.X)
			}
			if err != nil {
				return err
			}
			seq := value.([]any)
			if i < 0 || i >= len(seq) {
				if r.bashPPGoSource {
					return r.bashPPSprint162CollectionBoundsPanic(x, i, len(seq))
				}
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
		return nil, nil, nil, errBashPPNilDereference
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
				return nil, nil, nil, errBashPPNilEmbeddedDereference
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
			value, found = bashPPStorageGet(mapping, step.field)
			if !found {
				return nil, nil, nil, fmt.Errorf("BASHPP-EPOINTER-TARGET: pointer field %q no longer exists", step.field)
			}
			if meta != nil {
				meta = bashPPLayoutGet(meta.mapping, step.field)
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

// bashPPSliceArrayPointerValue presents a slice-to-array pointer's backing
// storage as the converted array on a Go-source dereference.
func (r *Runner) bashPPSliceArrayPointerValue(ptr *bashPPPointer, value any, meta *bashPPCollectionMeta) (any, *bashPPCollectionMeta, error) {
	if !r.bashPPGoSource || ptr == nil || meta == nil || meta.kind != "slice" {
		return value, meta, nil
	}
	array, ok := r.bashPPUnderlyingType(ptr.elem).(*syntax.BashPPCollectionType)
	if !ok || array.Kind != "array" || array.Length == nil {
		return value, meta, nil
	}
	n, err := r.bashPPArrayLength(array.Length.Value)
	if err != nil {
		return nil, nil, err
	}
	sequence, ok := value.([]any)
	if !ok || len(sequence) < n {
		return nil, nil, fmt.Errorf("BASHPP-EPOINTER-TARGET: pointer array storage no longer names collection storage")
	}
	converted := *meta
	converted.kind, converted.typ = "array", ptr.elem
	converted.sequence = append([]*bashPPCollectionMeta(nil), meta.sequence[:n]...)
	return sequence[:n], &converted, nil
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
	if meta != nil && meta.kind == "channel" {
		// A channel read out of a collection has to arrive as the channel it
		// names, not as a rendering of one: an interpreter-owned channel lives
		// entirely in the identity beside the payload, while a dependency-owned
		// one is the handle payload itself. Either way the cell is what send,
		// receive, close and select resolve against.
		cell.pointer, cell.pointerValue, cell.nilPointer = false, nil, false
		cell.valueMeta, cell.channel, cell.channelOwner = meta, meta.channel, meta.channelOwner
		if native, ok := value.(*bashPPBridgeValue); ok && native != nil {
			cell.vr = expand.NewObject(native)
			return
		}
		text := ""
		if value != nil {
			text = fmt.Sprint(value)
		}
		cell.vr = expand.Variable{Set: true, Kind: expand.String, Str: text}
		return
	}
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
	if ptr, target, converted, err := r.bashPPPointerConversion(expr); converted {
		if err != nil {
			r.errf("%v\n", err)
			r.exit.code = 2
			return true
		}
		if ptr == nil {
			// A runtime nil operand — `Peano(q)` with q nil — binds as a
			// nil pointer of the conversion's type, like the retype below.
			expr, typ, meta = nil, target, bashPPPointerMeta(target)
		} else {
			expr, value, typ = nil, ptr, target
		}
	}
	switch x := expr.(type) {
	case nil:
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
			r.bashPPReportNilDereference()
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
		err = r.goSourceRuntimeFault(errBashPPNilDereference)
	}
	if err != nil {
		if !errors.Is(err, errBashPPScalarInterrupted) {
			r.errf("%v\n", err)
			r.exit.code = 2
		}
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
		var layout map[string]*bashPPCollectionMeta
		if parentMeta != nil {
			layout = parentMeta.mapping
		}
		bashPPStorageSetField(mapping, layout, last.field, value, meta)
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
