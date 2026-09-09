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
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// bashPPLocalMethod is one interface method the dependency must be able to
// invoke on a materialised local type. String/Error, exact Read([]byte) (int, error)
// and the image.Image method set are mirrored; the mirror never carries the
// original body.
//
// Params and Results are the helper Go source spellings of the reviewed
// signature, already flattened one entry per value. They are empty for the
// fixed String/Error and Read protocol stubs, which have their own hand-written
// mirrors; a method carrying them is emitted by the generalised stub instead.
type bashPPLocalMethod struct {
	Name              string
	Pointer           bool
	ReaderLocalBuffer bool
	Params            []string
	Results           []string
}

// bashPPLocalType is the transportable descriptor of one original named type.
// Decl is the generated Go type body; it is structural, so the dependency's
// reflect view has exactly the original field names and element types.
type bashPPLocalType struct {
	Name           string
	Alias          bool
	WireType       string
	Decl           string
	Methods        []bashPPLocalMethod
	OmittedMethods []string
}

// bashPPHelperReserved are identifiers the fixed helper template already
// defines. An original type using one of these names is left unregistered
// rather than silently renamed, so the failure stays honest.
var bashPPHelperReserved = map[string]bool{
	"value": true, "bppProtocolEntry": true, "request": true, "response": true,
	"originalCallbackPanic": true, "originalPointers": true, "symbols": true, "types": true, "handles": true, "callbacks": true,
	"bppTextTemplate": true, "bppTemplateParse": true, "primitiveTemplateTree": true,
	"sliceBuffer": true, "localTypeKey": true, "localStructCodec": true, "localStructCodecs": true,
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
// Package declarations, aliases, uniquely named local declarations, and
// anonymous shapes retain their actual Go identity. A reused local spelling
// is omitted: the current runtime has no lexical type namespace, so registering
// either declaration would conflate distinct Go types. Shapes which cannot be
// expressed faithfully remain unregistered.
func (r *Runner) bashPPLocalTypeDescriptors() []bashPPLocalType {
	if !r.bashPPGoSource || r.bashPPGoSourceFile == nil {
		return nil
	}
	declared := map[string]syntax.BashPPTypeExpr{}
	methods := map[string][]*syntax.BashPPFuncDecl{}
	aliases := map[string]bool{}
	ambiguous := map[string]bool{}
	var anonymous []*syntax.BashPPStructType
	syntax.Walk(r.bashPPGoSourceFile, func(node syntax.Node) bool {
		if d, ok := node.(*syntax.BashPPDecl); ok && d.Site == syntax.StartTypeDecl && len(d.TypeParams) == 0 && d.DeclTypeExpr != nil {
			name := d.Name.Value
			if _, exists := declared[name]; exists {
				ambiguous[name] = true
			}
			declared[name] = d.DeclTypeExpr
			aliases[name] = d.Alias
		}
		if shape, ok := node.(*syntax.BashPPStructType); ok {
			anonymous = append(anonymous, shape)
		}
		return true
	})
	// A name used in two scopes denotes distinct Go types even when their
	// fields are identical. Do not let either name escape through the helper.
	for name := range ambiguous {
		delete(declared, name)
	}
	for _, stmt := range r.bashPPGoSourceFile.Stmts {
		switch d := stmt.Cmd.(type) {
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
	local := &bashPPLocalTypeSet{declared: declared, imports: r.bashPPImports}
	var out []bashPPLocalType
	for _, name := range names {
		if bashPPHelperReserved[name] {
			continue
		}
		decl, ok := local.source(declared[name], 0)
		if !ok {
			continue
		}
		materialised := bashPPLocalType{Name: name, Decl: decl, Alias: aliases[name]}
		// A defined interface type cannot carry a method declaration, so its
		// implementations are mirrored instead — the dynamic value is what
		// crosses the boundary.
		if !materialised.Alias && decl != "any" && !strings.HasPrefix(decl, "interface") {
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
	// Anonymous shapes keep their Go identity through aliases, never invented
	// defined types. Existing typed codecs provide legal private field access.
	shapes := map[string]*syntax.BashPPStructType{}
	for _, shape := range anonymous {
		shapes[bashPPBridgeTypeText(shape)] = shape
	}
	keys := make([]string, 0, len(shapes))
	for key := range shapes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		decl, ok := local.source(shapes[key], 0)
		if !ok {
			continue
		}
		name := fmt.Sprintf("bppAnonymous_%x", sha256.Sum256([]byte(key)))
		for declared[name] != nil || bashPPHelperReserved[name] {
			name += "_"
		}
		out = append(out, bashPPLocalType{Name: name, Decl: decl, Alias: true, WireType: key})
	}
	return out
}

// bashPPLocalTypeSet is the original program's own type namespace as the
// helper will materialise it.
type bashPPLocalTypeSet struct {
	declared map[string]syntax.BashPPTypeExpr
	imports  map[string]string
}

// mirrored reports the method set the helper stubs out. String/Error and Read
// are mirrored by fixed protocol stubs; each original body runs in the interpreter.
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
	methods = append(methods, l.mirroredImage(decls)...)
	for _, decl := range decls {
		if decl.Name == nil || decl.Name.Value != "Read" || len(decl.TypeParams) > 0 || len(decl.Receiver.TypeParams) > 0 || len(decl.Params) != 1 || len(decl.Params[0].Names) > 1 || decl.Params[0].Variadic() || len(decl.Results) != 2 {
			continue
		}
		param, ok := l.source(decl.Params[0].FieldTypeExpr, 0)
		if !ok || (param != "[]byte" && param != "[]uint8") {
			continue
		}
		a, ok := l.source(decl.Results[0].FieldTypeExpr, 0)
		if !ok || a != "int" || len(decl.Results[0].Names) > 1 {
			continue
		}
		b, ok := l.source(decl.Results[1].FieldTypeExpr, 0)
		if !ok || b != "error" || len(decl.Results[1].Names) > 1 {
			continue
		}
		methods = append(methods, bashPPLocalMethod{Name: "Read", Pointer: decl.Receiver.Pointer, ReaderLocalBuffer: bashPPReaderLocalBufferProof(decl)})
		break
	}
	return methods
}

// importedType reports whether typ names exactly the symbol path.name through
// one of the original program's own imports. The alias is whatever the original
// wrote; the dependency path is what is actually checked.
func (l *bashPPLocalTypeSet) importedType(typ syntax.BashPPTypeExpr, path, name string) bool {
	named, ok := typ.(*syntax.BashPPNamedType)
	if !ok || named.Name == nil || len(named.TypeArgs) > 0 {
		return false
	}
	alias, symbol, ok := strings.Cut(named.Name.Value, ".")
	return ok && symbol == name && l.imports[alias] == path
}

// mirroredImage reports the image.Image method set, or nothing. The Go Tour
// image exercise hands an original value to pic.ShowImage and image/png then
// drives ColorModel, Bounds and At from dependency code, so all three must
// cross together: a partial mirror would present itself to the dependency as an
// image.Image it cannot actually serve. Every signature is matched exactly —
// ColorModel() color.Model, Bounds() image.Rectangle, At(x, y int) color.Color —
// and each original body still runs in the interpreter.
func (l *bashPPLocalTypeSet) mirroredImage(decls []*syntax.BashPPFuncDecl) []bashPPLocalMethod {
	found := map[string]*syntax.BashPPFuncDecl{}
	for _, decl := range decls {
		if decl.Name == nil || len(decl.TypeParams) > 0 || len(decl.Receiver.TypeParams) > 0 {
			continue
		}
		switch decl.Name.Value {
		case "ColorModel", "Bounds", "At":
			if found[decl.Name.Value] != nil {
				return nil
			}
			found[decl.Name.Value] = decl
		}
	}
	if len(found) != 3 {
		return nil
	}
	// One unnamed-or-singly-named result of exactly the reviewed imported type.
	result := func(decl *syntax.BashPPFuncDecl, path, name string) (string, bool) {
		if len(decl.Results) != 1 || len(decl.Results[0].Names) > 1 {
			return "", false
		}
		if !l.importedType(decl.Results[0].FieldTypeExpr, path, name) {
			return "", false
		}
		return l.source(decl.Results[0].FieldTypeExpr, 0)
	}
	colorModel, ok := result(found["ColorModel"], "image/color", "Model")
	if !ok || len(found["ColorModel"].Params) != 0 {
		return nil
	}
	rectangle, ok := result(found["Bounds"], "image", "Rectangle")
	if !ok || len(found["Bounds"].Params) != 0 {
		return nil
	}
	colorValue, ok := result(found["At"], "image/color", "Color")
	if !ok {
		return nil
	}
	// At takes exactly two ints, however the original spelled them: `x, y int`
	// is one field with two names, `x int, y int` is two fields with one each.
	coordinates := 0
	for _, param := range found["At"].Params {
		if param.Variadic() {
			return nil
		}
		if text, ok := l.source(param.FieldTypeExpr, 0); !ok || text != "int" {
			return nil
		}
		coordinates += max(len(param.Names), 1)
	}
	if coordinates != 2 {
		return nil
	}
	return []bashPPLocalMethod{
		{Name: "At", Pointer: found["At"].Receiver.Pointer, Params: []string{"int", "int"}, Results: []string{colorValue}},
		{Name: "Bounds", Pointer: found["Bounds"].Receiver.Pointer, Results: []string{rectangle}},
		{Name: "ColorModel", Pointer: found["ColorModel"].Receiver.Pointer, Results: []string{colorModel}},
	}
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
		if alias, symbol, ok := strings.Cut(name, "."); ok && l.imports[alias] != "" && symbol != "" {
			return name, true
		}
		return "", false
	case *syntax.BashPPPointerType:
		element, ok := l.source(t.Element, depth+1)
		if !ok {
			return "", false
		}
		return "*" + element, true
	case *syntax.BashPPChanType:
		element, ok := l.source(t.Element, depth+1)
		if !ok {
			return "", false
		}
		prefix := "chan "
		if t.Direction == "recv" {
			prefix = "<-chan "
		}
		if t.Direction == "send" {
			prefix = "chan<- "
		}
		return prefix + element, true
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
	alias := ""
	if local.Alias {
		alias = "= "
	}
	fmt.Fprintf(&b, "type %s %s%s\n", local.Name, alias, local.Decl)
	for _, method := range local.Methods {
		receiver := local.Name
		if method.Pointer {
			receiver = "*" + local.Name
		}
		if len(method.Params) > 0 || len(method.Results) > 0 {
			b.WriteString(bashPPLocalMethodGo(local.Name, receiver, method))
			continue
		}
		if method.Name == "Read" {
			fmt.Fprintf(&b, `func (bpprecv %s) Read(p []byte)(int,error) {
 recv:=structural(reflect.ValueOf(bpprecv));recv.CallArgs=[]value{encode(reflect.ValueOf(p))}
 if %t { recv.CallArgs=append(recv.CallArgs,value{Kind:"reader-buffer",ReaderBuffer:append([]byte(nil),p[:cap(p)]...),ReaderLength:len(p)}) }
 out,err:=callback(%q,recv);if err!=nil{panic(err)}
 if len(out)==3 { if out[2].Kind!="reader-buffer" || len(out[2].ReaderBuffer)!=cap(p){panic(fmt.Errorf("original Read buffer writeback mismatch"))};copy(p[:cap(p)],out[2].ReaderBuffer);out=out[:2] }
 if len(out)!=2{panic(fmt.Errorf("original Read result count mismatch"))}
 count,err:=decode(out[0],reflect.TypeFor[int]());if err!=nil{panic(err)}
 failure,err:=decode(out[1],reflect.TypeFor[error]());if err!=nil{panic(err)}
 var readErr error;if failure.IsValid(){readErr,_=failure.Interface().(error)}
 return int(count.Int()),readErr
}
`, receiver, method.ReaderLocalBuffer, local.Name+".Read")
			continue
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

// bashPPLocalMethodGo emits the generalised transport stub for one mirrored
// method with typed parameters and typed results. The whole body is generated
// protocol: arguments are encoded onto the callback request, the interpreter
// runs the original body, and each result is decoded back at its declared type
// so a native handle stays a native handle. No original statement is compiled.
func bashPPLocalMethodGo(typeName, receiver string, method bashPPLocalMethod) string {
	var b strings.Builder
	params := make([]string, len(method.Params))
	encoded := make([]string, len(method.Params))
	for i, typ := range method.Params {
		params[i] = fmt.Sprintf("bpparg%d %s", i, typ)
		encoded[i] = fmt.Sprintf("encode(reflect.ValueOf(bpparg%d))", i)
	}
	results := strings.Join(method.Results, ", ")
	if len(method.Results) > 1 {
		results = "(" + results + ")"
	}
	if results != "" {
		results = " " + results
	}
	selector := typeName + "." + method.Name
	fmt.Fprintf(&b, "func (bpprecv %s) %s(%s)%s {\n", receiver, method.Name, strings.Join(params, ", "), results)
	b.WriteString(" recv:=structural(reflect.ValueOf(bpprecv))\n")
	if len(encoded) > 0 {
		fmt.Fprintf(&b, " recv.CallArgs=[]value{%s}\n", strings.Join(encoded, ","))
	}
	fmt.Fprintf(&b, " out,err:=callback(%q,recv);if err!=nil{panic(err)}\n", selector)
	fmt.Fprintf(&b, " if len(out)!=%d{panic(fmt.Errorf(%q))}\n", len(method.Results),
		fmt.Sprintf("original %s result count mismatch", selector))
	names := make([]string, len(method.Results))
	for i, typ := range method.Results {
		names[i] = fmt.Sprintf("bppres%d", i)
		fmt.Fprintf(&b, " bppval%d,err:=decode(out[%d],reflect.TypeFor[%s]());if err!=nil{panic(err)}\n", i, i, typ)
		fmt.Fprintf(&b, " var bppres%d %s;if bppval%d.IsValid(){bppres%d,_=bppval%d.Interface().(%s)}\n", i, typ, i, i, i, typ)
	}
	if len(names) > 0 {
		fmt.Fprintf(&b, " return %s\n", strings.Join(names, ", "))
	}
	b.WriteString("}\n")
	return b.String()
}

// bashPPLocalTypeIdentity is the comparison key that decides whether a running
// dependency session already materialises the current local type namespace.
func bashPPLocalTypeIdentity(locals []bashPPLocalType) string {
	var b strings.Builder
	for _, local := range locals {
		fmt.Fprintf(&b, "%s|%s|%t|%s|", local.Name, local.Decl, local.Alias, local.WireType)
		for _, method := range local.Methods {
			fmt.Fprintf(&b, "%s:%t:%t:%v:%v,", method.Name, method.Pointer, method.ReaderLocalBuffer, method.Params, method.Results)
		}
		fmt.Fprintf(&b, "%v;", local.OmittedMethods)
	}
	return b.String()
}
