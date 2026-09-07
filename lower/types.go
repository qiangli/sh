package lower

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// typeExpr lowers the parser's committed type vocabulary. Name resolution,
// representability, method sets, constraints and inference are checked on the
// resulting Go program; the emitter does not execute or expand type text.
func (e *emitter) typeExpr(t syntax.BashPPTypeExpr) (string, error) {
	switch n := t.(type) {
	case *syntax.BashPPChanType:
		var element string
		var err error
		if n.Element != nil {
			element, err = e.typeExpr(n.Element)
		} else if n.Elem != nil {
			element, err = e.typeSpelling(n.Elem, n.Elem.Value)
		} else {
			return "", e.fail(n, CodeType, "channel element type is missing")
		}
		if err != nil {
			return "", err
		}
		switch n.Direction {
		case "":
			return "chan " + element, nil
		case "send":
			return "chan<- " + element, nil
		case "recv":
			return "<-chan " + element, nil
		default:
			return "", e.fail(n, CodeType, "unknown channel direction")
		}

	case *syntax.BashPPFuncType:
		params, err := e.signatureTypes(n.Params)
		if err != nil {
			return "", err
		}
		results, err := e.signatureTypes(n.Results)
		if err != nil {
			return "", err
		}
		if results != "" {
			results = " (" + results + ")"
		}
		return "func(" + params + ")" + results, nil

	case *syntax.BashPPNamedType:
		if n.Name == nil {
			return "", e.fail(nil, CodeType, "missing type name")
		}
		name, err := e.typeSpelling(n.Name, n.Name.Value)
		if err != nil {
			return "", err
		}
		args, err := e.typeArgs(n.TypeArgs)
		return name + args, err
	case *syntax.BashPPTypeParamType:
		if n.Name == nil {
			return "", e.fail(nil, CodeType, "missing type parameter name")
		}
		return e.typeSpelling(n.Name, n.Name.Value)
	case *syntax.BashPPPointerType:
		typ, err := e.typeExpr(n.Element)
		return "*" + typ, err
	case *syntax.BashPPCollectionType:
		typ, err := e.typeExpr(n.Element)
		if err != nil {
			return "", err
		}
		switch n.Kind {
		case "slice":
			return "[]" + typ, nil
		case "inferred-array":
			return "[...]" + typ, nil
		case "array":
			if n.Length == nil {
				return "", e.fail(n, CodeType, "array length is missing")
			}
			// Length is a parser-committed constant expression, not a shell word.
			_, err := parser.ParseExpr(n.Length.Value)
			if err != nil {
				return "", e.fail(n.Length, CodeType, "invalid array length expression")
			}
			return "[" + n.Length.Value + "]" + typ, nil
		case "map":
			key, err := e.typeExpr(n.Key)
			return "map[" + key + "]" + typ, err
		default:
			return "", e.fail(n, CodeType, "unknown collection type: "+n.Kind)
		}
	case *syntax.BashPPStructType:
		fields, err := e.structFields(n.Fields)
		return "struct {" + fields + "}", err
	case *syntax.BashPPInterfaceType:
		var parts []string
		// Methods is a compatibility projection of Elems. Emitting both duplicates
		// method declarations and loses the positioned embedded-element ordering.
		if len(n.Elems) > 0 {
			for _, elem := range n.Elems {
				if elem == nil {
					return "", e.fail(n, CodeType, "missing interface element")
				}
				var part string
				var err error
				if elem.Method != nil {
					part, err = e.methodSpec(elem.Method)
				} else {
					part, err = e.typeExpr(elem.Embedded)
				}
				if err != nil {
					return "", err
				}
				parts = append(parts, part)
			}
		} else {
			for _, method := range n.Methods {
				part, err := e.methodSpec(method)
				if err != nil {
					return "", err
				}
				parts = append(parts, part)
			}
		}
		return "interface {" + strings.Join(parts, "; ") + "}", nil
	case *syntax.BashPPUnionType:
		if len(n.Terms) == 0 {
			return "", e.fail(nil, CodeType, "empty type union")
		}
		var terms []string
		for _, term := range n.Terms {
			text, err := e.typeExpr(term)
			if err != nil {
				return "", err
			}
			terms = append(terms, text)
		}
		return strings.Join(terms, " | "), nil
	case *syntax.BashPPApproxType:
		term, err := e.typeExpr(n.Term)
		return "~" + term, err
	default:
		return "", e.fail(t, CodeType, "missing or unsupported positioned type")
	}
}

