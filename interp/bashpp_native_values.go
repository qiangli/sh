package interp

// Sprint: #118; Story: #50; Story-ID: cf81e4868348
import (
	"context"
	"errors"
	"fmt"
	"go/constant"
	"go/token"
	"reflect"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func (r *Runner) bashPPBridgeHandles(call *syntax.BashPPCall) bool {
	if !r.bashPPGoSource || call == nil {
		return false
	}
	if call.CalleeExpr != nil {
		selector, ok := call.CalleeExpr.(*syntax.BashPPSelectorExpr)
		return ok && r.bashPPNativeExpr(selector.X)
	}
	if len(call.Fun) < 1 {
		return false
	}
	if len(call.Fun) >= 2 {
		if _, ok := r.bashPPImports[call.Fun[0].Value]; ok {
			return true
		}
		if r.bashPPNativeCellValue(call.Fun[0].Value) != nil {
			return true
		}
	}
	if len(call.Fun) == 1 {
		if value := r.bashPPNativeCellValue(call.Fun[0].Value); value != nil && value.Kind == "handle" && strings.HasPrefix(value.Type, "func(") {
			return true
		}
		for alias := range r.bashPPImports {
			if strings.HasPrefix(alias, ".:") {
				if _, ok := r.bashPPLookupFunc(call); !ok {
					return true
				}
			}
		}
	}
	return false
}
func (r *Runner) bashPPBridgeCall(ctx context.Context, call *syntax.BashPPCall) ([]bashPPBridgeValue, error) {
	q, err := r.bashPPPrepareNativeCall(ctx, call)
	if err != nil {
		return nil, err
	}
	req, err := r.bashPPEvalRequest()
	if err != nil {
		return nil, err
	}
	return r.bashPPNativeRequest(ctx, req, q)
}
func (r *Runner) bashPPPrepareNativeCall(ctx context.Context, call *syntax.BashPPCall) (bashPPBridgeRequest, error) {
	if !r.bashPPBridgeHandles(call) {
		return bashPPBridgeRequest{}, fmt.Errorf("gosource: call is not an imported dependency operation")
	}
	q := bashPPBridgeRequest{Op: "call", Spread: call.Ellipsis.IsValid()}
	if selector, ok := call.CalleeExpr.(*syntax.BashPPSelectorExpr); ok {
		receiver, err := r.bashPPNativeReceiver(selector.X)
		if err != nil {
			return bashPPBridgeRequest{}, err
		}
		q.Receiver = &receiver
		q.Selector = selector.Sel.Value
	} else if len(call.Fun) >= 2 {
		if _, ok := r.bashPPImports[call.Fun[0].Value]; ok && len(call.Fun) == 2 {
			q.Selector = call.Fun[0].Value + "." + call.Fun[1].Value
		} else {
			var receiverExpr syntax.BashPPExpr = &syntax.BashPPIdent{Name: call.Fun[0]}
			for _, part := range call.Fun[1 : len(call.Fun)-1] {
				receiverExpr = &syntax.BashPPSelectorExpr{X: receiverExpr, Sel: part}
			}
			receiver, err := r.bashPPNativeReceiver(receiverExpr)
			if err != nil {
				return bashPPBridgeRequest{}, err
			}
			q.Receiver = &receiver
			q.Selector = call.Fun[len(call.Fun)-1].Value
		}
	} else if value := r.bashPPNativeCellValue(call.Fun[0].Value); value != nil && value.Kind == "handle" && strings.HasPrefix(value.Type, "func(") {
		copy := *value
		q.Receiver = &copy
	} else {
		q.Selector = call.Fun[0].Value
	}
	if len(call.ArgExprs) != len(call.Args) {
		return bashPPBridgeRequest{}, fmt.Errorf("gosource: missing evaluated dependency arguments")
	}
	if len(call.ArgExprs) == 1 {
		if inner, ok := call.ArgExprs[0].(*syntax.BashPPCall); ok {
			if r.bashPPBridgeHandles(inner) {
				values, err := r.bashPPBridgeCall(ctx, inner)
				if err != nil {
					return bashPPBridgeRequest{}, err
				}
				q.Args = append(q.Args, values...)
				return q, nil
			}
			if fn, ok := r.bashPPLookupFunc(inner); ok {
				cells, err := r.goSourceCallResultCells(inner, fn)
				if err != nil {
					return bashPPBridgeRequest{}, err
				}
				for _, cell := range cells {
					value, err := r.bashPPBridgeCell(cell)
					if err != nil {
						return bashPPBridgeRequest{}, err
					}
					q.Args = append(q.Args, value)
				}
				return q, nil
			}
		}
	}
	for _, expr := range call.ArgExprs {
		value, err := r.bashPPBridgeExpr(expr)
		if err != nil {
			var positioned *goSourceError
			if errors.As(err, &positioned) {
				return bashPPBridgeRequest{}, err
			}
			return bashPPBridgeRequest{}, &goSourceError{prefix: r.bashErrPrefix(expr.Pos()), err: err}
		}
		q.Args = append(q.Args, value)
	}
	return q, nil
}
func (r *Runner) bashPPBridgeScalar(expr syntax.BashPPExpr) (bashPPScalar, bool, error) {
	if !r.bashPPGoSource {
		return bashPPScalar{}, false, nil
	}
	handled := false
	switch e := expr.(type) {
	case *syntax.BashPPCall:
		handled = r.bashPPBridgeHandles(e)
	case *syntax.BashPPSelectorExpr:
		handled = r.bashPPNativeExpr(e)
	case *syntax.BashPPIndexExpr:
		handled = r.bashPPNativeExpr(e.X)
	case *syntax.BashPPSliceExpr:
		handled = r.bashPPNativeExpr(e.X)
	}
	if !handled {
		return bashPPScalar{}, false, nil
	}
	value, err := r.bashPPBridgeExpr(expr)
	if err != nil {
		return bashPPScalar{}, true, err
	}
	scalar, err := value.scalar()
	return scalar, true, err
}
func (value bashPPBridgeValue) scalar() (bashPPScalar, error) {
	scalar := bashPPScalar{typ: value.Type, runtime: true}
	switch value.Kind {
	case "string":
		scalar.value = constant.MakeString(value.Text)
	case "bool":
		scalar.value = constant.MakeBool(value.Text == "true")
	case "int", "uint":
		scalar.value = constant.MakeFromLiteral(value.Text, token.INT, 0)
	case "complex":
		scalar.value = bashPPParseComplex(value.Text)
	case "float":
		scalar.value = constant.MakeFromLiteral(value.Text, token.FLOAT, 0)
	default:
		return scalar, fmt.Errorf("gosource: native %s (%s) is not scalar", value.Kind, value.Type)
	}
	if scalar.value == nil || scalar.value.Kind() == constant.Unknown {
		return scalar, fmt.Errorf("gosource: invalid native scalar")
	}
	return scalar, nil
}
func bridgeScalar(value bashPPScalar) (bashPPBridgeValue, error) {
	out := bashPPBridgeValue{Type: value.typ}
	if value.value == nil {
		return out, fmt.Errorf("gosource: absent scalar value")
	}
	switch value.value.Kind() {
	case constant.String:
		out.Kind = "string"
		out.Text = constant.StringVal(value.value)
	case constant.Bool:
		out.Kind = "bool"
		out.Text = strconv.FormatBool(constant.BoolVal(value.value))
	case constant.Int:
		out.Kind = "int"
		out.Text = value.value.ExactString()
		if strings.HasPrefix(value.typ, "uint") || value.typ == "byte" {
			out.Kind = "uint"
		}
	case constant.Complex:
		out.Kind = "complex"
		out.Text = strconv.FormatComplex(bashPPComplexNumber(value.value), 'g', -1, 128)
	case constant.Float:
		out.Kind = "float"
		number, _ := constant.Float64Val(value.value)
		out.Text = strconv.FormatFloat(number, 'g', -1, 64)
	default:
		return out, fmt.Errorf("gosource: unsupported native scalar kind %s", value.value.Kind())
	}
	return out, nil
}
func (r *Runner) bashPPBridgeExpr(expr syntax.BashPPExpr) (bashPPBridgeValue, error) {
	switch expr.(type) {
	case *syntax.BashPPFuncLit, *syntax.BashPPIdent:
		if cell, handled, err := r.goSourceCallableCell(expr); handled {
			if err != nil {
				return bashPPBridgeValue{}, err
			}
			if fn, ok := r.bashPPClosure(cell.vr.Str); ok {
				return r.bashPPBridgeFunction(fn)
			}
		}
	}
	switch x := expr.(type) {
	case *syntax.BashPPParenExpr:
		return r.bashPPBridgeExpr(x.X)
	case *syntax.BashPPIdent:
		if x.Name.Value == "nil" {
			return bashPPBridgeValue{Kind: "nil"}, nil
		}
		if r.bashPPScope != nil {
			if cell := r.bashPPScope.lookup(x.Name.Value); cell != nil {
				switch {
				case cell.pointer || cell.interfaceValue != nil:
					return r.bashPPBridgeCell(cell)
				case cell.vr.Kind == expand.Object:
					if value, ok := cell.vr.Obj.(*bashPPBridgeValue); ok {
						return *value, nil
					}
					return r.bashPPBridgeCollection(cell.vr.Obj, cell.valueMeta, cell.declType)
				}
			}
		}
	case *syntax.BashPPCall:
		if r.bashPPGoSource && len(x.Fun) == 1 && x.Fun[0].Value == "recover" && len(x.Args) == 0 && r.bashPPFuncs["recover"] == nil && (r.bashPPScope == nil || r.bashPPScope.lookup("recover") == nil) {
			value, recovered := r.bashPPRecover()
			if !recovered {
				return bashPPBridgeValue{Kind: "nil"}, nil
			}
			return bashPPBridgeValue{Kind: "string", Type: "string", Text: value}, nil
		}
		if r.bashPPBridgeHandles(x) {
			values, err := r.bashPPBridgeCall(r.ectx, x)
			if err != nil {
				return bashPPBridgeValue{}, err
			}
			if r.exit.exiting {
				return bashPPBridgeValue{}, errBashPPNativeExited
			}
			if len(values) != 1 {
				return bashPPBridgeValue{}, fmt.Errorf("gosource: expression requires one native result, got %d", len(values))
			}
			return values[0], nil
		}
	case *syntax.BashPPSelectorExpr:
		if r.bashPPNativeExpr(x.X) {
			base, err := r.bashPPNativeReceiver(x.X)
			if err != nil {
				return bashPPBridgeValue{}, err
			}
			value, err := r.bashPPNativeAccess(r.ectx, "member", base, x.Sel.Value)
			if err == nil && value.Kind == "handle" && strings.HasPrefix(value.Type, "func(") && base.NativeType != "" {
				value.Callable = base.NativeType + "." + x.Sel.Value
			}
			return value, err
		}
		if id, ok := x.X.(*syntax.BashPPIdent); ok {
			if _, imported := r.bashPPImports[id.Name.Value]; imported {
				req, err := r.bashPPEvalRequest()
				if err != nil {
					return bashPPBridgeValue{}, err
				}
				values, err := r.bashPPNativeRequest(r.ectx, req, bashPPBridgeRequest{Op: "get", Selector: id.Name.Value + "." + x.Sel.Value})
				if err != nil {
					return bashPPBridgeValue{}, err
				}
				if len(values) != 1 {
					return bashPPBridgeValue{}, fmt.Errorf("gosource: native symbol returned no value")
				}
				value := values[0]
				if value.Kind == "handle" && strings.HasPrefix(value.Type, "func(") {
					value.Callable = r.bashPPImports[id.Name.Value] + "." + x.Sel.Value
				}
				return value, nil
			}
		}
	case *syntax.BashPPIndexExpr:
		if r.bashPPNativeExpr(x.X) {
			return r.bashPPNativeIndex(x)
		}
	case *syntax.BashPPSliceExpr:
		if r.bashPPNativeExpr(x.X) {
			return r.bashPPNativeSlice(x)
		}
	case *syntax.BashPPDerefExpr:
		ptr, err := r.bashPPPointerExprValue(x.X)
		if err != nil {
			return bashPPBridgeValue{}, err
		}
		if ptr == nil {
			return bashPPBridgeValue{}, fmt.Errorf("gosource: invalid memory address or nil pointer dereference")
		}
		value, meta, typ, err := ptr.read()
		if err != nil {
			return bashPPBridgeValue{}, err
		}
		return r.bashPPBridgeCollection(value, meta, typ)
	case *syntax.BashPPAddressExpr:
		if lit, ok := x.X.(*syntax.BashPPCompositeLit); ok && r.bashPPNativeType(lit.LitType) {
			return r.bashPPNativeComposite(lit, true)
		}
		ptr, err := r.bashPPPointerExprValue(x)
		if err != nil {
			return bashPPBridgeValue{}, err
		}
		return r.bashPPBridgePointerValue(ptr)
	case *syntax.BashPPCompositeLit:
		if r.bashPPNativeType(x.LitType) {
			return r.bashPPNativeComposite(x, false)
		}
		value, meta, err := r.bashPPEvalComposite(x, x.LitType)
		if err != nil {
			return bashPPBridgeValue{}, err
		}
		return r.bashPPBridgeCollection(value, meta, x.LitType)
	}
	// A predeclared value call — append(xs, 1), copy(dst, src), make(...) — is
	// implemented over cells rather than as a callable, so the scalar evaluator
	// cannot look it up; see bashPPValueBuiltinBridge in
	// bashpp_collection_bridge.go. An unclaimed name keeps the scalar path.
	if bridged, claimed, err := r.bashPPValueBuiltinBridge(expr); claimed {
		return bridged, err
	}
	// An interpreter-owned structured read — board[i], xs[1:], v.Inner — has no
	// scalar spelling and crosses as the collection it is; see
	// bashPPStructuredBridgeRead in bashpp_collection_growth.go. Scalar reads
	// report false and keep the scalar evaluator's own diagnostics below.
	if value, meta, ok := r.bashPPStructuredBridgeRead(expr); ok {
		return r.bashPPBridgeCollection(value, meta, meta.typ)
	}
	scalar, err := r.bashPPEvalScalarExpr(expr)
	if err != nil {
		return bashPPBridgeValue{}, err
	}
	value, err := bridgeScalar(scalar)
	if err != nil {
		return value, err
	}
	return r.bashPPBridgeDefinedScalar(value)
}

// bashPPBridgeFloatText normalises one shell-held float, including the exact
// rational spelling the interpreter uses for a non-representable constant.
func bashPPBridgeFloatText(text string) (string, bool) {
	number := constant.MakeFromLiteral(text, token.FLOAT, 0)
	if number.Kind() == constant.Unknown {
		numerator, denominator, ok := strings.Cut(text, "/")
		if !ok {
			return "", false
		}
		top := constant.MakeFromLiteral(numerator, token.FLOAT, 0)
		bottom := constant.MakeFromLiteral(denominator, token.FLOAT, 0)
		if top.Kind() == constant.Unknown || bottom.Kind() == constant.Unknown || constant.Sign(bottom) == 0 {
			return "", false
		}
		number = constant.BinaryOp(top, token.QUO, bottom)
	}
	value, _ := constant.Float64Val(number)
	return strconv.FormatFloat(value, 'g', -1, 64), true
}

// bashPPBridgeDefinedScalar re-reads a scalar the shell carries as text at the
// underlying kind of the original defined type that names it, so a value of
// `type Celsius float64` crosses the boundary as a float and keeps the
// materialised Celsius identity rather than arriving as a string.
func (r *Runner) bashPPBridgeDefinedScalar(value bashPPBridgeValue) (bashPPBridgeValue, error) {
	if value.Kind != "string" || value.Type == "" {
		return value, nil
	}
	if _, local := r.bashPPTypes[value.Type]; !local {
		return value, nil
	}
	named := &syntax.BashPPNamedType{Name: &syntax.Lit{Value: value.Type}}
	underlying := bashPPTypeText(r.bashPPUnderlyingType(named))
	switch {
	case underlying == "string":
		return value, nil
	case underlying == "bool":
		if value.Text != "true" && value.Text != "false" {
			return value, fmt.Errorf("gosource: %s value %q is not a bool", value.Type, value.Text)
		}
		value.Kind = "bool"
	case underlying == "float32" || underlying == "float64":
		// The shell may hold an exact non-integer constant in its rational
		// form, which is the interpreter's own spelling and not a Go literal.
		number, ok := bashPPBridgeFloatText(value.Text)
		if !ok {
			return value, fmt.Errorf("gosource: %s value %q is not a %s", value.Type, value.Text, underlying)
		}
		value.Kind, value.Text = "float", number
	case bashPPIntegerType(underlying):
		if _, err := strconv.ParseInt(value.Text, 10, 64); err != nil {
			return value, fmt.Errorf("gosource: %s value %q is not an %s", value.Type, value.Text, underlying)
		}
		value.Kind = "int"
		if strings.HasPrefix(underlying, "uint") || underlying == "byte" {
			value.Kind = "uint"
		}
	}
	return value, nil
}

// bashPPBridgePointerValue transports a pointer to an original value as the
// pointee it addresses, so the dependency observes a real *T — Go's &{1 2}
// rather than a struct. The identity resolves to the original interpreter
// storage for method callbacks. General native out-parameters fail before the
// dependency executes because native writes have no complete alias contract.
func (r *Runner) bashPPBridgePointerValue(ptr *bashPPPointer) (bashPPBridgeValue, error) {
	if ptr == nil {
		return bashPPBridgeValue{Kind: "nil"}, nil
	}
	value, meta, typ, err := ptr.read()
	if err != nil {
		return bashPPBridgeValue{}, err
	}
	inner, err := r.bashPPBridgeCollection(value, meta, typ)
	if err != nil {
		return bashPPBridgeValue{}, err
	}
	req, err := r.bashPPEvalRequest()
	if err != nil {
		return bashPPBridgeValue{}, err
	}
	session := req.Bridge
	session.mu.Lock()
	if session.origins == nil {
		session.origins = map[uint64]*bashPPPointer{}
	}
	var origin uint64
	for id, existing := range session.origins {
		if existing.target == ptr.target && reflect.DeepEqual(existing.path, ptr.path) {
			origin = id
			break
		}
	}
	if origin == 0 {
		session.originNext++
		origin = session.originNext
		session.origins[origin] = ptr
	}
	session.mu.Unlock()
	return bashPPBridgeValue{Origin: origin, Session: session.id, Kind: "pointer", Type: "*" + inner.Type, Elements: []bashPPBridgeValue{inner}}, nil
}

func (r *Runner) bashPPBridgeCollection(value any, meta *bashPPCollectionMeta, typ syntax.BashPPTypeExpr) (bashPPBridgeValue, error) {
	if meta != nil && meta.interfaceValue != nil {
		cell := meta.interfaceValue.cell
		if meta.interfaceValue.nilIface || cell == nil {
			return bashPPBridgeValue{Kind: "nil"}, nil
		}
		if cell.pointer {
			return r.bashPPBridgePointerValue(cell.pointerValue)
		}
		if cell.vr.Kind == expand.Object {
			return r.bashPPBridgeCollection(cell.vr.Obj, cell.valueMeta, cell.declType)
		}
		return bridgeScalar(r.bashPPScalarFromCell(cell))
	}
	if meta != nil && meta.typ != nil {
		typ = meta.typ
	}
	result := bashPPBridgeValue{Type: bashPPBridgeTypeText(typ)}
	switch value := value.(type) {
	case []any:
		result.Kind = "slice"
		if meta != nil {
			result.Kind = meta.kind
			if result.Kind == "inferred-array" {
				result.Kind = "array"
			}
		}
		collection, ok := r.bashPPUnderlyingType(typ).(*syntax.BashPPCollectionType)
		if !ok {
			return result, fmt.Errorf("gosource: missing collection element identity")
		}
		for i, item := range value {
			var child *bashPPCollectionMeta
			if meta != nil && i < len(meta.sequence) {
				child = meta.sequence[i]
			}
			converted, err := r.bashPPBridgeCollection(item, child, collection.Element)
			if err != nil {
				return result, err
			}
			result.Elements = append(result.Elements, converted)
		}
		return result, nil
	case map[string]any:
		switch shape := r.bashPPUnderlyingType(typ).(type) {
		case *syntax.BashPPCollectionType:
			if shape.Kind != "map" {
				return result, fmt.Errorf("gosource: mapping without map type")
			}
			result.Kind = "map"
			for key, item := range value {
				keyValue := bashPPBridgeValue{Type: bashPPTypeText(shape.Key), Text: key}
				switch bashPPTypeText(r.bashPPUnderlyingType(shape.Key)) {
				case "string":
					keyValue.Kind = "string"
				case "bool":
					keyValue.Kind = "bool"
				default:
					keyValue.Kind = "int"
				}
				var child *bashPPCollectionMeta
				if meta != nil {
					child = meta.mapping[key]
				}
				converted, err := r.bashPPBridgeCollection(item, child, shape.Element)
				if err != nil {
					return result, err
				}
				result.Entries = append(result.Entries, bashPPBridgeEntry{Key: keyValue, Value: converted})
			}
			return result, nil
		case *syntax.BashPPStructType:
			result.Kind = "struct"
			result.Fields = map[string]bashPPBridgeValue{}
			for _, field := range shape.Fields {
				for _, name := range field.Names {
					item, exists := value[name.Value]
					if !exists {
						return result, fmt.Errorf("gosource: missing struct field %s", name.Value)
					}
					var child *bashPPCollectionMeta
					if meta != nil {
						child = meta.mapping[name.Value]
					}
					converted, err := r.bashPPBridgeCollection(item, child, field.FieldTypeExpr)
					if err != nil {
						return result, err
					}
					result.Fields[name.Value] = converted
				}
			}
			return result, nil
		default:
			return result, fmt.Errorf("gosource: missing mapping type schema")
		}
	case string:
		result.Kind = "string"
		result.Text = value
	case bool:
		result.Kind = "bool"
		result.Text = strconv.FormatBool(value)
	case int:
		result.Kind = "int"
		result.Text = strconv.Itoa(value)
	case int64:
		result.Kind = "int"
		result.Text = strconv.FormatInt(value, 10)
	case float64:
		result.Kind = "float"
		result.Text = strconv.FormatFloat(value, 'g', -1, 64)
	case nil:
		// A nil slice or map keeps its declared type and zero state; see
		// bashPPNilCollectionBridge in bashpp_collection_growth.go.
		if nilValue, ok := r.bashPPNilCollectionBridge(meta, typ); ok {
			return nilValue, nil
		}
		result.Kind = "nil"
		return result, nil
	default:
		return result, fmt.Errorf("gosource: unsupported interpreter collection value %T", value)
	}
	return result, nil
}
func (r *Runner) bashPPBridgeShortDecl(ctx context.Context, d *syntax.BashPPShortDecl) bool {
	if !r.bashPPBridgeHandles(d.Call) {
		return false
	}
	values, err := r.bashPPBridgeCall(ctx, d.Call)
	if err == nil && r.exit.exiting {
		// The dependency process terminated the program; the recorded status
		// must not be replaced by an arity diagnostic.
		return true
	}
	if err == nil && len(values) != len(d.Lhs) {
		err = fmt.Errorf("assignment mismatch: %d variables but %d native results", len(d.Lhs), len(values))
	}
	if err != nil {
		if !r.bashPPPanicking() {
			r.exit.fatal(err)
		}
		return true
	}
	for i, lhs := range d.Lhs {
		if lhs.Value == "_" {
			continue
		}
		r.bashPPBindNativeValue(lhs.Value, values[i])
	}
	return true
}

// bashPPBindNativeValue declares name from one native value. A scalar becomes
// an ordinary typed interpreter variable; anything else stays a session handle
// so the dependency keeps ownership, identity and mutation of the value.
func (r *Runner) bashPPBindNativeValue(name string, value bashPPBridgeValue) {
	scalar, err := value.scalar()
	if err == nil {
		r.bashPPDeclareName(name, expand.Variable{Set: true, Kind: expand.String, Str: bashPPScalarString(scalar.value)})
		cell := r.bashPPScope.lookup(name)
		cell.scalarKind = scalar.value.Kind()
		cell.typeName = value.Type
		cell.declType = &syntax.BashPPNamedType{Name: &syntax.Lit{Value: value.Type}}
		return
	}
	copy := value
	r.bashPPDeclareName(name, expand.NewObject(&copy))
	cell := r.bashPPScope.lookup(name)
	cell.typeName = value.Type
	if value.Interface != "" {
		cell.declType = &syntax.BashPPNamedType{Name: &syntax.Lit{Value: value.Interface}}
		payload := &bashPPCell{vr: expand.NewObject(&copy)}
		cell.interfaceValue = &bashPPInterfaceValue{nilIface: value.Kind == "nil", cell: payload, dynamic: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: value.Type}}}
	}
}

