// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"fmt"
	"go/constant"
	"go/token"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// bashPPValueBuiltin reports the predeclared functions whose values or
// mutations are implemented over bashPPCell. panic/recover deliberately stay
// in bashpp_panic.go; close deliberately stays in bashpp_concurrency.go.
func bashPPValueBuiltin(name string) bool {
	switch name {
	case "append", "cap", "clear", "copy", "delete", "len", "make",
		"max", "min", "new", "print", "println":
		return true
	}
	return false
}

func (r *Runner) bashPPBuiltinError(kind, format string, args ...any) {
	r.errf("BASHPP-EBUILTIN-%s: "+format+"\n", append([]any{kind}, args...)...)
	r.exit = exitStatus{code: 2}
}

func (r *Runner) bashPPBuiltinArity(name, want string, got int) {
	r.bashPPBuiltinError("ARITY", "%s expects %s; got %d argument(s)", name, want, got)
}

type bashPPBuiltinArg struct {
	value     any
	meta      *bashPPCollectionMeta
	typ       syntax.BashPPTypeExpr
	cell      *bashPPCell
	channel   *bashPPChannel
	scalar    bashPPScalar
	hasScalar bool
	text      string
}

func (r *Runner) bashPPBuiltinArg(w *syntax.Word) bashPPBuiltinArg {
	text := bashPPWordSource(w)
	if text == "nil" {
		return bashPPBuiltinArg{text: text}
	}
	if cell := r.bashPPCellForWord(w); cell != nil {
		arg := bashPPBuiltinArg{cell: cell, typ: cell.declType, channel: cell.channel, text: text}
		if arg.typ == nil && cell.typeName != "" {
			arg.typ = &syntax.BashPPNamedType{Name: &syntax.Lit{Value: cell.typeName}}
		}
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
		return arg
	}
	if value, ok := r.bashPPResolveWord(w); ok {
		scalar := bashPPScalarFromString(value)
		return bashPPBuiltinArg{value: bashPPBuiltinExactScalarValue(value, scalar), scalar: scalar, hasScalar: true, text: text}
	}
	value := r.bashPPExprValue(w)
	if len(w.Parts) == 1 {
		switch w.Parts[0].(type) {
		case *syntax.SglQuoted, *syntax.DblQuoted:
			return bashPPBuiltinArg{value: value, scalar: bashPPScalar{value: constant.MakeString(value)}, hasScalar: true, text: text}
		}
	}
	scalar := bashPPScalarFromString(value)
	return bashPPBuiltinArg{value: bashPPBuiltinExactScalarValue(value, scalar), scalar: scalar, hasScalar: true, text: text}
}

func bashPPBuiltinExactScalarValue(text string, scalar bashPPScalar) any {
	switch scalar.value.Kind() {
	case constant.String:
		return constant.StringVal(scalar.value)
	case constant.Bool:
		return constant.BoolVal(scalar.value)
	case constant.Int:
		if value, ok := constant.Int64Val(scalar.value); ok {
			return int(value)
		} else {
			return text
		}
	case constant.Float:
		value, _ := constant.Float64Val(scalar.value)
		return value
	}
	return text
}

func bashPPBuiltinScalar(value any) string {
	switch value := value.(type) {
	case string:
		return value
	case nil:
		return "<nil>"
	default:
		return fmt.Sprint(value)
	}
}

func bashPPBuiltinScalarCell(value string) *bashPPCell {
	return &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: value}, scalarKind: bashPPScalarFromString(value).value.Kind()}
}

func (r *Runner) bashPPBuiltinMutable(name string, arg bashPPBuiltinArg) bool {
	if arg.cell == nil {
		r.bashPPBuiltinError("TYPE", "%s first argument must be an addressable collection", name)
		return false
	}
	if arg.cell.constant || arg.cell.vr.ReadOnly || arg.cell.object != nil && arg.cell.object.readonly {
		owner := arg.text
		if arg.cell.object != nil && arg.cell.object.owner != "" {
			owner = arg.cell.object.owner
		}
		r.errf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value %q through %s\n", owner, name)
		r.exit = exitStatus{code: 2}
		return false
	}
	return true
}

