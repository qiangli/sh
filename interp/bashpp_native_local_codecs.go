package interp

// Sprint: #118; Story: #54; Story-ID: c3a60493cde9
// Codecs contain typed field selectors only. Original method bodies never
// enter the helper; private field access is legal because mirrored declarations
// and their codecs belong to the same generated Go package.
import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

func bashPPLocalCodecsGo(locals []bashPPLocalType) (string, error) {
	declarations := make(map[string]ast.Expr)
	for _, local := range locals {
		expr, err := parser.ParseExpr(local.Decl)
		if err != nil {
			return "", fmt.Errorf("local codec %s: %w", local.Name, err)
		}
		declarations[local.Name] = expr
	}
	render := func(expr ast.Expr) string {
		var b bytes.Buffer
		_ = format.Node(&b, token.NewFileSet(), expr)
		return b.String()
	}
	shapes := make(map[string]*ast.StructType)
	var underlying func(ast.Expr, map[string]bool) *ast.StructType
	underlying = func(expr ast.Expr, seen map[string]bool) *ast.StructType {
		switch e := expr.(type) {
		case *ast.StructType:
			return e
		case *ast.Ident:
			if seen[e.Name] {
				return nil
			}
			seen[e.Name] = true
			return underlying(declarations[e.Name], seen)
		}
		return nil
	}
	for _, local := range locals {
		expr := declarations[local.Name]
		if shape := underlying(expr, map[string]bool{}); shape != nil {
			shapes[local.Name] = shape
		}
		ast.Inspect(expr, func(node ast.Node) bool {
			if shape, ok := node.(*ast.StructType); ok {
				shapes[render(shape)] = shape
			}
			return true
		})
	}
	names := make([]string, 0, len(shapes))
	for name := range shapes {
		names = append(names, name)
	}
	sort.Strings(names)
	// Local variables must not shadow an original type referenced by a codec.
	prefix := "bppCodec"
	for {
		collision := false
		for name := range declarations {
			if strings.HasPrefix(name, prefix) {
				collision = true
				break
			}
		}
		if !collision {
			break
		}
		prefix += "_"
	}
	var b strings.Builder
	for _, name := range names {
		shape := shapes[name]
		fmt.Fprintf(&b, "func init() {\nlocalStructCodecs[reflect.TypeFor[%s]()] = localStructCodec{\n", name)
		fmt.Fprintf(&b, "decode: func(%sWire value) (reflect.Value,error) {\nvar %sDst %s\nfor %sName,%sField := range %sWire.Fields { _ = %sField; switch %sName {\n", prefix, prefix, name, prefix, prefix, prefix, prefix, prefix)
		for _, field := range shape.Fields.List {
			for _, id := range field.Names {
				if id.Name == "_" {
					continue
				}
				fmt.Fprintf(&b, "case %q: %sItem,%sErr := decode(%sField,reflect.TypeOf(&%sDst.%s).Elem()); if %sErr != nil { return reflect.Value{},%sErr }; reflect.ValueOf(&%sDst.%s).Elem().Set(%sItem)\n", id.Name, prefix, prefix, prefix, prefix, id.Name, prefix, prefix, prefix, id.Name, prefix)
			}
		}
		fmt.Fprintf(&b, "default: return reflect.Value{},fmt.Errorf(\"unknown field %%s of %%T\",%sName,%sDst)\n} }; return reflect.ValueOf(%sDst),nil },\n", prefix, prefix, prefix)
		fmt.Fprintf(&b, "structural: func(%sRV reflect.Value) value {\n%sSrc := %sRV.Interface().(%s)\n_ = %sSrc\nreturn value{Kind:\"struct\",Type:typeID(%sRV.Type()),Fields:map[string]value{\n", prefix, prefix, prefix, name, prefix, prefix)
		for _, field := range shape.Fields.List {
			for _, id := range field.Names {
				if id.Name == "_" {
					continue
				}
				fmt.Fprintf(&b, "%q: structural(reflect.ValueOf(&%sSrc.%s).Elem()),\n", id.Name, prefix, id.Name)
			}
		}
		b.WriteString("}} },\n}\n")
		fmt.Fprintf(&b, "types[%q] = reflect.TypeFor[%s](); types[typeID(reflect.TypeFor[%s]())] = reflect.TypeFor[%s]()\n}\n", name, name, name, name)
	}
	return b.String(), nil
}
