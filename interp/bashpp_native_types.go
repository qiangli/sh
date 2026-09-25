package interp

// Sprint: #118; Story: #52; Story-ID: d564bada90bb
import (
	"fmt"
	"go/token"
	"mvdan.cc/sh/v3/syntax"
	"strings"
)

// Imported types retain the dependency's representation, including private
// fields. Only Go-source trees with an actual package binding use this path.
func (r *Runner) bashPPNativeType(typ syntax.BashPPTypeExpr) bool {
	if !r.bashPPGoSource {
		return false
	}
	switch t := typ.(type) {
	case *syntax.BashPPPointerType:
		return r.bashPPNativeType(t.Element)
	case *syntax.BashPPNamedType:
		alias, _, ok := strings.Cut(t.Name.Value, ".")
		return ok && r.bashPPImports[alias] != ""
	}
	return false
}
func (r *Runner) bashPPNativeTypeRequest(op string, typ syntax.BashPPTypeExpr, args ...bashPPBridgeValue) (bashPPBridgeValue, error) {
	req, err := r.bashPPEvalRequest()
	if err != nil {
		return bashPPBridgeValue{}, err
	}
	// Session-fixed answers ("type", handle "assignable") are remembered per
	// session; see bashpp_s248_native_type_facts.go.
	return r.bashPPNativeTypeFactRequest(req, op, typ, args)
}
func (r *Runner) bashPPNativeComposite(lit *syntax.BashPPCompositeLit, address bool) (bashPPBridgeValue, error) {
	return r.bashPPNativeCompositeAtBoundary(lit, address, false)
}

func (r *Runner) bashPPNativeCompositeAtBoundary(lit *syntax.BashPPCompositeLit, address, returnBoundary bool) (bashPPBridgeValue, error) {
	value := bashPPBridgeValue{Kind: "struct", Type: r.bashPPBridgeTypeIdentity(lit.LitType), Fields: map[string]bashPPBridgeValue{}}
	// Go allows either every field keyed or none; the positional form —
	// color.RGBA{c, c, 255, 255} — is filled in the dependency's own field
	// order, which only the dependency knows.
	for _, elem := range lit.Elems {
		field, err := r.bashPPBridgeExpr(elem.Value)
		if err != nil {
			return value, err
		}
		if elem.Key == nil {
			value.Elements = append(value.Elements, field)
			continue
		}
		key, ok := elem.Key.(*syntax.BashPPIdent)
		if !ok {
			return value, fmt.Errorf("gosource: imported struct literal requires named fields")
		}
		if _, exists := value.Fields[key.Name.Value]; exists {
			return value, fmt.Errorf("gosource: duplicate imported field %s", key.Name.Value)
		}
		value.Fields[key.Name.Value] = field
	}
	if len(value.Elements) > 0 && len(value.Fields) > 0 {
		return value, fmt.Errorf("gosource: imported struct literal mixes keyed and positional fields")
	}
	if returnBoundary && !address {
		value.deferredNativeComposite = true
		return value, nil
	}
	op := "construct"
	if address {
		op = "address"
	}
	return r.bashPPNativeTypeRequest(op, lit.LitType, value)
}
func (r *Runner) bashPPNativeDeclaration(d *syntax.BashPPDecl) bool {
	if !r.bashPPGoSource || d.Site != syntax.StartVar {
		return false
	}
	if r.bashPPNativeEmbedDeclaration(d) {
		return true
	}
	var value bashPPBridgeValue
	var err error
	if d.InitExpr != nil && r.bashPPNativeExpr(d.InitExpr) {
		value, err = r.bashPPBridgeExpr(d.InitExpr)
	} else if d.InitExpr == nil && r.bashPPNativeType(d.DeclTypeExpr) {
		value, err = r.bashPPNativeTypeRequest("new", d.DeclTypeExpr)
	} else {
		return false
	}
	if err != nil {
		r.exit.fatal(&goSourceError{prefix: r.bashErrPrefix(d.Pos()), err: err})
		return true
	}
	if _, iface := r.bashPPInterfaceType(d.DeclTypeExpr); iface {
		value.Interface = r.bashPPBridgeTypeIdentity(d.DeclTypeExpr)
	}
	if d.Name.Value != "_" {
		r.bashPPBindNativeValue(d.Name.Value, value)
	}
	return true
}
func (r *Runner) bashPPNativeCompare(left syntax.BashPPExpr, op token.Token, right syntax.BashPPExpr) (bool, error) {
	lv, err := r.bashPPBridgeExpr(left)
	if err != nil {
		return false, err
	}
	rv, err := r.bashPPBridgeExpr(right)
	if err != nil {
		return false, err
	}
	return r.bashPPNativeCompareValues(lv, op, rv)
}

// goSourceNativeHandleIsNil reports whether a dependency-owned pointer,
// which the worker may hand over as a handle even when nil, is the nil
// pointer. The interface marker is dropped first: the question is about the
// dynamic value, and a typed nil inside an interface is not a nil interface.
func (r *Runner) goSourceNativeHandleIsNil(value bashPPBridgeValue) (bool, error) {
	if value.Kind == "nil" {
		return true, nil
	}
	if value.Kind != "handle" {
		return false, nil
	}
	value.Interface = ""
	return r.bashPPNativeCompareValues(value, token.EQL, bashPPBridgeValue{Kind: "nil"})
}

func (r *Runner) bashPPNativeCompareValues(lv bashPPBridgeValue, op token.Token, rv bashPPBridgeValue) (bool, error) {
	lv, rv = bashPPBridgeCompareOperands(lv, rv)
	if equal, handled := bashPPNativeScalarEqual(lv, rv); handled {
		if op == token.NEQ {
			equal = !equal
		}
		return equal, nil
	}
	// Building an evaluator request snapshots environment and local-type state.
	// Scalar values never cross the bridge, so defer that work until the local
	// comparison above has proved it is actually necessary.
	req, err := r.bashPPEvalRequest()
	if err != nil {
		return false, err
	}
	values, err := r.bashPPNativeRequest(r.ectx, req, bashPPBridgeRequest{Op: "equal", Args: []bashPPBridgeValue{lv, rv}})
	if err != nil {
		// Comparing identical uncomparable dynamic types (a func value in an
		// interface, say) is a recoverable run-time panic in Go, not a
		// dependency failure: reword reflect's refusal as the runtime does.
		const prefix, suffix = "native dependency panic: reflect.Value.Equal: values of type ", " are not comparable"
		if _, rest, ok := strings.Cut(err.Error(), prefix); ok {
			if typ, ok := strings.CutSuffix(strings.SplitN(rest, "\n", 2)[0], suffix); ok {
				r.bashPPRaise("runtime error: comparing uncomparable type " + typ)
				return false, errBashPPScalarInterrupted
			}
		}
		return false, err
	}
	if len(values) != 1 || values[0].Kind != "bool" {
		return false, fmt.Errorf("gosource: malformed native comparison")
	}
	equal := values[0].Text == "true"
	if op == token.NEQ {
		equal = !equal
	}
	return equal, nil
}
