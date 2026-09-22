package interp

// Sprint: #118; Story: #50; Story-ID: cf81e4868348
import (
	"context"
	"errors"
	"fmt"
	"go/constant"
	"go/token"
	"math"
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
		if !ok {
			return false
		}
		return r.bashPPNativeExpr(selector.X) ||
			r.goSourceNativeScalarReceiver(selector.X) ||
			r.bashPPNativePointerExpr(selector.X) ||
			r.bashPPPromotedNativeReceiver(selector.X, selector.Sel.Value) != nil
	}
	if len(call.Fun) < 1 {
		return false
	}
	if len(call.Fun) >= 2 {
		if _, ok := r.bashPPImports[call.Fun[0].Value]; ok {
			// unsafe.Sizeof, Alignof and Offsetof are constant operators
			// the evaluator folds from the operand's declared type; the
			// dependency has no callable symbol for them.
			return !r.goSourceUnsafeConstantOperator(call)
		}
		if len(call.Fun) == 2 && r.goSourceOriginalMethodCall(call.Fun[0].Value, call.Fun[1].Value) {
			return false
		}
		if r.bashPPNativeCellValue(call.Fun[0].Value) != nil {
			return true
		}
		if len(call.Fun) == 2 && (r.goSourceNativeScalarReceiver(&syntax.BashPPIdent{Name: call.Fun[0]}) ||
			r.bashPPNativePointerExpr(&syntax.BashPPIdent{Name: call.Fun[0]})) {
			return true
		}
		var receiver syntax.BashPPExpr = &syntax.BashPPIdent{Name: call.Fun[0]}
		for _, part := range call.Fun[1 : len(call.Fun)-1] {
			receiver = &syntax.BashPPSelectorExpr{X: receiver, Sel: part}
		}
		if len(call.Fun) > 2 && r.bashPPNativeExpr(receiver) {
			return true
		}
		// A method promoted from an embedded imported type is the dependency's
		// to run even though the receiver spelling names a local struct.
		if r.bashPPPromotedNativeReceiver(receiver, call.Fun[len(call.Fun)-1].Value) != nil {
			return true
		}
	}
	if len(call.Fun) == 1 {
		if value := r.bashPPNativeCellValue(call.Fun[0].Value); value != nil && value.Kind == "handle" && (value.Function || strings.HasPrefix(value.Type, "func(")) {
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
	// The integer sync/atomic functions address interpreter storage, so they
	// are answered here rather than prepared as a dependency request; see
	// gosource_atomic.md.
	if values, claimed, err := r.goSourceAtomicCall(call); claimed {
		return values, err
	}
	// unsafe.String reads interpreter-owned byte storage through an original
	// pointer, so it is answered here as well.
	if values, claimed, err := r.goSourceUnsafeStringCall(call); claimed {
		return values, err
	}
	// Stack introspection reads the interpreter's own frames; see
	// bashpp_sprint162_nilptr2_stack.go.
	if values, claimed, err := r.goSourceRuntimeStackCall(call); claimed {
		return values, err
	}
	q, err := r.bashPPPrepareNativeCall(ctx, call)
	if err != nil {
		return nil, err
	}
	req, err := r.bashPPEvalRequest()
	if err != nil {
		return nil, err
	}
	if !r.goSourceNativeSleepBoundary(ctx, req, q) {
		return nil, errBashPPScalarInterrupted
	}
	return r.bashPPNativeRequest(ctx, req, q)
}
func (r *Runner) bashPPPrepareNativeCall(ctx context.Context, call *syntax.BashPPCall) (bashPPBridgeRequest, error) {
	if !r.bashPPBridgeHandles(call) {
		return bashPPBridgeRequest{}, fmt.Errorf("gosource: call is not an imported dependency operation")
	}
	q := bashPPBridgeRequest{Op: "call", Spread: call.Ellipsis.IsValid()}
	if r.bashPPGoSourceFile != nil && call.Pos().IsValid() {
		if source, ok := r.bashPPGoSourceFile.SourceAt(call.Pos()); ok {
			q.SourceFile = source.Name
			q.SourceLine = int(call.Pos().Line())
		}
	}
	if selector, ok := call.CalleeExpr.(*syntax.BashPPSelectorExpr); ok {
		receiver, err := r.bashPPNativeMethodReceiver(selector.X, selector.Sel.Value)
		if err != nil {
			return bashPPBridgeRequest{}, err
		}
		q.Receiver = &receiver
		q.Selector = selector.Sel.Value
	} else if len(call.Fun) >= 2 {
		if _, ok := r.bashPPImports[call.Fun[0].Value]; ok && len(call.Fun) == 2 {
			q.Selector = call.Fun[0].Value + "." + call.Fun[1].Value
			if len(call.TypeArgs) > 0 {
				// An imported generic function is registered per
				// instantiation; the call names the instantiation under the
				// enclosing frame's type bindings, beside the selector every
				// host-side policy reads. See
				// bashpp_sprint171_imported_instances.go.
				args := make([]syntax.BashPPTypeExpr, len(call.TypeArgs))
				for i, arg := range call.TypeArgs {
					args[i] = r.bashPPBindTypeExpr(arg.ArgType)
				}
				q.Instance = bashPPImportedInstanceSuffix(args)
			}
		} else {
			var receiverExpr syntax.BashPPExpr = &syntax.BashPPIdent{Name: call.Fun[0]}
			for _, part := range call.Fun[1 : len(call.Fun)-1] {
				receiverExpr = &syntax.BashPPSelectorExpr{X: receiverExpr, Sel: part}
			}
			receiver, err := r.bashPPNativeMethodReceiver(receiverExpr, call.Fun[len(call.Fun)-1].Value)
			if err != nil {
				return bashPPBridgeRequest{}, err
			}
			q.Receiver = &receiver
			q.Selector = call.Fun[len(call.Fun)-1].Value
		}
	} else if value := r.bashPPNativeCellValue(call.Fun[0].Value); value != nil && value.Kind == "handle" && (value.Function || strings.HasPrefix(value.Type, "func(")) {
		copy := *value
		q.Receiver = &copy
	} else {
		q.Selector = call.Fun[0].Value
	}
	if q.Receiver == nil {
		if alias, name, ok := strings.Cut(q.Selector, "."); ok && r.bashPPImports[alias] == "log" {
			switch name {
			case "Print", "Println", "Printf":
				q.LogPrint = name
			}
		}
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
	case *syntax.BashPPDerefExpr:
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
		// math.NaN() and math.Inf(1) cross as the text go/constant cannot
		// hold; they are the runtime non-finite scalar, not an invalid one.
		if special, ok := bashPPNonFiniteText(value.Text); ok {
			return bashPPNonFiniteScalar(special, value.Type), nil
		}
		scalar.value = constant.MakeFromLiteral(value.Text, token.FLOAT, 0)
		if number, err := strconv.ParseFloat(value.Text, 64); err == nil {
			scalar.negativeZero = number == 0 && math.Signbit(number)
		}
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
	if value.hasNonFinite {
		return bashPPBridgeValue{Kind: "float", Type: value.typ, Text: strconv.FormatFloat(value.nonFinite, 'g', -1, 64)}, nil
	}
	switch value.value.Kind() {
	case constant.String:
		out.Kind = "string"
		out.Text = constant.StringVal(value.value)
		out.Bytes = []byte(out.Text)
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
		if value.negativeZero && number == 0 {
			out.Text = "-0"
		}
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
	if value, ok := r.goSourceTypedNilBridgeValue(expr); ok {
		return value, nil
	}
	if value, handled, err := r.goSourceRecoverBridgeValue(expr); handled {
		return value, err
	}
	switch x := expr.(type) {
	case *syntax.BashPPUnaryExpr:
		if cell, handled, err := r.goSourceChannelValueCell(x); handled {
			if err != nil {
				return bashPPBridgeValue{}, err
			}
			return r.bashPPBridgeCell(cell)
		}
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
		// `fmt.Println(recover())`: the recovered value crosses as the
		// interface value it is, with its dynamic type.
		if r.bashPPPredeclaredRecover(x) {
			iv, _ := r.bashPPRecoverInterfaceValue()
			return r.bashPPBridgeCell(&bashPPCell{declType: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "any"}}, interfaceValue: iv})
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
		if r.bashPPGoSource {
			if fn, ok := r.bashPPLookupFunc(x); ok {
				cells, err := r.goSourceCallResultCells(x, fn)
				if err != nil {
					return bashPPBridgeValue{}, err
				}
				if len(cells) != 1 {
					return bashPPBridgeValue{}, fmt.Errorf("gosource: expression requires one local result, got %d", len(cells))
				}
				return r.bashPPBridgeCell(cells[0])
			}
			if r.exit.exiting || r.exit.fatalExit || r.exit.err != nil {
				return bashPPBridgeValue{}, errBashPPScalarInterrupted
			}
		}
	case *syntax.BashPPSelectorExpr:
		if value := r.bashPPNativeLocalField(x); value != nil {
			return *value, nil
		}
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
		if typ, ok := r.goSourceStaticExprType(x); ok && r.bashPPNativeType(typ) {
			value, meta, err := r.bashPPReadExpr(x)
			if err != nil {
				return bashPPBridgeValue{}, err
			}
			native, ok := value.(*bashPPBridgeValue)
			if !ok || native == nil || meta == nil {
				return bashPPBridgeValue{}, fmt.Errorf("gosource: native collection element lost its authenticated handle")
			}
			checked, _, err := r.goSourceNativeAssignedValue(*native, typ)
			if err != nil {
				return bashPPBridgeValue{}, err
			}
			return *checked.(*bashPPBridgeValue), nil
		}
	case *syntax.BashPPSliceExpr:
		if r.bashPPNativeExpr(x.X) {
			return r.bashPPNativeSlice(x)
		}
	case *syntax.BashPPDerefExpr:
		if r.bashPPNativeExpr(x.X) {
			base, err := r.bashPPBridgeExpr(x.X)
			if err != nil {
				return bashPPBridgeValue{}, err
			}
			return r.bashPPNativeAccess(r.ectx, "deref", base, "")
		}
		ptr, err := r.bashPPPointerExprValue(x.X)
		if err != nil {
			return bashPPBridgeValue{}, err
		}
		if ptr == nil {
			return bashPPBridgeValue{}, r.goSourceRuntimeFault(errBashPPNilDereference)
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
	case *syntax.BashPPNewExpr:
		// `new(T)` is the pointer it allocates, exactly as `&T{}` is.
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
	if value, handled, err := r.goSourceBridgeCollectionRead(expr); handled {
		return value, err
	}
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
	value, err = r.bashPPBridgeDefinedScalar(value)
	return bashPPBridgeInstantiatedScalar(value, r.bashPPExprScalarType(expr)), err
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
	if value.Type == "" {
		return value, nil
	}
	named := &syntax.BashPPNamedType{Name: &syntax.Lit{Value: value.Type}}
	underlying := bashPPTypeText(r.bashPPUnderlyingType(named))
	if underlying == value.Type {
		return value, nil
	}
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
		integer := constant.MakeFromLiteral(value.Text, token.INT, 0)
		if integer.Kind() != constant.Int || !bashPPIntegerRepresentable(underlying, integer) {
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
	req, err := r.bashPPEvalRequest()
	if err != nil {
		return bashPPBridgeValue{}, err
	}
	session := req.Bridge
	// The origin is known before the pointee crosses, and a pointer met
	// again on its own transport path is a back-reference, not a second
	// walk; see bashpp_sprint165_runtime2_cycle.go.
	origin := bashPPTransportOrigin(session, ptr)
	onPath, leave := r.bashPPTransportEnter(origin)
	if onPath {
		return bashPPTransportBackReference(session, origin, meta, typ), nil
	}
	defer leave()
	inner, isInterface, err := r.goSourceInterfacePointee(ptr)
	if err != nil {
		return bashPPBridgeValue{}, err
	}
	if !isInterface {
		inner, err = r.bashPPBridgeCollection(value, meta, typ)
		if err != nil {
			return bashPPBridgeValue{}, err
		}
	}
	pointerType := "*" + inner.Type
	if isInterface {
		// The pointee is the interface variable, whatever it holds.
		pointerType = "*" + inner.Interface
	}
	return bashPPBridgeValue{Origin: origin, Session: session.id, Kind: "pointer", Type: pointerType, Elements: []bashPPBridgeValue{inner}}, nil
}

func (s *bashPPNativeSession) applyNativePointerUpdates(req bashPPEvalRequest, reply bashPPBridgeResponse) error {
	if len(reply.PtrUpdates) == 0 {
		return nil
	}
	owner := req.CallbackOwner
	if owner == nil {
		return fmt.Errorf("gosource: native pointer writeback has no request owner")
	}
	for _, update := range reply.PtrUpdates {
		if update.Origin == 0 || update.Session != s.id {
			return fmt.Errorf("gosource: native pointer writeback has invalid origin")
		}
		s.mu.Lock()
		ptr := s.origins[update.Origin]
		s.mu.Unlock()
		if ptr == nil {
			return fmt.Errorf("gosource: native pointer writeback target expired")
		}
		if len(update.Elements) != 1 {
			return fmt.Errorf("gosource: native pointer writeback needs one value")
		}
		// A structural pointee may nest native values this session still owns.
		// They arrived on its own authenticated connection, so they carry its
		// identity — a handle from any other session still fails closed.
		s.bashPPAuthenticateCallbackValue(&update.Elements[0])
		if err := owner.bashPPWriteBridgePointer(ptr, update.Elements[0]); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) bashPPWriteBridgePointer(ptr *bashPPPointer, value bashPPBridgeValue) error {
	if ptr == nil {
		return errBashPPNilDereference
	}
	if ptr.target.object != nil && ptr.target.object.readonly {
		return fmt.Errorf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value %q through pointer", ptr.target.object.owner)
	}
	if ptr.target.constant || ptr.target.vr.ReadOnly {
		return fmt.Errorf("BASHPP-EREADONLY-MUTATION: cannot mutate readonly value through pointer")
	}
	_, _, typ, err := ptr.read()
	if err != nil {
		return err
	}
	converted, meta, err := r.bashPPBridgeContents(value, typ)
	if err != nil {
		return fmt.Errorf("gosource: native pointer writeback: %w", err)
	}
	if len(ptr.path) == 0 {
		bashPPStoreCellValue(ptr.target, converted, meta)
		return nil
	}
	parent, parentMeta, _, err := ptr.readParent()
	if err != nil {
		return err
	}
	last := ptr.path[len(ptr.path)-1]
	if last.field != "" {
		mapping, ok := parent.(map[string]any)
		if !ok {
			return fmt.Errorf("BASHPP-EPOINTER-TARGET: pointer field path no longer names struct storage")
		}
		var layout map[string]*bashPPCollectionMeta
		if parentMeta != nil {
			layout = parentMeta.mapping
		}
		bashPPStorageSetField(mapping, layout, last.field, converted, meta)
		return nil
	}
	sequence, ok := parent.([]any)
	if !ok || last.index < 0 || last.index >= len(sequence) {
		return fmt.Errorf("BASHPP-EPOINTER-TARGET: pointer index no longer names collection storage")
	}
	sequence[last.index] = converted
	if parentMeta != nil {
		parentMeta.sequence[last.index] = meta
	}
	return nil
}

// bashPPBridgeIntegerCarrier reports the numeric wire kind for a collection
// element carried as its decimal spelling. Only a well-formed integer literal
// that is representable in the destination integer type qualifies, so an
// ordinary string element (declared type string, or a named string type) is
// left to cross as text. This is the large-unsigned carrier — a []uint64
// element above math.MaxInt64 that the signed int carrier cannot hold.
func (r *Runner) bashPPBridgeIntegerCarrier(typ syntax.BashPPTypeExpr, text string) (kind string, ok bool) {
	name, isName := r.bashPPUnderlyingType(typ).(*syntax.BashPPNamedType)
	if !isName {
		return "", false
	}
	dest, ok := r.bashPPUnderlyingIntegerName(name.Name.Value)
	if !ok || !bashPPCollectionIntegerText(dest, text) {
		return "", false
	}
	switch dest {
	case "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "byte":
		return "uint", true
	default:
		return "int", true
	}
}

func (r *Runner) bashPPBridgeCollection(value any, meta *bashPPCollectionMeta, typ syntax.BashPPTypeExpr) (bashPPBridgeValue, error) {
	if meta != nil && meta.interfaceValue != nil {
		cell := meta.interfaceValue.cell
		if meta.interfaceValue.nilIface || cell == nil {
			return bashPPBridgeValue{Kind: "nil"}, nil
		}
		if bashPPRuntimeErrorType(meta.interfaceValue.dynamic) {
			return bashPPBridgeValue{Kind: "string", Text: bashPPRuntimeErrorText(meta.interfaceValue)}, nil
		}
		if cell.pointer {
			if r.bashPPGoSource {
				return r.bashPPBridgeCell(cell)
			}
			return r.bashPPBridgePointerValue(cell.pointerValue)
		}
		if cell.vr.Kind == expand.Object {
			return r.bashPPBridgeCollection(cell.vr.Obj, cell.valueMeta, cell.declType)
		}
		if bashPPRuntimeErrorType(cell.declType) {
			return bashPPBridgeValue{Kind: "string", Text: cell.vr.String()}, nil
		}
		scalar, err := bridgeScalar(r.bashPPScalarFromCell(cell))
		return bashPPBridgeInstantiatedScalar(scalar, cell.declType), err
	}
	if meta != nil && meta.typ != nil {
		typ = meta.typ
	}
	result := bashPPBridgeValue{Type: bashPPBridgeTypeText(typ)}
	switch value := value.(type) {
	case *bashPPBridgeValue:
		if value == nil || !r.bashPPGoSource {
			return bashPPBridgeValue{}, fmt.Errorf("gosource: missing native field value")
		}
		return *value, nil
	case *bashPPPointer:
		// An element or field holding a pointer to original storage — the
		// []*T shape — crosses as the pointee it addresses, with the identity
		// that lets a method callback bind back to that same storage.
		if !r.bashPPGoSource {
			return result, fmt.Errorf("gosource: unsupported interpreter collection value %T", value)
		}
		return r.bashPPBridgePointerValue(value)
	case []any:
		result.Kind = "slice"
		inferredArray := false
		if meta != nil {
			result.Kind = meta.kind
			if result.Kind == "inferred-array" {
				result.Kind = "array"
				inferredArray = true
			}
		}
		collection, ok := r.bashPPUnderlyingType(typ).(*syntax.BashPPCollectionType)
		if !ok {
			return result, fmt.Errorf("gosource: missing collection element identity")
		}
		// An inferred-length array literal ([...]T) carries the placeholder "..."
		// in its declared type. The dependency's reflect-based type resolver only
		// parses a concrete length, so emit the realised element count instead.
		if inferredArray {
			result.Type = "[" + strconv.Itoa(len(value)) + "]" + bashPPBridgeTypeText(collection.Element)
		}
		// A named array type the helper does not materialise — its length is a
		// constant name or expression the helper cannot evaluate — still has a
		// realised length here. Transport the structural spelling the resolver
		// can parse, exactly like the inferred-length form above.
		if result.Kind == "array" && !inferredArray && r.bashPPGoSource && !r.bashPPBridgeResolvableArrayType(typ, collection) {
			result.Type = "[" + strconv.Itoa(len(value)) + "]" + bashPPBridgeTypeText(collection.Element)
		}
		if r.bashPPGoSource && result.Kind == "slice" {
			result.sliceView = &bashPPNativeSlice{view: value, meta: meta, typ: typ}
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
			if bashPPSprint165MapHasTypedKeys(meta) {
				for _, entry := range bashPPSprint165MapEntries(meta) {
					keyValue, err := r.bashPPBridgeCollection(entry.key, entry.keyMeta, shape.Key)
					if err != nil {
						return result, err
					}
					item, child, found := bashPPSprint165MapEntryValue(value, meta, entry.storage)
					if !found {
						continue
					}
					converted, err := r.bashPPBridgeCollection(item, child, shape.Element)
					if err != nil {
						return result, err
					}
					result.Entries = append(result.Entries, bashPPBridgeEntry{Key: keyValue, Value: converted})
				}
				return result, nil
			}
			for key, item := range bashPPStorageSnapshot(value) {
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
					child = bashPPLayoutGet(meta.mapping, key)
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
			// Flattening includes embedded fields under their promoted names,
			// which is where the interpreter keeps their storage; the worker's
			// FieldByName and the generated codecs address the same names.
			for _, field := range bashPPFlatFields(shape.Fields) {
				// Blank fields have layout but no addressable storage. The
				// native declaration supplies their zero values.
				if r.bashPPGoSource && field.name == "_" {
					continue
				}
				item, exists := bashPPStorageGet(value, field.name)
				if !exists {
					return result, fmt.Errorf("gosource: missing struct field %s", field.name)
				}
				var child *bashPPCollectionMeta
				if meta != nil {
					child = bashPPLayoutGet(meta.mapping, field.name)
				}
				converted, err := r.bashPPBridgeCollection(item, child, field.typ)
				if err != nil {
					return result, err
				}
				result.Fields[field.name] = converted
			}
			return result, nil
		default:
			return result, fmt.Errorf("gosource: missing mapping type schema")
		}
	case string:
		// A large unsigned integer element is carried as its decimal spelling
		// because it exceeds the interpreter's signed int carrier. Transport it
		// with the numeric wire kind its declared type needs; an ordinary
		// string element still crosses as a string.
		if kind, ok := r.bashPPBridgeIntegerCarrier(typ, value); ok {
			result.Kind = kind
		} else {
			result.Kind = "string"
		}
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
		r.bashPPDeclareName(name, expand.Variable{Set: true, Kind: expand.String, Str: bashPPScalarStorageString(scalar)})
		cell := r.bashPPScope.lookup(name)
		cell.scalarKind = scalar.value.Kind()
		cell.negativeZero = scalar.negativeZero
		cell.nonFinite, cell.hasNonFinite = scalar.nonFinite, scalar.hasNonFinite
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
	case *syntax.BashPPChanType:
		return goSourceNativeChannelTypeText(t)
	case *syntax.BashPPStructType:
		var fields []string
		for _, field := range t.Fields {
			// An embedded field spells only its element type, exactly as the
			// original wrote it; the helper materialises the same embedding.
			if field.Embedded {
				text := bashPPBridgeTypeText(field.FieldTypeExpr)
				if field.Tag != nil {
					text += " " + field.Tag.Value
				}
				fields = append(fields, text)
				continue
			}
			names := make([]string, len(field.Names))
			for i, name := range field.Names {
				names[i] = name.Value
			}
			text := strings.Join(names, ",") + " " + bashPPBridgeTypeText(field.FieldTypeExpr)
			if field.Tag != nil {
				text += " " + field.Tag.Value
			}
			fields = append(fields, text)
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
		if bashPPRuntimeErrorType(cell.interfaceValue.dynamic) {
			return bashPPBridgeValue{Kind: "string", Text: bashPPRuntimeErrorText(cell.interfaceValue)}, nil
		}
		value, err := r.bashPPBridgeCell(cell.interfaceValue.cell)
		if err == nil && r.bashPPGoSource {
			// A typed nil dynamic value is still a nonnil interface. Range
			// copies and argument/result cells must retain that static wrapper.
			value.Interface = bashPPBridgeTypeText(cell.declType)
		}
		return value, err
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
	// A runtime error's payload cell (declared as the runtime's unexported
	// error type, see bashpp_sprint162_runtime_error.go) crosses as its
	// message string: the dependency has no such type to resolve, and the
	// message is exactly what %v / %s / Error() print.
	if bashPPRuntimeErrorType(cell.declType) {
		return bashPPBridgeValue{Kind: "string", Text: cell.vr.String()}, nil
	}
	scalar, err := bridgeScalar(r.bashPPScalarFromCell(cell))
	if err != nil {
		return scalar, err
	}
	scalar = bashPPBridgeInstantiatedScalar(scalar, cell.declType)
	return r.bashPPBridgeDefinedScalar(scalar)
}

// goSourceUnsafeConstantOperator reports whether call is one of the unsafe
// constant operators — Sizeof, Alignof or Offsetof of one operand — that
// goSourceUnsafeConstant folds; such a call is never a dependency request.
func (r *Runner) goSourceUnsafeConstantOperator(call *syntax.BashPPCall) bool {
	if !r.bashPPGoSource || len(call.Fun) != 2 || len(call.ArgExprs) != 1 || r.bashPPImports[call.Fun[0].Value] != "unsafe" {
		return false
	}
	switch call.Fun[1].Value {
	case "Sizeof", "Alignof", "Offsetof":
		return true
	}
	return false
}

// goSourceUnsafeStringCall answers `unsafe.String(ptr, len)`: the string of
// len bytes starting at the byte ptr names. The pointer is one of the
// interpreter's own — into an array or slice element, or to a single byte
// variable — so the bytes are read from that storage; the dependency helper
// could neither see it nor return a string over it. A nil pointer with a
// zero length is the empty string; with any other length, or a length past
// the storage the pointer names, the call is the run-time fault Go raises.
// It reports claimed=false for any other call.
func (r *Runner) goSourceUnsafeStringCall(call *syntax.BashPPCall) (values []bashPPBridgeValue, claimed bool, err error) {
	defer func() { err = r.goSourceRuntimeFault(err) }()
	if !r.bashPPGoSource || call == nil || len(call.Fun) != 2 || call.Fun[1].Value != "String" || r.bashPPImports[call.Fun[0].Value] != "unsafe" {
		return nil, false, nil
	}
	if len(call.ArgExprs) != 2 || len(call.ArgExprs) != len(call.Args) || call.Ellipsis.IsValid() {
		return nil, false, nil
	}
	if r.bashPPScope != nil && r.bashPPScope.lookup(call.Fun[0].Value) != nil {
		return nil, false, nil
	}
	ptr, err := r.bashPPPointerExprValue(call.ArgExprs[0])
	if err != nil {
		return nil, true, err
	}
	length, err := r.bashPPEvalScalarExpr(call.ArgExprs[1])
	if err != nil {
		return nil, true, err
	}
	n, ok := constant.Int64Val(constant.ToInt(length.value))
	if !ok || n < 0 {
		return nil, true, &bashPPRuntimeError{refusal: "BASHPP-EUNSAFE-STRING: unsafe.String: len out of range", runtime: "unsafe.String: len out of range"}
	}
	if ptr == nil {
		if n == 0 {
			return []bashPPBridgeValue{{Kind: "string", Type: "string", NativeType: "string"}}, true, nil
		}
		return nil, true, &bashPPRuntimeError{refusal: "BASHPP-EUNSAFE-STRING: unsafe.String: ptr is nil and len is not zero", runtime: "unsafe.String: ptr is nil and len is not zero"}
	}
	var bytes []any
	if last := len(ptr.path) - 1; last >= 0 && ptr.path[last].field == "" && !ptr.path[last].deref {
		// A pointer to an element: the bytes run on from that element.
		parent, _, _, err := ptr.readParent()
		if err != nil {
			return nil, true, err
		}
		seq, ok := parent.([]any)
		if !ok || ptr.path[last].index < 0 || ptr.path[last].index > len(seq) {
			return nil, true, fmt.Errorf("BASHPP-EUNSAFE-STRING: pointer no longer names byte storage")
		}
		bytes = seq[ptr.path[last].index:]
	} else {
		value, _, _, err := ptr.read()
		if err != nil {
			return nil, true, err
		}
		bytes = []any{value}
	}
	if n > int64(len(bytes)) {
		return nil, true, &bashPPRuntimeError{refusal: "BASHPP-EUNSAFE-STRING: unsafe.String: len out of range", runtime: "unsafe.String: len out of range"}
	}
	out := make([]byte, n)
	for i := range out {
		b, ok := bytes[i].(int)
		if !ok || b < 0 || b > 255 {
			return nil, true, fmt.Errorf("BASHPP-EUNSAFE-STRING: pointer does not name byte storage")
		}
		out[i] = byte(b)
	}
	return []bashPPBridgeValue{{Kind: "string", Type: "string", NativeType: "string", Text: string(out), Bytes: out}}, true, nil
}