func bashPPBridgeTypeText(typ syntax.BashPPTypeExpr) string {
	switch t := typ.(type) {
	case *syntax.BashPPStructType:
		var fields []string
		for _, field := range t.Fields {
			if field.Embedded {
				return "<unsupported embedded field>"
			}
			names := make([]string, len(field.Names))
			for i, name := range field.Names {
				names[i] = name.Value
			}
			fields = append(fields, strings.Join(names, ",")+" "+bashPPBridgeTypeText(field.FieldTypeExpr))
		}
		return "struct{" + strings.Join(fields, ";") + "}"
	case *syntax.BashPPInterfaceType:
		if len(t.Methods) == 0 && len(t.Elems) == 0 {
			return "interface{}"
		}
	case *syntax.BashPPCollectionType:
		if t.Kind == "map" {
			return "map[" + bashPPBridgeTypeText(t.Key) + "]" + bashPPBridgeTypeText(t.Element)
		}
		length := ""
		if t.Length != nil {
			length = t.Length.Value
		}
		return "[" + length + "]" + bashPPBridgeTypeText(t.Element)
	}
	return bashPPTypeText(typ)
}

func (r *Runner) bashPPBridgeCell(cell *bashPPCell) (bashPPBridgeValue, error) {
	if cell != nil {
		if fn, ok := r.bashPPClosure(cell.vr.Str); ok {
			return r.bashPPBridgeFunction(fn)
		}
	}
	if cell == nil {
		return bashPPBridgeValue{}, fmt.Errorf("gosource: missing result cell")
	}
	if cell.interfaceValue != nil {
		if cell.interfaceValue.nilIface {
			return bashPPBridgeValue{Kind: "nil"}, nil
		}
		return r.bashPPBridgeCell(cell.interfaceValue.cell)
	}
	if cell.pointer {
		value, err := r.bashPPBridgePointerValue(cell.pointerValue)
		if value.Type == "" {
			value.Type = bashPPTypeText(cell.declType)
		}
		return value, err
	}
	if cell.vr.Kind == expand.Object {
		if value, ok := cell.vr.Obj.(*bashPPBridgeValue); ok {
			return *value, nil
		}
		return r.bashPPBridgeCollection(cell.vr.Obj, cell.valueMeta, cell.declType)
	}
	return bridgeScalar(r.bashPPScalarFromCell(cell))
}