// typeSpelling is solely a compatibility adapter for already typed AST fields
// whose structured FieldTypeExpr was absent in an older parser representation.
// It accepts types, never arbitrary value expressions or source statements.
func (e *emitter) typeSpelling(n syntax.Node, text string) (string, error) {
	x, err := parser.ParseExpr(text)
	if err != nil || !nativeTypeSyntax(x) {
		return "", e.fail(n, CodeType, "invalid committed type: "+text)
	}
	return text, nil
}
func nativeTypeSyntax(x ast.Expr) bool {
	switch n := x.(type) {
	case *ast.Ident:
		return token.IsIdentifier(n.Name) && !token.Lookup(n.Name).IsKeyword()
	case *ast.SelectorExpr:
		_, ok := n.X.(*ast.Ident)
		return ok
	case *ast.StarExpr:
		return nativeTypeSyntax(n.X)
	case *ast.ParenExpr:
		return nativeTypeSyntax(n.X)
	case *ast.ArrayType:
		// The checker validates array-length constancy without evaluating input.
		return nativeTypeSyntax(n.Elt)
	case *ast.MapType:
		return nativeTypeSyntax(n.Key) && nativeTypeSyntax(n.Value)
	case *ast.ChanType:
		return nativeTypeSyntax(n.Value)
	case *ast.FuncType:
		return nativeTypeFields(n.Params) && nativeTypeFields(n.Results)
	case *ast.StructType:
		return nativeTypeFields(n.Fields)
	case *ast.InterfaceType:
		return nativeTypeFields(n.Methods)
	case *ast.IndexExpr:
		return nativeTypeSyntax(n.X) && nativeTypeSyntax(n.Index)
	case *ast.IndexListExpr:
		if !nativeTypeSyntax(n.X) {
			return false
		}
		for _, index := range n.Indices {
			if !nativeTypeSyntax(index) {
				return false
			}
		}
		return true
	case *ast.Ellipsis:
		return nativeTypeSyntax(n.Elt)
	case *ast.UnaryExpr:
		return n.Op == token.TILDE && nativeTypeSyntax(n.X)
	case *ast.BinaryExpr:
		return n.Op == token.OR && nativeTypeSyntax(n.X) && nativeTypeSyntax(n.Y)
	}
	return false
}
func nativeTypeFields(fields *ast.FieldList) bool {
	if fields == nil {
		return true
	}
	for _, field := range fields.List {
		if !nativeTypeSyntax(field.Type) {
			return false
		}
	}
	return true
}
func (e *emitter) fieldType(f *syntax.BashPPField) (string, error) {
	if f == nil {
		return "", e.fail(nil, CodeType, "missing field")
	}
	if f.FieldTypeExpr != nil {
		return e.typeExpr(f.FieldTypeExpr)
	}
	if f.FieldType != nil {
		return e.typeSpelling(f.FieldType, f.FieldType.Value)
	}
	return "", e.fail(f, CodeType, "missing field type")
}
func (e *emitter) structFields(fields []*syntax.BashPPField) (string, error) {
	var parts []string
	for _, f := range fields {
		if f == nil {
			return "", e.fail(nil, CodeType, "missing struct field")
		}
		if f.Default != nil || f.Ellipsis.IsValid() {
			return "", e.fail(f, CodeUnsupported, "struct field default or variadic modifier")
		}
		typ, err := e.fieldType(f)
		if err != nil {
			return "", err
		}
		if f.Embedded {
			parts = append(parts, typ)
			continue
		}
		if len(f.Names) == 0 {
			return "", e.fail(f, CodeType, "ordinary struct field has no name")
		}
		parts = append(parts, strings.Join(names(f.Names), ", ")+" "+typ)
	}
	return strings.Join(parts, "; "), nil
}

