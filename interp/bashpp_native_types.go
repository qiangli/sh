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
	values, err := r.bashPPNativeRequest(r.ectx, req, bashPPBridgeRequest{Op: op, Selector: bashPPBridgeTypeText(typ), Args: args})
	if err != nil {
		return bashPPBridgeValue{}, err
	}
	if len(values) != 1 {
		return bashPPBridgeValue{}, fmt.Errorf("gosource: native type operation returned %d values", len(values))
	}
	return values[0], nil
}
func (r *Runner) bashPPNativeComposite(lit *syntax.BashPPCompositeLit, address bool) (bashPPBridgeValue, error) {
	value := bashPPBridgeValue{Kind: "struct", Type: bashPPBridgeTypeText(lit.LitType), Fields: map[string]bashPPBridgeValue{}}
	for _, elem := range lit.Elems {
		key, ok := elem.Key.(*syntax.BashPPIdent)
		if !ok {
			return value, fmt.Errorf("gosource: imported struct literal requires named fields")
		}
		if _, exists := value.Fields[key.Name.Value]; exists {
			return value, fmt.Errorf("gosource: duplicate imported field %s", key.Name.Value)
		}
		field, err := r.bashPPBridgeExpr(elem.Value)
		if err != nil {
			return value, err
		}
		value.Fields[key.Name.Value] = field
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
	req, err := r.bashPPEvalRequest()
	if err != nil {
		return false, err
	}
	values, err := r.bashPPNativeRequest(r.ectx, req, bashPPBridgeRequest{Op: "equal", Args: []bashPPBridgeValue{lv, rv}})
	if err != nil {
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