func (r *Runner) bashPPBuiltinCollection(arg bashPPBuiltinArg, kinds ...string) (*syntax.BashPPCollectionType, bool) {
	if arg.meta == nil {
		return nil, false
	}
	shape, ok := r.bashPPUnderlyingType(arg.meta.typ).(*syntax.BashPPCollectionType)
	if !ok {
		return nil, false
	}
	for _, kind := range kinds {
		if arg.meta.kind == kind {
			return shape, true
		}
	}
	return nil, false
}

func (r *Runner) bashPPBuiltinPointerArrayLen(arg bashPPBuiltinArg) (int, bool) {
	ptr, ok := r.bashPPPointerType(arg.typ)
	if !ok {
		return 0, false
	}
	array, ok := r.bashPPUnderlyingType(ptr.Element).(*syntax.BashPPCollectionType)
	if !ok || array.Kind != "array" || array.Length == nil {
		return 0, false
	}
	n, err := r.bashPPArrayLength(array.Length.Value)
	return n, err == nil
}

func (r *Runner) bashPPBuiltinByteSlice(shape *syntax.BashPPCollectionType) bool {
	typ := r.bashPPCanonicalAssignableType(shape.Element)
	named, ok := typ.(*syntax.BashPPNamedType)
	return ok && (named.Name.Value == "byte" || named.Name.Value == "uint8")
}

func (r *Runner) bashPPBuiltinInt(name string, arg bashPPBuiltinArg) (int, bool) {
	var text string
	switch value := arg.value.(type) {
	case int:
		return value, true
	case string:
		text = value
	default:
		text = fmt.Sprint(value)
	}
	n, err := strconv.Atoi(text)
	if err != nil {
		r.bashPPBuiltinError("TYPE", "%s size must be an integer; got %s", name, arg.text)
		return 0, false
	}
	return n, true
}

func (r *Runner) bashPPBuiltinElement(arg bashPPBuiltinArg, expected syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta, bool) {
	if arg.meta != nil {
		if err := r.bashPPCheckTypedValue(arg.value, arg.meta, expected); err != nil {
			r.bashPPBuiltinError("TYPE", "%v", err)
			return nil, nil, false
		}
		value, meta := bashPPCopyArrayValue(arg.value, arg.meta)
		return value, meta, true
	}
	if err := r.bashPPCheckCollectionValue(arg.value, expected); err != nil {
		r.bashPPBuiltinError("TYPE", "%v", err)
		return nil, nil, false
	}
	return arg.value, nil, true
}