// signatureTypes deliberately does not bind names: interface parameters are
// declarative and must not leak identifiers into the surrounding value scope.
func (e *emitter) signatureTypes(fields []*syntax.BashPPField) (string, error) {
	var parts []string
	for i, f := range fields {
		typ, err := e.fieldType(f)
		if err != nil {
			return "", err
		}
		if f.Default != nil {
			return "", e.fail(f, CodeUnsupported, "interface signature default")
		}
		if f.Ellipsis.IsValid() {
			if i != len(fields)-1 {
				return "", e.fail(f, CodeType, "variadic parameter must be final")
			}
			typ = "..." + typ
		}
		if len(f.Names) > 0 {
			typ = strings.Join(names(f.Names), ", ") + " " + typ
		}
		parts = append(parts, typ)
	}
	return strings.Join(parts, ", "), nil
}
func (e *emitter) methodSpec(m *syntax.BashPPMethodSpec) (string, error) {
	if m == nil || m.Name == nil {
		return "", e.fail(nil, CodeType, "missing method name")
	}
	params, err := e.signatureTypes(m.Params)
	if err != nil {
		return "", err
	}
	results, err := e.signatureTypes(m.Results)
	if err != nil {
		return "", err
	}
	if results != "" {
		results = " (" + results + ")"
	}
	return m.Name.Value + "(" + params + ")" + results, nil
}
func (e *emitter) typeParams(params []*syntax.BashPPTypeParam) (string, error) {
	if len(params) == 0 {
		return "", nil
	}
	var parts []string
	for _, param := range params {
		if param == nil || len(param.Names) == 0 {
			return "", e.fail(nil, CodeType, "missing generic parameter name")
		}
		constraint, err := e.typeExpr(param.Constraint)
		if err != nil {
			return "", err
		}
		parts = append(parts, strings.Join(names(param.Names), ", ")+" "+constraint)
	}
	return "[" + strings.Join(parts, ", ") + "]", nil
}
func (e *emitter) typeArgs(args []*syntax.BashPPTypeArg) (string, error) {
	if len(args) == 0 {
		return "", nil
	}
	var parts []string
	for _, arg := range args {
		if arg == nil {
			return "", e.fail(nil, CodeType, "missing generic type argument")
		}
		text, err := e.typeExpr(arg.ArgType)
		if err != nil {
			return "", err
		}
		parts = append(parts, text)
	}
	return "[" + strings.Join(parts, ", ") + "]", nil
}
func (e *emitter) typeDecl(n *syntax.BashPPDecl) (string, error) {
	if n == nil || n.Name == nil {
		return "", e.fail(nil, CodeType, "missing declared type name")
	}
	if len(n.EnumMembers) > 0 {
		return "", e.fail(n, CodeUnsupported, "enum declarations require Bash# metadata lowering")
	}
	params, err := e.typeParams(n.TypeParams)
	if err != nil {
		return "", err
	}
	var typ string
	switch {
	case n.DeclTypeExpr != nil:
		typ, err = e.typeExpr(n.DeclTypeExpr)
	case n.DeclType != nil && n.DeclType.Value == "struct":
		typ, err = e.structFields(n.StructFields)
		typ = "struct {" + typ + "}"
	case n.DeclType != nil:
		typ, err = e.typeSpelling(n.DeclType, n.DeclType.Value)
	default:
		return "", e.fail(n, CodeType, "missing declared type")
	}
	if err != nil {
		return "", err
	}
	alias := " "
	if n.Alias {
		alias = " = "
	}
	e.bind(n.Name.Value)
	return "type " + n.Name.Value + params + alias + typ, nil
}
func (e *emitter) compositeExpr(n *syntax.BashPPCompositeLit) (string, error) {
	typ := ""
	var err error
	if n.LitType != nil {
		typ, err = e.typeExpr(n.LitType)
		if err != nil {
			return "", err
		}
	}
	var elems []string
	for _, elem := range n.Elems {
		if elem == nil {
			return "", e.fail(n, CodeExpr, "missing composite element")
		}
		value, err := e.compositeValue(elem.Value)
		if err != nil {
			return "", err
		}
		if elem.Key != nil {
			var key string
			// Go's checker distinguishes struct field identifiers from map key values.
			// The emitter must not look up a field name as a surrounding local variable.
			if id, ok := elem.Key.(*syntax.BashPPIdent); ok {
				key = id.Name.Value
			} else {
				key, err = e.expr(elem.Key)
			}
			if err != nil {
				return "", err
			}
			value = key + ": " + value
		}
		elems = append(elems, value)
	}
	return typ + "{" + strings.Join(elems, ", ") + "}", nil
}
func (e *emitter) compositeValue(x syntax.BashPPExpr) (string, error) {
	if n, ok := x.(*syntax.BashPPCompositeLit); ok {
		return e.compositeExpr(n)
	}
	return e.expr(x)
}
func (e *emitter) selectorExpr(n *syntax.BashPPSelectorExpr) (string, error) {
	value, err := e.expr(n.X)
	if err != nil {
		return "", err
	}
	if n.Sel == nil {
		return "", e.fail(n.X, CodeExpr, "missing selector name")
	}
	return "(" + value + ")." + n.Sel.Value, nil
}
func (e *emitter) typeAssertExpr(n *syntax.BashPPTypeAssertExpr) (string, error) {
	value, err := e.expr(n.X)
	if err != nil {
		return "", err
	}
	typ := "type"
	if n.TypeToken == nil {
		typ, err = e.typeExpr(n.Assert)
		if err != nil {
			return "", err
		}
	}
	return "(" + value + ").(" + typ + ")", nil
}

// nativeBuiltin identifies predeclared Go operations whose typed behavior uses
// native value/copy/alias semantics. Printing and shell-facing stringification
// require separate language runtime handling and are intentionally excluded.
func nativeBuiltin(name string) bool {
	switch name {
	case "append", "cap", "clear", "close", "complex", "copy", "delete", "imag", "len", "make", "max", "min", "new", "panic", "real", "recover":
		return true
	}
	return false
}
