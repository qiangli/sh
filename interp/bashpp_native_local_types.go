package interp

// Sprint: #118; Story: #54; Story-ID: c3a60493cde9
//
// Original locally declared named types are materialised in the dependency
// helper as real Go declarations, so an imported call such as
// fmt.Println(Vertex{1, 2}) receives a value with the original field names,
// field types and defined type name. The helper never contains an original
// method body: a local String/Error method is mirrored by a generated stub
// whose whole body asks the interpreter to run the original body.

import (
	"fmt"
	"sort"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// bashPPLocalMethod is one interface method the dependency must be able to
// invoke on a materialised local type. Only the fmt-facing Stringer and error
// methods are mirrored; the mirror never carries the original body.
type bashPPLocalMethod struct {
	Name    string
	Pointer bool
}

// bashPPLocalType is the transportable descriptor of one original named type.
// Decl is the generated Go type body; it is structural, so the dependency's
// reflect view has exactly the original field names and element types.
type bashPPLocalType struct {
	Name           string
	Decl           string
	Methods        []bashPPLocalMethod
	OmittedMethods []string
}

// bashPPHelperReserved are identifiers the fixed helper template already
// defines. An original type using one of these names is left unregistered
// rather than silently renamed, so the failure stays honest.
var bashPPHelperReserved = map[string]bool{
	"value": true, "entry": true, "request": true, "response": true,
	"originalCallbackPanic": true, "originalPointers": true, "symbols": true, "types": true, "handles": true, "callbacks": true,
	"localTypeKey": true, "localStructCodec": true, "localStructCodecs": true,
	"outbound": true, "failure": true, "encode": true, "structural": true,
	"decode": true, "resolveType": true, "typeID": true, "access": true,
	"dispatch": true, "main": true, "callback": true, "callbackFailed": true,
	"takeFailure": true, "bridgeAddress": true, "bridgeAuth": true,
}

// bashPPLocalScalarTypes are the predeclared names the helper's base type
// registry already resolves, so a named type over one of them renders as-is.
var bashPPLocalScalarTypes = map[string]bool{
	"bool": true, "string": true, "error": true, "any": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "float32": true, "float64": true, "rune": true, "byte": true,
}

// bashPPLocalTypeDescriptors renders every original named type the helper can
// mirror faithfully. The set is read from the loaded program rather than from
// declarations executed so far, because the dependency session starts as soon
// as the imports do — before the original type declarations run — and its
// materialised type namespace is fixed for the life of that session.
//
// A type whose shape cannot be expressed without guessing — a channel, a func
// field, a generic instantiation, an imported element type — is omitted, and
// the dependency then reports it as an unregistered bridge type instead of
// accepting a fabricated stand-in.
func (r *Runner) bashPPLocalTypeDescriptors() []bashPPLocalType {
	if !r.bashPPGoSource || r.bashPPGoSourceFile == nil {
		return nil
	}
	declared := map[string]syntax.BashPPTypeExpr{}
	methods := map[string][]*syntax.BashPPFuncDecl{}
	for _, stmt := range r.bashPPGoSourceFile.Stmts {
		switch d := stmt.Cmd.(type) {
		case *syntax.BashPPDecl:
			if d.Site == syntax.StartTypeDecl && !d.Alias && len(d.TypeParams) == 0 && d.DeclTypeExpr != nil {
				declared[d.Name.Value] = d.DeclTypeExpr
			}
		case *syntax.BashPPFuncDecl:
			if d.Receiver != nil && d.Receiver.RecvType != nil {
				owner := d.Receiver.RecvType.Value
				methods[owner] = append(methods[owner], d)
			}
		}
	}
	names := make([]string, 0, len(declared))
	for name := range declared {
		// Go lets a program redefine a predeclared name. The helper's base
		// registry resolves those names to the predeclared types, so nothing
		// here would be faithful; materialise nothing rather than a namespace
		// that silently means something else.
		if bashPPLocalScalarTypes[name] {
			return nil
		}
		names = append(names, name)
	}
	sort.Strings(names)
	local := &bashPPLocalTypeSet{declared: declared}
	var out []bashPPLocalType
	for _, name := range names {
		if bashPPHelperReserved[name] {
			continue
		}
		decl, ok := local.source(declared[name], 0)
		if !ok {
			continue
		}
		materialised := bashPPLocalType{Name: name, Decl: decl}
		// A defined interface type cannot carry a method declaration, so its
		// implementations are mirrored instead — the dynamic value is what
		// crosses the boundary.
		if decl != "any" && !strings.HasPrefix(decl, "interface") {
			materialised.Methods = local.mirrored(methods[name])
			for _, method := range methods[name] {
				mirrored := false
				for _, m := range materialised.Methods {
					if m.Name == method.Name.Value {
						mirrored = true
					}
				}
				if !mirrored {
					materialised.OmittedMethods = append(materialised.OmittedMethods, method.Name.Value)
				}
			}
		}
		out = append(out, materialised)
	}
	return out
}

// bashPPLocalTypeSet is the original program's own type namespace as the
// helper will materialise it.
type bashPPLocalTypeSet struct {
	declared map[string]syntax.BashPPTypeExpr
}

// mirrored reports the method set the helper stubs out. Only String and Error
// are mirrored: they are what the fmt verbs consult, and each is answered by a
// callback into the interpreter rather than by compiled original code.
func (l *bashPPLocalTypeSet) mirrored(decls []*syntax.BashPPFuncDecl) []bashPPLocalMethod {
	var methods []bashPPLocalMethod
	for _, want := range []string{"Error", "String"} {
		for _, decl := range decls {
			if decl.Name == nil || decl.Name.Value != want {
				continue
			}
			if len(decl.TypeParams) > 0 || len(decl.Receiver.TypeParams) > 0 || len(decl.Params) > 0 {
				continue
			}
			if len(decl.Results) != 1 || len(decl.Results[0].Names) > 1 {
				continue
			}
			if text, ok := l.source(decl.Results[0].FieldTypeExpr, 0); !ok || text != "string" {
				continue
			}
			methods = append(methods, bashPPLocalMethod{Name: want, Pointer: decl.Receiver.Pointer})
			break
		}
	}
	return methods
}

// bashPPLocalTypeSource renders one original type expression as helper Go
// source. It reports false for any shape the helper cannot reproduce exactly.
func (l *bashPPLocalTypeSet) source(typ syntax.BashPPTypeExpr, depth int) (string, bool) {
	if typ == nil || depth > 16 {
		return "", false
	}
	switch t := typ.(type) {
	case *syntax.BashPPNamedType:
		if len(t.TypeArgs) > 0 {
			return "", false
		}
		name := t.Name.Value
		if bashPPLocalScalarTypes[name] {
			return name, true
		}
		if name == "interface{}" {
			return "any", true
		}
		if _, local := l.declared[name]; local && !bashPPHelperReserved[name] {
			return name, true
		}
		return "", false
	case *syntax.BashPPPointerType:
		element, ok := l.source(t.Element, depth+1)
		if !ok {
			return "", false
		}
		return "*" + element, true
	case *syntax.BashPPCollectionType:
		element, ok := l.source(t.Element, depth+1)
		if !ok {
			return "", false
		}
		switch t.Kind {
		case "slice":
			return "[]" + element, true
		case "array":
			if t.Length == nil {
				return "", false
			}
			return "[" + t.Length.Value + "]" + element, true
		case "map":
			key, ok := l.source(t.Key, depth+1)
			if !ok {
				return "", false
			}
			return "map[" + key + "]" + element, true
		}
		return "", false
	case *syntax.BashPPStructType:
		var fields []string
		for _, field := range t.Fields {
			if field.Embedded || len(field.Names) == 0 {
				return "", false
			}
			element, ok := l.source(field.FieldTypeExpr, depth+1)
			if !ok {
				return "", false
			}
			names := make([]string, len(field.Names))
			for i, name := range field.Names {
				names[i] = name.Value
			}
			if field.Tag != nil {
				element += " " + field.Tag.Value
			}
			fields = append(fields, strings.Join(names, ", ")+" "+element)
		}
		return "struct {\n" + strings.Join(fields, "\n") + "\n}", true
	case *syntax.BashPPInterfaceType:
		if len(t.Elems) > 0 {
			return "", false
		}
		if len(t.Methods) == 0 {
			return "any", true
		}
		var specs []string
		for _, spec := range t.Methods {
			text, ok := l.signature(spec, depth+1)
			if !ok {
				return "", false
			}
			specs = append(specs, spec.Name.Value+text)
		}
		return "interface {\n" + strings.Join(specs, "\n") + "\n}", true
	}
	return "", false
}

// signature renders one interface method specification.
func (l *bashPPLocalTypeSet) signature(spec *syntax.BashPPMethodSpec, depth int) (string, bool) {
	render := func(fields []*syntax.BashPPField) ([]string, bool) {
		var out []string
		for _, field := range fields {
			text, ok := l.source(field.FieldTypeExpr, depth+1)
			if !ok {
				return nil, false
			}
			count := len(field.Names)
			if count == 0 {
				count = 1
			}
			for range count {
				out = append(out, text)
			}
		}
		return out, true
	}
	params, ok := render(spec.Params)
	if !ok {
		return "", false
	}
	results, ok := render(spec.Results)
	if !ok {
		return "", false
	}
	text := "(" + strings.Join(params, ", ") + ")"
	switch len(results) {
	case 0:
	case 1:
		text += " " + results[0]
	default:
		text += " (" + strings.Join(results, ", ") + ")"
	}
	return text, true
}

// bashPPLocalTypeGo emits the helper declarations for one descriptor: the type
// itself, and one stub per mirrored method. The stub body is fixed generated
// protocol code; the original body stays interpreted on the other side of the
// callback.
func bashPPLocalTypeGo(local bashPPLocalType) string {
	var b strings.Builder
	fmt.Fprintf(&b, "type %s %s\n", local.Name, local.Decl)
	for _, method := range local.Methods {
		receiver := local.Name
		if method.Pointer {
			receiver = "*" + local.Name
		}
		fmt.Fprintf(&b, `func (bpprecv %s) %s() string {
 out, err := callback(%q, structural(reflect.ValueOf(bpprecv)))
 if err != nil { return callbackFailed(err) }
 if len(out) != 1 || out[0].Kind != "string" { return callbackFailed(fmt.Errorf("original %s.%s did not answer one string")) }
 return out[0].Text
}
`, receiver, method.Name, local.Name+"."+method.Name, local.Name, method.Name)
	}
	return b.String()
}

// bashPPLocalTypeIdentity is the comparison key that decides whether a running
// dependency session already materialises the current local type namespace.
func bashPPLocalTypeIdentity(locals []bashPPLocalType) string {
	var b strings.Builder
	for _, local := range locals {
		fmt.Fprintf(&b, "%s|%s|", local.Name, local.Decl)
		for _, method := range local.Methods {
			fmt.Fprintf(&b, "%s:%t,", method.Name, method.Pointer)
		}
		fmt.Fprintf(&b, "%v;", local.OmittedMethods)
	}
	return b.String()
}