// bashPPRunValueBuiltin executes a non-panic predeclared call. Its result is a
// full cell so structured identity and named type metadata survive `:=`.
func (r *Runner) bashPPRunValueBuiltin(name string, c *syntax.BashPPCall) (*bashPPCell, bool) {
	args := make([]bashPPBuiltinArg, len(c.Args))
	for i, word := range c.Args {
		args[i] = r.bashPPBuiltinArg(word)
	}
	switch name {
	case "len", "cap":
		if len(args) != 1 || c.Ellipsis.IsValid() {
			r.bashPPBuiltinArity(name, "exactly 1 argument", len(args))
			return nil, false
		}
		if args[0].channel != nil {
			if r.bashPPConcurrent == nil || args[0].cell.channelOwner != r.bashPPConcurrent || r.bashPPChanBoundary {
				r.bashPPBuiltinError("TYPE", "%s argument is not a channel in this task group", name)
				return nil, false
			}
			n := len(args[0].channel.ch)
			if name == "cap" {
				n = cap(args[0].channel.ch)
			}
			return bashPPBuiltinScalarCell(strconv.Itoa(n)), true
		}
		if n, ok := r.bashPPBuiltinPointerArrayLen(args[0]); ok {
			return bashPPBuiltinScalarCell(strconv.Itoa(n)), true
		}
		if name == "len" {
			if text, ok := args[0].value.(string); ok && args[0].meta == nil {
				return bashPPBuiltinScalarCell(strconv.Itoa(len(text))), true
			}
		}
		if args[0].value == nil && args[0].meta == nil {
			r.bashPPBuiltinError("NIL", "%s cannot be applied to untyped nil", name)
			return nil, false
		}
		kinds := []string{"array", "inferred-array", "slice", "map"}
		if name == "cap" {
			kinds = kinds[:3]
		}
		if _, ok := r.bashPPBuiltinCollection(args[0], kinds...); !ok {
			r.bashPPBuiltinError("TYPE", "%s argument must be %s", name, map[bool]string{true: "an array or slice", false: "a string, array, slice, or map"}[name == "cap"])
			return nil, false
		}
		if args[0].meta.kind == "map" {
			return bashPPBuiltinScalarCell(strconv.Itoa(len(args[0].value.(map[string]any)))), true
		}
		seq, _ := args[0].value.([]any)
		n := len(seq)
		if name == "cap" {
			n = cap(seq)
		}
		return bashPPBuiltinScalarCell(strconv.Itoa(n)), true

	case "append":
		if len(args) < 1 {
			r.bashPPBuiltinArity(name, "at least 1 argument", len(args))
			return nil, false
		}
		shape, ok := r.bashPPBuiltinCollection(args[0], "slice")
		if !ok {
			if args[0].value == nil {
				r.bashPPBuiltinError("NIL", "append first argument is untyped nil")
				return nil, false
			}
			r.bashPPBuiltinError("TYPE", "append first argument must be a slice")
			return nil, false
		}
		seq, _ := args[0].value.([]any)
		metas := args[0].meta.sequence
		oldCap := cap(seq)
		additional := len(args) - 1
		stringSpread := false
		if c.Ellipsis.IsValid() && len(args) == 2 {
			if text, ok := args[1].value.(string); ok && args[1].meta == nil && args[1].channel == nil && r.bashPPBuiltinByteSlice(shape) {
				additional, stringSpread = len([]byte(text)), true
			} else if spread, ok := args[1].value.([]any); ok {
				additional = len(spread)
			}
		}
		if additional > 0 && len(seq)+additional <= oldCap && !r.bashPPBuiltinMutable(name, args[0]) {
			return nil, false
		}
		if c.Ellipsis.IsValid() {
			if len(args) != 2 {
				r.bashPPBuiltinArity(name, "a slice and one spread slice", len(args))
				return nil, false
			}
			if stringSpread {
				for _, b := range []byte(args[1].value.(string)) {
					seq, metas = append(seq, int(b)), append(metas, nil)
				}
			} else {
				otherShape, ok := r.bashPPBuiltinCollection(args[1], "slice")
				if !ok || !r.bashPPTypeAssignable(otherShape.Element, shape.Element) {
					r.bashPPBuiltinError("TYPE", "append spread argument must be a compatible slice or string for []byte")
					return nil, false
				}
				other, _ := args[1].value.([]any)
				seq = append(seq, other...)
				metas = append(metas, args[1].meta.sequence...)
			}
		} else {
			for _, arg := range args[1:] {
				value, meta, ok := r.bashPPBuiltinElement(arg, shape.Element)
				if !ok {
					return nil, false
				}
				seq, metas = append(seq, value), append(metas, meta)
			}
		}
		meta := &bashPPCollectionMeta{kind: "slice", typ: args[0].meta.typ, sequence: metas}
		identity := args[0].cell.object
		if len(seq) > oldCap {
			identity = &bashPPObjectIdentity{collection: meta}
		}
		return &bashPPCell{vr: expand.NewObject(seq), object: identity, valueMeta: meta, declType: args[0].meta.typ}, true

	case "copy":
		if len(args) != 2 || c.Ellipsis.IsValid() {
			r.bashPPBuiltinArity(name, "exactly 2 arguments", len(args))
			return nil, false
		}
		dstType, dstOK := r.bashPPBuiltinCollection(args[0], "slice")
		srcType, srcOK := r.bashPPBuiltinCollection(args[1], "slice")
		text, stringSource := args[1].value.(string)
		stringSource = stringSource && args[1].meta == nil && args[1].channel == nil && dstOK && r.bashPPBuiltinByteSlice(dstType)
		if !dstOK || !stringSource && (!srcOK || !r.bashPPTypeAssignable(srcType.Element, dstType.Element)) {
			r.bashPPBuiltinError("TYPE", "copy arguments must be compatible slices")
			return nil, false
		}
		if !r.bashPPBuiltinMutable(name, args[0]) {
			return nil, false
		}
		dst, _ := args[0].value.([]any)
		var n int
		if stringSource {
			bytes := []byte(text)
			n = min(len(dst), len(bytes))
			for i := range n {
				dst[i], args[0].meta.sequence[i] = int(bytes[i]), nil
			}
		} else {
			src, _ := args[1].value.([]any)
			n = copy(dst, src)
			copy(args[0].meta.sequence[:n], args[1].meta.sequence[:n])
		}
		return bashPPBuiltinScalarCell(strconv.Itoa(n)), true

	case "delete":
		if len(args) != 2 || c.Ellipsis.IsValid() {
			r.bashPPBuiltinArity(name, "exactly 2 arguments", len(args))
			return nil, false
		}
		shape, ok := r.bashPPBuiltinCollection(args[0], "map")
		if !ok {
			r.bashPPBuiltinError("TYPE", "delete first argument must be a map")
			return nil, false
		}
		if !r.bashPPBuiltinMutable(name, args[0]) {
			return nil, false
		}
		if err := r.bashPPCheckCollectionValue(args[1].value, shape.Key); err != nil {
			r.bashPPBuiltinError("TYPE", "delete key: %v", err)
			return nil, false
		}
		if mapping, ok := args[0].value.(map[string]any); ok {
			key := fmt.Sprint(args[1].value)
			delete(mapping, key)
			delete(args[0].meta.mapping, key)
		}
		return nil, false

	case "clear":
		if len(args) != 1 || c.Ellipsis.IsValid() {
			r.bashPPBuiltinArity(name, "exactly 1 argument", len(args))
			return nil, false
		}
		shape, ok := r.bashPPBuiltinCollection(args[0], "slice", "map")
		if !ok {
			r.bashPPBuiltinError("TYPE", "clear argument must be a slice or map")
			return nil, false
		}
		if !r.bashPPBuiltinMutable(name, args[0]) {
			return nil, false
		}
		if args[0].meta.kind == "map" {
			if mapping, ok := args[0].value.(map[string]any); ok {
				clear(mapping)
				clear(args[0].meta.mapping)
			}
		} else if seq, ok := args[0].value.([]any); ok {
			for i := range seq {
				seq[i], args[0].meta.sequence[i] = r.bashPPZeroValue(shape.Element)
			}
		}
		return nil, false

	case "make":
		if len(args) < 1 || len(args) > 3 || c.ArgType == nil || c.Ellipsis.IsValid() {
			r.bashPPBuiltinArity(name, "a slice/map type and valid size arguments", len(args))
			return nil, false
		}
		typ := c.ArgType
		shape, ok := r.bashPPUnderlyingType(typ).(*syntax.BashPPCollectionType)
		if !ok || shape.Kind != "slice" && shape.Kind != "map" {
			r.bashPPBuiltinError("TYPE", "make type must be a slice or map")
			return nil, false
		}
		if shape.Kind == "map" {
			if len(args) > 2 {
				r.bashPPBuiltinArity(name, "a map type and optional size", len(args))
				return nil, false
			}
			if len(args) == 2 {
				n, ok := r.bashPPBuiltinInt(name, args[1])
				if !ok || n < 0 {
					if ok {
						r.bashPPBuiltinError("SIZE", "make map size must not be negative")
					}
					return nil, false
				}
			}
			value := make(map[string]any)
			meta := &bashPPCollectionMeta{kind: "map", typ: typ, mapping: make(map[string]*bashPPCollectionMeta)}
			return &bashPPCell{vr: expand.NewObject(value), object: &bashPPObjectIdentity{collection: meta}, valueMeta: meta, declType: typ}, true
		}
		if len(args) < 2 {
			r.bashPPBuiltinArity(name, "a slice type and length, with optional capacity", len(args))
			return nil, false
		}
		length, ok := r.bashPPBuiltinInt(name, args[1])
		if !ok {
			return nil, false
		}
		capacity := length
		if len(args) == 3 {
			capacity, ok = r.bashPPBuiltinInt(name, args[2])
			if !ok {
				return nil, false
			}
		}
		if length < 0 || capacity < length {
			r.bashPPBuiltinError("SIZE", "make slice length/capacity is invalid: %d/%d", length, capacity)
			return nil, false
		}
		value := make([]any, length, capacity)
		children := make([]*bashPPCollectionMeta, length, capacity)
		for i := range value {
			value[i], children[i] = r.bashPPZeroValue(shape.Element)
		}
		meta := &bashPPCollectionMeta{kind: "slice", typ: typ, sequence: children}
		return &bashPPCell{vr: expand.NewObject(value), object: &bashPPObjectIdentity{collection: meta}, valueMeta: meta, declType: typ}, true

	case "min", "max":
		if len(args) == 0 || c.Ellipsis.IsValid() {
			r.bashPPBuiltinArity(name, "at least 1 non-spread argument", len(args))
			return nil, false
		}
		best := 0
		var resultType syntax.BashPPTypeExpr
		kind := constant.Unknown
		for _, arg := range args {
			if !arg.hasScalar || arg.scalar.value == nil {
				r.bashPPBuiltinError("TYPE", "%s arguments must all be ordered scalar values", name)
				return nil, false
			}
			argKind := arg.scalar.value.Kind()
			if argKind != constant.Int && argKind != constant.Float && argKind != constant.String {
				r.bashPPBuiltinError("TYPE", "%s arguments must all be ordered values", name)
				return nil, false
			}
			if kind == constant.Unknown {
				kind = argKind
			} else if kind == constant.String || argKind == constant.String {
				if kind != argKind {
					r.bashPPBuiltinError("TYPE", "%s arguments must all be ordered values of one kind", name)
					return nil, false
				}
			} else if argKind == constant.Float {
				kind = constant.Float
			}
			if arg.typ != nil {
				if resultType == nil {
					resultType = arg.typ
				} else if !r.bashPPTypeAssignable(arg.typ, resultType) || !r.bashPPTypeAssignable(resultType, arg.typ) {
					r.bashPPBuiltinError("TYPE", "%s typed arguments must have one type", name)
					return nil, false
				}
			}
		}
		if resultType != nil {
			named, ok := r.bashPPUnderlyingType(resultType).(*syntax.BashPPNamedType)
			if !ok {
				r.bashPPBuiltinError("TYPE", "%s result type must be an ordered scalar type", name)
				return nil, false
			}
			for _, arg := range args {
				if arg.typ == nil {
					if _, err := r.bashPPConvertScalar(named.Name.Value, arg.scalar); err != nil {
						r.bashPPBuiltinError("TYPE", "%v", err)
						return nil, false
					}
				}
			}
		}
		for i := 1; i < len(args); i++ {
			op := token.LSS
			if name == "max" {
				op = token.GTR
			}
			if constant.Compare(args[i].scalar.value, op, args[best].scalar.value) {
				best = i
			}
		}
		result := bashPPBuiltinScalarCell(bashPPScalarString(args[best].scalar.value))
		result.scalarKind = args[best].scalar.value.Kind()
		result.declType = resultType
		if named, ok := resultType.(*syntax.BashPPNamedType); ok {
			result.typeName = named.Name.Value
		}
		return result, true

	case "print", "println":
		if c.Ellipsis.IsValid() {
			r.bashPPBuiltinError("ARITY", "%s does not accept a spread argument", name)
			return nil, false
		}
		parts := make([]string, len(args))
		for i, arg := range args {
			parts[i] = bashPPBuiltinScalar(arg.value)
		}
		if name == "println" {
			r.outf("%s\n", strings.Join(parts, " "))
		} else {
			r.outf("%s", strings.Join(parts, ""))
		}
		return nil, false

	case "new":
		r.bashPPBuiltinError("TYPE", "new requires exactly one type argument and is only a value expression")
		return nil, false
	}
	return nil, false
}

func (r *Runner) bashPPBindBuiltinResult(d *syntax.BashPPShortDecl, result *bashPPCell, produced bool) {
	if !produced {
		if r.exit.code == 0 {
			r.bashPPBuiltinError("ARITY", "%s produces no value", d.Call.Fun[0].Value)
		}
		return
	}
	if len(d.Lhs) != 1 {
		r.errf("assignment mismatch: %d variable(s) but 1 value(s)\n", len(d.Lhs))
		r.exit = exitStatus{code: 2}
		return
	}
	name := d.Lhs[0].Value
	r.bashPPDeclareName(name, result.vr)
	if r.exit.code != 0 {
		return
	}
	cell := r.bashPPScope.lookup(name)
	*cell = *result
	if cell.object == nil && cell.vr.Kind == expand.Object {
		cell.object = &bashPPObjectIdentity{owner: name, collection: cell.valueMeta}
	} else if cell.object != nil && cell.object.owner == "" {
		cell.object.owner = name
	}
}
