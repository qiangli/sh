package gosource

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/format"
	"go/parser"
	"go/scanner"
	"go/token"
	"go/types"
	"regexp"
	"strconv"
	"strings"

	s "mvdan.cc/sh/v3/syntax"
)

type converter struct {
	packagePath   string
	importAliases map[string]string
	// mapped maps each explicit package path linked into the same file to
	// its map index. A selector on one of their import bindings collapses
	// to the selected object's rename, and their package-level names are
	// spelled by that rename everywhere (see mangledName); the program's
	// own names are never renamed.
	mapped map[string]int
	// mappedPkgs lists the linked packages by map index, so a type string's
	// marker qualifier can be resolved back to the named object.
	mappedPkgs []*types.Package
	// resolveImport applies the relative-import rule to an import path as
	// written, so an import without a binding (blank) can still be matched
	// against mapped.
	resolveImport  func(string) (string, error)
	syntheticPos   token.Pos
	prefix         string
	fset           *token.FileSet
	files          []*ast.File
	sources        []Source
	info           *types.Info
	renames        map[types.Object]string
	err            error
	branchScopes   []converterBranchScope
	statementLabel string
}

type converterBranchScope struct {
	label string
	loop  bool
}

func (c *converter) pushBranchScope(label string, loop bool) func() {
	c.branchScopes = append(c.branchScopes, converterBranchScope{label: label, loop: loop})
	return func() { c.branchScopes = c.branchScopes[:len(c.branchScopes)-1] }
}

func (c *converter) takeStatementLabel() string {
	label := c.statementLabel
	c.statementLabel = ""
	return label
}

func (c *converter) labeledBranchDepth(branch *ast.BranchStmt) int {
	depth := 0
	for i := len(c.branchScopes) - 1; i >= 0; i-- {
		scope := c.branchScopes[i]
		if branch.Tok == token.CONTINUE && !scope.loop {
			continue
		}
		depth++
		if scope.label == branch.Label.Name {
			return depth
		}
	}
	c.fail(branch, "labeled branch")
	return 0
}

func (c *converter) pos(p token.Pos) s.Pos {
	if c.syntheticPos.IsValid() && p.IsValid() {
		p = c.syntheticPos
	}
	if !p.IsValid() {
		return s.Pos{}
	}
	v := c.fset.Position(p)
	return s.NewPos(uint(p-1), uint(v.Line), uint(v.Column))
}
func (c *converter) fail(n ast.Node, what string) {
	if c.err == nil {
		p := n.Pos()
		if c.syntheticPos.IsValid() {
			p = c.syntheticPos
		}
		c.err = fmt.Errorf("%s: gosource: unsupported %s", c.fset.Position(p), what)
	}
}
func (c *converter) lit(p token.Pos, v string) *s.Lit {
	return &s.Lit{Value: v, ValuePos: c.pos(p), ValueEnd: c.pos(p + token.Pos(len(v)))}
}
func (c *converter) ident(n *ast.Ident) *s.Lit {
	v := n.Name
	obj := c.info.ObjectOf(n)
	if rename := c.renames[obj]; rename != "" {
		v = rename
	}
	out := c.lit(n.Pos(), v)
	out.ValueEnd = c.pos(n.End())
	return out
}

// mappedPkgName reports whether e is the import binding of a linked explicit
// package. A selector through it collapses to the selected object's rename:
// the package's declarations were lowered into the same flat file.
func (c *converter) mappedPkgName(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	if !ok || len(c.mapped) == 0 {
		return false
	}
	pkgname, ok := c.info.ObjectOf(id).(*types.PkgName)
	if !ok {
		return false
	}
	_, mapped := c.mapped[pkgname.Imported().Path()]
	return mapped
}

// mappedMarker is the qualifier a type string spells a mapped package by:
// the hygiene prefix, which is free in every linked source, and the map
// index. typeString rewrites "<marker>.Name" into the object's rename.
func (c *converter) mappedMarker(index int) string {
	return fmt.Sprintf("%spkg_%d", c.prefix, index)
}

// mangledName is the flat-file spelling of a mapped package's package-level
// object: the marker followed by the declared name.
func (c *converter) mangledName(index int, name string) string {
	return c.mappedMarker(index) + "_" + name
}

// qualifier spells a package in a type string: unqualified for the program
// package, by marker for a linked explicit package, by hoisted alias for an
// import, and by declared name otherwise. Only typeString may use it, since
// the marker must be rewritten before the text is read.
func (c *converter) qualifier(p *types.Package) string {
	if index, ok := c.mapped[p.Path()]; ok {
		return c.mappedMarker(index)
	}
	if p.Path() == c.packagePath {
		return ""
	}
	if alias := c.importAliases[p.Path()]; alias != "" {
		return alias
	}
	return p.Name()
}

// typeString spells a checked type for the flat file. types.TypeString can
// qualify a package but not rename an object, so a mapped package's names
// come out as "<marker>.Name" and are rewritten here: a package-level name
// becomes its rename, and any other (a function-local type, which the
// checker qualifies by package too) stays bare, as it is declared. A local
// type spelled like a package-level one of the same package takes the
// latter's rename; the runtime has no lexical type namespace either.
func (c *converter) typeString(t types.Type) string {
	text := types.TypeString(t, c.qualifier)
	if len(c.mapped) == 0 || !strings.Contains(text, c.prefix+"pkg_") {
		return text
	}
	pattern := regexp.MustCompile(regexp.QuoteMeta(c.prefix+"pkg_") + `([0-9]+)\.([\pL\pN_]+)`)
	return pattern.ReplaceAllStringFunc(text, func(match string) string {
		sub := pattern.FindStringSubmatch(match)
		index, _ := strconv.Atoi(sub[1])
		if obj := c.mappedPkgs[index].Scope().Lookup(sub[2]); obj != nil {
			if rename := c.renames[obj]; rename != "" {
				return rename
			}
		}
		return sub[2]
	})
}
func (c *converter) text(n ast.Node) string {
	var b bytes.Buffer
	original := map[*ast.Ident]string{}
	// A mapped package binding is spelled as a marker the hygiene prefix
	// guarantees absent from the source, then dropped with its dot.
	marker := c.prefix + "mapped"
	ast.Inspect(n, func(node ast.Node) bool {
		if sel, ok := node.(*ast.SelectorExpr); ok && c.mappedPkgName(sel.X) {
			id := sel.X.(*ast.Ident)
			original[id] = id.Name
			id.Name = marker
			return true
		}
		if id, ok := node.(*ast.Ident); ok {
			if name := c.renames[c.info.ObjectOf(id)]; name != "" && original[id] == "" {
				original[id] = id.Name
				id.Name = name
			}
		}
		return true
	})
	defer func() {
		for id, name := range original {
			id.Name = name
		}
	}()
	_ = format.Node(&b, c.fset, n)
	if len(c.mapped) == 0 {
		return b.String()
	}
	return strings.ReplaceAll(b.String(), marker+".", "")
}
func (c *converter) word(n ast.Expr) *s.Word {
	if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			c.fail(n, "string literal")
		}
		return &s.Word{Parts: []s.WordPart{&s.SglQuoted{Left: c.pos(n.Pos()), Right: c.pos(n.End() - 1), Value: value}}}
	}
	// Preserve Go quotes as literal data. Legacy runtime consumers parse these
	// words as Go operands, never through a shell parser.
	return &s.Word{Parts: []s.WordPart{&s.Lit{Value: c.text(n), ValuePos: c.pos(n.Pos()), ValueEnd: c.pos(n.End())}}}
}
func (c *converter) stmt(cmd s.Command) *s.Stmt {
	if cmd == nil {
		return nil
	}
	return &s.Stmt{Cmd: cmd, Position: cmd.Pos()}
}
func (c *converter) block(b *ast.BlockStmt) *s.Block {
	if b == nil {
		return nil
	}
	o := &s.Block{Lbrace: c.pos(b.Lbrace), Rbrace: c.pos(b.Rbrace)}
	for _, st := range b.List {
		o.Stmts = append(o.Stmts, c.statements(st)...)
	}
	return o
}

// isNewType reports whether x is a call to the predeclared new builtin with a
// type operand. Go 1.27 also allows new(v) over a value, whose argument is not
// convertible by typ; those calls stay on the generic call path.
func (c *converter) isNewType(x *ast.CallExpr) bool {
	id, ok := x.Fun.(*ast.Ident)
	if !ok || len(x.Args) != 1 {
		return false
	}
	obj, ok := c.info.Uses[id].(*types.Builtin)
	if !ok || obj.Name() != "new" {
		return false
	}
	return c.info.Types[x.Args[0]].IsType()
}

func (c *converter) isNewBuiltin(x *ast.CallExpr) bool {
	id, ok := x.Fun.(*ast.Ident)
	if !ok || len(x.Args) != 1 {
		return false
	}
	obj, ok := c.info.Uses[id].(*types.Builtin)
	return ok && obj.Name() == "new"
}

// valueType converts the go/types result of an expression back into the typed
// syntax representation. Go 1.27's new(v) needs both v and its defaulted type:
// the former initializes the fresh cell while the latter is the pointer's
// element identity. Positions are anchored at the original expression without
// inventing source text or reparsing the program.
func (c *converter) valueType(e ast.Expr) s.BashPPTypeExpr {
	typ := c.info.Types[e].Type
	if typ == nil {
		return nil
	}
	return c.checkedType(types.Default(typ), e, "inferred value type")
}

// checkedType converts a go/types type into the typed syntax representation,
// anchored at the source node it stands for. The checker's spelling of the
// type is reparsed rather than reconstructed so that every form typ already
// understands — instantiated named types, type parameters of the enclosing
// declaration, function and interface literals — comes out the same way it
// would have from the source.
func (c *converter) checkedType(typ types.Type, at ast.Node, what string) s.BashPPTypeExpr {
	typeName := c.typeString(typ)
	if typeName == "any" && types.Identical(typ, types.Universe.Lookup("any").Type()) {
		typeName = "interface{}"
	}
	parsed, err := parser.ParseExpr(typeName)
	if err != nil {
		c.fail(at, what)
		return nil
	}
	c.syntheticPos = at.Pos()
	result := c.typ(parsed)
	c.syntheticPos = token.NoPos
	return result
}

// instanceTypeArgs spells the type arguments the checker instantiated a
// generic function callee with: the full list whether the call wrote them
// all, wrote a prefix and left the rest to inference, or wrote none. The
// runtime then binds the callee's type parameters from the call itself and
// never has to infer them from argument values — go/types' inference, which
// accepted the program, is the only inference in the pipeline. Generic types
// are not callees (a conversion is lowered before reaching the call form),
// so only a function instance qualifies.
func (c *converter) instanceTypeArgs(fun ast.Expr) []*s.BashPPTypeArg {
	base := fun
	for {
		switch v := base.(type) {
		case *ast.ParenExpr:
			base = v.X
			continue
		case *ast.IndexExpr:
			base = v.X
			continue
		case *ast.IndexListExpr:
			base = v.X
			continue
		}
		break
	}
	var id *ast.Ident
	switch v := base.(type) {
	case *ast.Ident:
		id = v
	case *ast.SelectorExpr:
		id = v.Sel
	default:
		return nil
	}
	inst, ok := c.info.Instances[id]
	if !ok || inst.TypeArgs == nil || inst.TypeArgs.Len() == 0 {
		return nil
	}
	if _, ok := inst.Type.(*types.Signature); !ok {
		return nil
	}
	out := make([]*s.BashPPTypeArg, 0, inst.TypeArgs.Len())
	for i := range inst.TypeArgs.Len() {
		typ := c.checkedType(inst.TypeArgs.At(i), id, "instantiated type argument")
		if typ == nil {
			return nil
		}
		out = append(out, &s.BashPPTypeArg{ArgType: typ})
	}
	return out
}

func (c *converter) stringValue(e ast.Expr) bool {
	typ := c.info.TypeOf(e)
	if typ == nil {
		return false
	}
	basic, ok := typ.Underlying().(*types.Basic)
	return ok && (basic.Kind() == types.String || basic.Kind() == types.UntypedString)
}
func (c *converter) typ(e ast.Expr) s.BashPPTypeExpr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ast.Ident:
		if c.info.ObjectOf(x) == types.Universe.Lookup("any") {
			return &s.BashPPInterfaceType{Interface: c.ident(x), Lbrace: c.pos(x.End() - 1), Rbrace: c.pos(x.End() - 1)}
		}
		if object := c.info.ObjectOf(x); object != nil {
			if _, parameter := object.Type().(*types.TypeParam); parameter {
				return &s.BashPPTypeParamType{Name: c.ident(x)}
			}
		}
		return &s.BashPPNamedType{Name: c.ident(x)}
	case *ast.SelectorExpr:
		if c.mappedPkgName(x.X) {
			return &s.BashPPNamedType{Name: c.ident(x.Sel)}
		}
		return &s.BashPPNamedType{Name: c.lit(x.Pos(), c.ident(x.X.(*ast.Ident)).Value+"."+x.Sel.Name)}
	case *ast.StarExpr:
		return &s.BashPPPointerType{Star: c.pos(x.Star), Element: c.typ(x.X)}
	case *ast.ArrayType:
		o := &s.BashPPCollectionType{Start: c.pos(x.Pos()), Lbrack: c.pos(x.Lbrack), Rbrack: c.pos(x.Elt.Pos() - 1), Element: c.typ(x.Elt), Kind: "slice"}
		if x.Len != nil {
			o.Kind = "array"
			o.Length = c.lit(x.Len.Pos(), c.text(x.Len))
			if _, ok := x.Len.(*ast.Ellipsis); ok {
				o.Kind = "inferred-array"
			}
		}
		return o
	case *ast.MapType:
		return &s.BashPPCollectionType{Kind: "map", Start: c.pos(x.Map), Lbrack: c.pos(x.Map + 3), Rbrack: c.pos(x.Value.Pos() - 1), Key: c.typ(x.Key), Element: c.typ(x.Value)}
	case *ast.StructType:
		return &s.BashPPStructType{Struct: c.lit(x.Struct, "struct"), Lbrace: c.pos(x.Fields.Opening), Rbrace: c.pos(x.Fields.Closing), Fields: c.fields(x.Fields, true)}
	case *ast.ChanType:
		dir := ""
		if x.Dir == ast.SEND {
			dir = "send"
		}
		if x.Dir == ast.RECV {
			dir = "recv"
		}
		return &s.BashPPChanType{Chan: c.pos(x.Begin), Arrow: c.pos(x.Arrow), Direction: dir, Element: c.typ(x.Value), Elem: c.lit(x.Value.Pos(), c.text(x.Value))}
	case *ast.FuncType:
		return &s.BashPPFuncType{Func: c.pos(x.Func), Lparen: c.pos(x.Params.Opening), Rparen: c.pos(x.Params.Closing), Params: c.fields(x.Params, false), Results: c.fields(x.Results, false)}
	case *ast.InterfaceType:
		out := &s.BashPPInterfaceType{Interface: c.lit(x.Interface, "interface"), Lbrace: c.pos(x.Methods.Opening), Rbrace: c.pos(x.Methods.Closing)}
		for _, f := range x.Methods.List {
			if ft, ok := f.Type.(*ast.FuncType); ok && len(f.Names) > 0 {
				m := &s.BashPPMethodSpec{Name: c.ident(f.Names[0]), Params: c.fields(ft.Params, false), Results: c.fields(ft.Results, false), Lparen: c.pos(ft.Params.Opening), Rparen: c.pos(ft.Params.Closing)}
				out.Methods = append(out.Methods, m)
				out.Elems = append(out.Elems, &s.BashPPInterfaceElem{Method: m})
			} else {
				out.Elems = append(out.Elems, &s.BashPPInterfaceElem{Embedded: c.typ(f.Type)})
			}
		}
		return out
	case *ast.IndexExpr:
		if named, ok := c.typ(x.X).(*s.BashPPNamedType); ok {
			named.TypeArgs = []*s.BashPPTypeArg{{ArgType: c.typ(x.Index)}}
			return named
		}
	case *ast.IndexListExpr:
		if o, ok := c.typ(x.X).(*s.BashPPNamedType); ok {
			for _, a := range x.Indices {
				o.TypeArgs = append(o.TypeArgs, &s.BashPPTypeArg{ArgType: c.typ(a)})
			}
			return o
		}
	case *ast.UnaryExpr:
		if x.Op == token.TILDE {
			return &s.BashPPApproxType{Tilde: c.pos(x.OpPos), Term: c.typ(x.X)}
		}
	case *ast.BinaryExpr:
		if x.Op == token.OR {
			return &s.BashPPUnionType{Terms: []s.BashPPTypeExpr{c.typ(x.X), c.typ(x.Y)}, Bars: []*s.Lit{c.lit(x.OpPos, "|")}}
		}
	case *ast.ParenExpr:
		return c.typ(x.X)
	}
	c.fail(e, fmt.Sprintf("type %T", e))
	return &s.BashPPNamedType{Name: c.lit(e.Pos(), "invalid")}
}
func (c *converter) fields(list *ast.FieldList, structure bool) []*s.BashPPField {
	if list == nil {
		return nil
	}
	var out []*s.BashPPField
	for _, f := range list.List {
		t := f.Type
		v := &s.BashPPField{Embedded: structure && len(f.Names) == 0}
		if e, ok := t.(*ast.Ellipsis); ok {
			v.Ellipsis = c.pos(e.Ellipsis)
			t = e.Elt
		}
		v.FieldType = c.lit(t.Pos(), c.text(t))
		v.FieldTypeExpr = c.typ(t)
		if f.Tag != nil {
			v.Tag = c.lit(f.Tag.Pos(), f.Tag.Value)
		}
		for _, n := range f.Names {
			v.Names = append(v.Names, c.ident(n))
		}
		out = append(out, v)
	}
	return out
}
func (c *converter) typeParams(list *ast.FieldList) []*s.BashPPTypeParam {
	if list == nil {
		return nil
	}
	var out []*s.BashPPTypeParam
	for _, f := range list.List {
		v := &s.BashPPTypeParam{Constraint: c.typ(f.Type)}
		for _, n := range f.Names {
			v.Names = append(v.Names, c.ident(n))
		}
		out = append(out, v)
	}
	return out
}
func (c *converter) function(f *ast.FuncDecl) *s.BashPPFuncDecl {
	o := &s.BashPPFuncDecl{Kw: c.lit(f.Pos(), "func"), Name: c.ident(f.Name), Params: c.fields(f.Type.Params, false), Results: c.fields(f.Type.Results, false), TypeParams: c.typeParams(f.Type.TypeParams), Lparen: c.pos(f.Type.Params.Opening), Rparen: c.pos(f.Type.Params.Closing), Body: c.block(f.Body)}
	if f.Type.Results != nil {
		o.ResLparen = c.pos(f.Type.Results.Opening)
		o.ResRparen = c.pos(f.Type.Results.Closing)
	}
	if f.Recv != nil {
		r := f.Recv.List[0]
		t := r.Type
		recv := &s.BashPPReceiver{Lparen: c.pos(f.Recv.Opening), Rparen: c.pos(f.Recv.Closing)}
		if len(r.Names) > 0 {
			recv.Name = c.ident(r.Names[0])
		}
		if ptr, ok := t.(*ast.StarExpr); ok {
			recv.Pointer = true
			t = ptr.X
		}
		named, ok := c.typ(t).(*s.BashPPNamedType)
		if !ok {
			c.fail(t, fmt.Sprintf("method receiver type %T", t))
			recv.RecvType = c.lit(t.Pos(), c.text(t))
		} else {
			recv.RecvType = named.Name
			for _, arg := range named.TypeArgs {
				param, ok := arg.ArgType.(*s.BashPPTypeParamType)
				if !ok {
					c.fail(t, fmt.Sprintf("method receiver type argument %T", arg.ArgType))
					continue
				}
				recv.TypeParams = append(recv.TypeParams, param.Name)
			}
		}
		o.Receiver = recv
	}
	return o
}
func (c *converter) importSpec(g *ast.GenDecl, i *ast.ImportSpec) *s.BashPPImport {
	path, _ := strconv.Unquote(i.Path.Value)
	if c.importedPathIsMapped(i, path) {
		return nil
	}
	out := &s.BashPPImport{Site: s.StartImport, Class: s.ClassR, Kw: c.lit(g.TokPos, "import"), Path: &s.DblQuoted{Left: c.pos(i.Path.Pos()), Right: c.pos(i.Path.End() - 1), Parts: []s.WordPart{c.lit(i.Path.Pos()+1, path)}}}
	if i.Name != nil {
		out.Alias = c.ident(i.Name)
	} else if obj := c.info.Implicits[i]; c.renames[obj] != "" {
		out.Alias = c.lit(i.Path.Pos(), c.renames[obj])
	}
	return out
}

// importedPathIsMapped reports whether an import spec names a package in the
// explicit package map, through its binding when it has one and through the
// relative-import rule otherwise.
func (c *converter) importedPathIsMapped(i *ast.ImportSpec, path string) bool {
	if len(c.mapped) == 0 {
		return false
	}
	var obj types.Object
	if i.Name != nil {
		obj = c.info.Defs[i.Name]
	} else {
		obj = c.info.Implicits[i]
	}
	if pkgname, ok := obj.(*types.PkgName); ok {
		_, mapped := c.mapped[pkgname.Imported().Path()]
		return mapped
	}
	if c.resolveImport != nil {
		if resolved, err := c.resolveImport(path); err == nil {
			path = resolved
		}
	}
	_, mapped := c.mapped[path]
	return mapped
}
func (c *converter) typeDecl(g *ast.GenDecl, t *ast.TypeSpec) *s.BashPPDecl {
	out := &s.BashPPDecl{Site: s.StartTypeDecl, Kw: c.lit(g.TokPos, "type"), Name: c.ident(t.Name), DeclType: c.lit(t.Type.Pos(), c.text(t.Type)), DeclTypeExpr: c.typ(t.Type), Alias: t.Assign.IsValid(), TypeParams: c.typeParams(t.TypeParams), End_: c.pos(t.End())}
	if st, ok := out.DeclTypeExpr.(*s.BashPPStructType); ok {
		out.DeclType.Value = "struct"
		out.StructFields = st.Fields
		out.Lbrace = st.Lbrace
		out.Rbrace = st.Rbrace
	}
	return out
}
func (c *converter) valueDecl(g *ast.GenDecl, v *ast.ValueSpec, n *ast.Ident, index int) *s.BashPPDecl {
	out := &s.BashPPDecl{Kw: c.lit(g.TokPos, g.Tok.String()), Name: c.ident(n), Site: s.StartVar, End_: c.pos(v.End())}
	if v.Type == nil && g.Tok == token.VAR {
		if obj := c.info.Defs[n]; obj != nil {
			typeName := c.typeString(obj.Type())
			// The synthetic AST has no go/types object bindings. Expand the
			// predeclared any alias so it keeps its interface shape instead
			// of becoming an unresolved named type during initialization.
			if typeName == "any" && types.Identical(obj.Type(), types.Universe.Lookup("any").Type()) {
				typeName = "interface{}"
			}
			parsed, err := parser.ParseExpr(typeName)
			if err != nil {
				c.fail(n, "inferred variable type")
			} else {
				out.DeclType = c.lit(n.Pos(), typeName)
				c.syntheticPos = n.Pos()
				out.DeclTypeExpr = c.typ(parsed)
				c.syntheticPos = token.NoPos
			}
		}
	}
	if g.Tok == token.CONST {
		out.Site = s.StartConst
	}
	if v.Type != nil {
		out.DeclType = c.lit(v.Type.Pos(), c.text(v.Type))
		out.DeclTypeExpr = c.typ(v.Type)
	}
	if g.Tok == token.CONST {
		if obj, ok := c.info.Defs[n].(*types.Const); ok {
			value := obj.Val().ExactString()
			kind := "INT"
			switch obj.Val().Kind() {
			case constant.String:
				kind = "STRING"
			case constant.Bool:
				out.InitExpr = &s.BashPPIdent{Name: c.lit(n.Pos(), value)}
			case constant.Float:
				kind = "FLOAT"
				// ExactString may spell an integral float as an integer. Keep
				// floating syntax without any machine-float conversion.
				if !strings.Contains(value, "/") {
					value += ".0"
				}
				if parts := strings.Split(value, "/"); len(parts) == 2 {
					out.InitExpr = &s.BashPPBinaryExpr{X: &s.BashPPBasicLit{Kind: "FLOAT", Value: c.lit(n.Pos(), parts[0]+".0")}, Op: c.lit(n.Pos(), "/"), Y: &s.BashPPBasicLit{Kind: "FLOAT", Value: c.lit(n.Pos(), parts[1]+".0")}}
				}
			case constant.Complex:
				out.InitExpr = c.complexConstantExpr(n.Pos(), obj.Val())
			}
			if out.InitExpr == nil {
				out.InitExpr = &s.BashPPBasicLit{Kind: kind, Value: c.lit(n.Pos(), value)}
			}
			out.Init = []*s.Word{{Parts: []s.WordPart{c.lit(n.Pos(), value)}}}
		}
		return out
	}
	if len(v.Values) > 0 {
		if len(v.Values) != len(v.Names) {
			c.fail(v, "tuple variable declaration")
			return out
		}
		out.Init = []*s.Word{c.word(v.Values[index])}
		out.InitExpr = c.expr(v.Values[index])
	}
	return out
}
func (c *converter) expr(e ast.Expr) s.BashPPExpr {
	if e == nil {
		return nil
	}
	result := c.exprValue(e)
	// go/types records the concrete type only at a constant's contextual
	// conversion/defaulting boundary. Inner untyped operands remain exact.
	// Preserve that boundary, particularly float and rune defaults in any.
	if tv := c.info.Types[e]; tv.Value != nil {
		if basic, ok := tv.Type.(*types.Basic); ok && basic.Info()&types.IsUntyped == 0 {
			return &s.BashPPConvertExpr{ConvType: c.lit(e.Pos(), basic.Name()), Lparen: c.pos(e.Pos()), Rparen: c.pos(e.End() - 1), X: result}
		}
	}
	return result
}

func (c *converter) exprValue(e ast.Expr) s.BashPPExpr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ast.FuncLit:
		return c.funlit(x)
	case *ast.BasicLit:
		return &s.BashPPBasicLit{Kind: x.Kind.String(), Value: c.lit(x.ValuePos, x.Value)}
	case *ast.Ident:
		if value := c.genericFuncValue(x); value != nil {
			return value
		}
		return &s.BashPPIdent{Name: c.ident(x)}
	case *ast.ParenExpr:
		return &s.BashPPParenExpr{Lparen: c.pos(x.Lparen), Rparen: c.pos(x.Rparen), X: c.expr(x.X)}
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			return &s.BashPPAddressExpr{Amp: c.pos(x.OpPos), X: c.expr(x.X)}
		}
		return &s.BashPPUnaryExpr{Op: c.lit(x.OpPos, x.Op.String()), X: c.expr(x.X)}
	case *ast.StarExpr:
		return &s.BashPPDerefExpr{Star: c.pos(x.Star), X: c.expr(x.X)}
	case *ast.BinaryExpr:
		return &s.BashPPBinaryExpr{X: c.expr(x.X), Op: c.lit(x.OpPos, x.Op.String()), Y: c.expr(x.Y)}
	case *ast.SelectorExpr:
		if value := c.genericFuncValue(x); value != nil {
			return value
		}
		if value := c.typeParamMethodExpr(x); value != nil {
			return value
		}
		if c.mappedPkgName(x.X) {
			return &s.BashPPIdent{Name: c.ident(x.Sel)}
		}
		out := &s.BashPPSelectorExpr{X: c.expr(x.X), Dot: c.pos(x.Sel.Pos() - 1), Sel: c.ident(x.Sel), FuncType: c.functionValueType(x)}
		if selection := c.info.Selections[x]; selection != nil && selection.Kind() == types.MethodVal {
			out.MethodValue = true
			out.ReceiverAddressable = c.info.Types[x.X].Addressable()
		}
		return out
	case *ast.IndexExpr:
		if value := c.genericFuncValue(x); value != nil {
			return value
		}
		return &s.BashPPIndexExpr{GoString: c.stringValue(x.X), X: c.expr(x.X), Lbrack: c.pos(x.Lbrack), Rbrack: c.pos(x.Rbrack), Index: c.expr(x.Index)}
	case *ast.IndexListExpr:
		if value := c.genericFuncValue(x); value != nil {
			return value
		}
	case *ast.SliceExpr:
		return &s.BashPPSliceExpr{GoString: c.stringValue(x.X), X: c.expr(x.X), Lbrack: c.pos(x.Lbrack), Rbrack: c.pos(x.Rbrack), Low: c.expr(x.Low), High: c.expr(x.High), Max: c.expr(x.Max), Colon: c.pos(x.Lbrack + 1), SecondColon: func() s.Pos {
			if x.Slice3 {
				return c.pos(x.Max.Pos() - 1)
			}
			return s.Pos{}
		}()}
	case *ast.CompositeLit:
		out := &s.BashPPCompositeLit{LitType: c.typ(x.Type), Lbrace: c.pos(x.Lbrace), Rbrace: c.pos(x.Rbrace)}
		for _, el := range x.Elts {
			v := &s.BashPPCompositeElem{}
			if kv, ok := el.(*ast.KeyValueExpr); ok {
				v.Key = c.expr(kv.Key)
				v.Value = c.expr(kv.Value)
				v.Colon = c.pos(kv.Colon)
			} else {
				v.Value = c.expr(el)
			}
			out.Elems = append(out.Elems, v)
		}
		return out
	case *ast.TypeAssertExpr:
		out := &s.BashPPTypeAssertExpr{X: c.expr(x.X), Dot: c.pos(x.Lparen - 1), Lparen: c.pos(x.Lparen), Rparen: c.pos(x.Rparen), Assert: c.typ(x.Type)}
		if x.Type == nil {
			out.TypeToken = c.lit(x.Lparen+1, "type")
		}
		return out
	case *ast.CallExpr:
		if c.isNewType(x) {
			id := x.Fun.(*ast.Ident)
			return &s.BashPPNewExpr{New: c.ident(id), Lparen: c.pos(x.Lparen), Rparen: c.pos(x.Rparen), AllocType: c.typ(x.Args[0])}
		}
		if c.isNewBuiltin(x) {
			id := x.Fun.(*ast.Ident)
			return &s.BashPPNewExpr{New: c.ident(id), Lparen: c.pos(x.Lparen), Rparen: c.pos(x.Rparen), AllocType: c.valueType(x.Args[0]), Init: c.expr(x.Args[0])}
		}
		if c.info.Types[x.Fun].IsType() && len(x.Args) == 1 {
			var typeLit *s.Lit
			if id, ok := x.Fun.(*ast.Ident); ok {
				typeLit = c.ident(id)
			} else {
				typeLit = c.lit(x.Fun.Pos(), c.text(x.Fun))
				typeLit.ValueEnd = c.pos(x.Fun.End())
			}
			value := c.info.Types[x.Args[0]].Value
			stringConstant := value != nil && value.Kind() == constant.String
			return &s.BashPPConvertExpr{GoStringConstant: stringConstant, ConvType: typeLit, ConvTypeExpr: c.typ(x.Fun), Lparen: c.pos(x.Lparen), Rparen: c.pos(x.Rparen), X: c.expr(x.Args[0])}
		}
		return c.call(x)
	}
	c.fail(e, fmt.Sprintf("expression %T", e))
	return &s.BashPPIdent{Name: c.lit(e.Pos(), "invalid")}
}
func (c *converter) funlit(x *ast.FuncLit) *s.BashPPFuncLit {
	return &s.BashPPFuncLit{Kw: c.lit(x.Type.Func, "func"), Params: c.fields(x.Type.Params, false), Results: c.fields(x.Type.Results, false), Lparen: c.pos(x.Type.Params.Opening), Rparen: c.pos(x.Type.Params.Closing), Body: c.block(x.Body)}
}
func (c *converter) call(x *ast.CallExpr) *s.BashPPCall {
	out := &s.BashPPCall{Lparen: c.pos(x.Lparen), Rparen: c.pos(x.Rparen), Ellipsis: c.pos(x.Ellipsis), ResultFuncType: c.functionValueType(x)}
	isInstantiation := func(e ast.Expr) bool {
		var base ast.Expr
		var args []ast.Expr
		switch v := e.(type) {
		case *ast.IndexExpr:
			base, args = v.X, []ast.Expr{v.Index}
		case *ast.IndexListExpr:
			base, args = v.X, v.Indices
		default:
			return false
		}
		var id *ast.Ident
		switch v := base.(type) {
		case *ast.Ident:
			id = v
		case *ast.SelectorExpr:
			id = v.Sel
		}
		if id != nil {
			if _, ok := c.info.Instances[id]; ok {
				return true
			}
		}
		baseType := c.info.Types[base]
		if !baseType.IsType() {
			if _, ok := baseType.Type.(*types.Signature); !ok {
				return false
			}
		}
		for _, arg := range args {
			if !c.info.Types[arg].IsType() {
				return false
			}
		}
		return true
	}
	var simple func(ast.Expr) bool
	simple = func(e ast.Expr) bool {
		switch v := e.(type) {
		case *ast.Ident, *ast.FuncLit:
			return true
		case *ast.SelectorExpr:
			return simple(v.X)
		case *ast.IndexExpr:
			return isInstantiation(v) && simple(v.X)
		case *ast.IndexListExpr:
			return isInstantiation(v) && simple(v.X)
		}
		return false
	}
	if simple(x.Fun) {
		c.callee(out, x.Fun)
	} else {
		out.CalleeExpr = c.expr(x.Fun)
	}
	for i, a := range x.Args {
		if i == 0 && len(out.Fun) == 1 && out.Fun[0].Value == "make" {
			out.ArgType = c.typ(a)
			out.Args = append(out.Args, c.word(a))
			out.ArgExprs = append(out.ArgExprs, nil)
			continue
		}
		out.Args = append(out.Args, c.word(a))
		out.ArgExprs = append(out.ArgExprs, c.expr(a))
	}
	return out
}

// callee lowers a simple callee — a name, a selector chain, a literal, or
// one of those instantiated — onto the call's Fun/FuncLit/TypeArgs.
func (c *converter) callee(out *s.BashPPCall, e ast.Expr) {
	switch v := e.(type) {
	case *ast.Ident:
		out.Fun = append(out.Fun, c.ident(v))
	case *ast.SelectorExpr:
		if !c.mappedPkgName(v.X) {
			c.callee(out, v.X)
		}
		out.Fun = append(out.Fun, c.ident(v.Sel))
	case *ast.FuncLit:
		out.FuncLit = c.funlit(v)
	case *ast.IndexExpr:
		c.callee(out, v.X)
		out.TypeArgs = append(out.TypeArgs, &s.BashPPTypeArg{ArgType: c.typ(v.Index)})
	case *ast.IndexListExpr:
		c.callee(out, v.X)
		for _, t := range v.Indices {
			out.TypeArgs = append(out.TypeArgs, &s.BashPPTypeArg{ArgType: c.typ(t)})
		}
	default:
		c.fail(e, "call target")
		return
	}
	if args := c.instanceTypeArgs(e); args != nil {
		out.TypeArgs = args
	}
}

// genericFuncValue lowers a generic function used as a VALUE — `Abs[T]`,
// `pair[string, int]`, `slices.Max[[]int]`, or a bare `Abs` whose type
// arguments the checker inferred from the assignment context — as the
// closure that forwards to the instantiated function:
//
//	func(p0 T0, p1 ...T1) R { return Abs[T](p0, p1...) }
//
// The closure is the value's instantiated signature exactly, so every
// consumer — a parameter, a variable, a struct field, a map value — sees an
// ordinary function value and nothing downstream needs a notion of a
// partially applied generic. Go forbids comparing functions, so the extra
// indirection is unobservable. Returns nil when e is not such a value.
func (c *converter) genericFuncValue(e ast.Expr) s.BashPPExpr {
	base := ast.Unparen(e)
	switch v := base.(type) {
	case *ast.IndexExpr:
		base = v.X
	case *ast.IndexListExpr:
		base = v.X
	}
	var id *ast.Ident
	switch v := ast.Unparen(base).(type) {
	case *ast.Ident:
		id = v
	case *ast.SelectorExpr:
		id = v.Sel
	default:
		return nil
	}
	inst, ok := c.info.Instances[id]
	if !ok || inst.TypeArgs == nil || inst.TypeArgs.Len() == 0 {
		return nil
	}
	instantiated, ok := inst.Type.(*types.Signature)
	if !ok {
		return nil
	}
	// The instance carries the instantiated signature; the expression's own
	// type still spells the type parameter list when the name is bare.
	signature, _ := c.checkedType(instantiated, e, "instantiated function value type").(*s.BashPPFuncType)
	if signature == nil {
		return nil
	}
	return c.forwardingClosure(e, signature, func(call *s.BashPPCall, _ []*s.Lit) {
		c.callee(call, ast.Unparen(e))
	})
}

// typeParamMethodExpr lowers a method expression whose receiver type is a
// type parameter — `T.String` inside `func f[T Stringer]` — as the closure
// that calls the method on its first argument, `func(r T, …) R { return
// r.String(…) }`. The receiver is a value at run time, and a method
// selected on a value is what the runtime resolves; a method selected on
// a type parameter's NAME has no declaration to resolve against until the
// frame binds it. Returns nil for every other selector.
func (c *converter) typeParamMethodExpr(x *ast.SelectorExpr) s.BashPPExpr {
	selection := c.info.Selections[x]
	if selection == nil || selection.Kind() != types.MethodExpr {
		return nil
	}
	recv := selection.Recv()
	if pointer, ok := recv.(*types.Pointer); ok {
		recv = pointer.Elem()
	}
	if _, ok := recv.(*types.TypeParam); !ok {
		return nil
	}
	signature, _ := c.checkedType(c.info.TypeOf(x), x, "method expression type").(*s.BashPPFuncType)
	if signature == nil {
		return nil
	}
	return c.forwardingClosure(x, signature, func(call *s.BashPPCall, args []*s.Lit) {
		call.Fun = []*s.Lit{args[0], c.ident(x.Sel)}
		call.Args, call.ArgExprs = call.Args[1:], call.ArgExprs[1:]
	})
}

// forwardingClosure builds `func(a0 T0, a1 ...T1) R { return <callee>(a0,
// a1...) }` for a signature: a closure with that exact signature whose body
// forwards every parameter to a call the caller completes. The callee hook
// receives the call with its arguments already in place and the parameter
// names, so it can take one of them as a receiver.
func (c *converter) forwardingClosure(e ast.Expr, signature *s.BashPPFuncType, callee func(call *s.BashPPCall, args []*s.Lit)) s.BashPPExpr {
	c.syntheticPos = e.Pos()
	defer func() { c.syntheticPos = token.NoPos }()
	at := e.Pos()
	call := &s.BashPPCall{Lparen: c.pos(at), Rparen: c.pos(at)}
	lit := &s.BashPPFuncLit{Kw: c.lit(at, "func"), Lparen: c.pos(at), Rparen: c.pos(at)}
	var spelled []string
	var names []*s.Lit
	for _, group := range signature.Params {
		count := len(group.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			name := fmt.Sprintf("%sarg%d", c.prefix, len(spelled))
			field := *group
			field.Names = []*s.Lit{c.lit(at, name)}
			lit.Params = append(lit.Params, &field)
			text := name
			if field.Variadic() {
				text += "..."
				call.Ellipsis = c.pos(at)
			}
			spelled = append(spelled, text)
			names = append(names, c.lit(at, name))
			call.Args = append(call.Args, &s.Word{Parts: []s.WordPart{c.lit(at, text)}})
			call.ArgExprs = append(call.ArgExprs, &s.BashPPIdent{Name: c.lit(at, name)})
		}
	}
	callee(call, names)
	for _, group := range signature.Results {
		field := *group
		field.Names = nil
		lit.Results = append(lit.Results, &field)
	}
	body := &s.Block{Lbrace: c.pos(at), Rbrace: c.pos(at)}
	if len(lit.Results) == 0 {
		body.Stmts = []*s.Stmt{c.stmt(call)}
	} else {
		spelling := c.text(e) + "(" + strings.Join(spelled, ", ") + ")"
		ret := &s.BashPPReturn{Kw: c.lit(at, "return"), Call: call, Results: []*s.Word{{Parts: []s.WordPart{c.lit(at, spelling)}}}}
		body.Stmts = []*s.Stmt{c.stmt(ret)}
	}
	lit.Body = body
	return lit
}

func (c *converter) statements(st ast.Stmt) []*s.Stmt {
	var cmd s.Command
	switch x := st.(type) {
	case *ast.EmptyStmt:
		return nil
	case *ast.BlockStmt:
		cmd = c.block(x)
	case *ast.ExprStmt:
		expr := ast.Unparen(x.X)
		if call, ok := expr.(*ast.CallExpr); ok {
			cmd = c.call(call)
		} else if recv, ok := expr.(*ast.UnaryExpr); ok && recv.Op == token.ARROW {
			cmd = &s.BashPPReceive{Arrow: c.pos(recv.OpPos), Chan: c.word(recv.X), ChanExpr: c.expr(recv.X)}
		} else {
			c.fail(x, "expression statement")
		}
	case *ast.DeclStmt:
		g := x.Decl.(*ast.GenDecl)
		var out []*s.Stmt
		for _, spec := range g.Specs {
			switch v := spec.(type) {
			case *ast.ValueSpec:
				if g.Tok == token.VAR && len(v.Values) == 1 && len(v.Names) > 1 {
					out = append(out, c.tupleValueDecls(g, v)...)
					continue
				}
				for i, n := range v.Names {
					out = append(out, c.stmt(c.valueDecl(g, v, n, i)))
				}
			case *ast.TypeSpec:
				out = append(out, c.stmt(c.typeDecl(g, v)))
			}
		}
		return out
	case *ast.AssignStmt:
		if x.Tok == token.DEFINE {
			out := &s.BashPPShortDecl{Class: s.ClassR, GoRegion: true, OpPos: c.pos(x.TokPos)}
			for _, e := range x.Lhs {
				out.Lhs = append(out.Lhs, c.ident(e.(*ast.Ident)))
			}
			for _, e := range x.Rhs {
				out.Rhs = append(out.Rhs, c.word(e))
				if len(x.Rhs) > 1 {
					out.RhsExprs = append(out.RhsExprs, c.expr(e))
				}
			}
			if len(x.Rhs) == 1 {
				switch rhs := x.Rhs[0].(type) {
				case *ast.FuncLit:
					out.FuncLit = c.funlit(rhs)
					out.Rhs = nil
				case *ast.CallExpr:
					if c.isNewBuiltin(rhs) {
						out.Expr = c.expr(rhs)
						out.Rhs = nil
						break
					}
					if c.info.Types[rhs.Fun].IsType() {
						out.Expr = c.expr(rhs)
						out.Rhs = nil
						break
					}
					out.Call = c.call(rhs)
					if len(out.Call.Fun) == 1 && out.Call.Fun[0].Value == "make" {
						if ch, ok := out.Call.ArgType.(*s.BashPPChanType); ok {
							out.MakeChan = &s.BashPPMakeChan{Make: out.Call.Fun[0], ChanType: ch, Lparen: out.Call.Lparen, Rparen: out.Call.Rparen}
							if len(rhs.Args) > 1 {
								out.MakeChan.Capacity = c.word(rhs.Args[1])
								out.MakeChan.CapacityExpr = c.expr(rhs.Args[1])
							}
							out.Call = nil
							out.Rhs = nil
						}
					}
				case *ast.UnaryExpr:
					if rhs.Op == token.ARROW {
						out.Recv = &s.BashPPReceive{Arrow: c.pos(rhs.OpPos), Chan: c.word(rhs.X), ChanExpr: c.expr(rhs.X)}
						out.Rhs = nil
					} else {
						out.Expr = c.expr(rhs)
					}
				default:
					out.Expr = c.expr(rhs)
					// An instantiated generic function value lowers to a
					// closure; it takes the literal's slot, as a literal does.
					if lit, ok := out.Expr.(*s.BashPPFuncLit); ok {
						out.FuncLit, out.Expr, out.Rhs = lit, nil, nil
					}
				}
			}
			cmd = out
		} else if x.Tok == token.ASSIGN {
			if len(x.Lhs) > 1 && !tuplePlainTargets(x.Lhs) {
				return c.tupleAssignStmts(x)
			}
			// Parentheses around an lvalue are meaningless in Go: `(_) = v`
			// and `(x) = v` assign exactly as their unparenthesized forms. Peel
			// them so a parenthesized blank still reaches the blank-name list
			// and a parenthesized name is not lowered as a value read.
			target := ast.Unparen(x.Lhs[0])
			out := &s.BashPPAssign{Eq: c.pos(x.TokPos), Target: c.word(target), Value: c.word(x.Rhs[0]), TargetExpr: c.expr(target)}
			for _, e := range x.Lhs {
				if id, ok := ast.Unparen(e).(*ast.Ident); ok {
					out.Names = append(out.Names, c.ident(id))
				}
			}
			for _, e := range x.Rhs {
				out.Values = append(out.Values, c.word(e))
				out.ValueExprs = append(out.ValueExprs, c.expr(e))
			}
			if len(x.Rhs) == 1 {
				out.ValueExpr = c.expr(x.Rhs[0])
				if call, ok := x.Rhs[0].(*ast.CallExpr); ok && !c.info.Types[call.Fun].IsType() {
					out.Call = c.call(call)
				}
			}
			cmd = out
		} else {
			cmd = &s.BashPPUpdate{TargetWord: c.word(x.Lhs[0]), Target: c.expr(x.Lhs[0]), Op: c.lit(x.TokPos, x.Tok.String()), ValueWord: c.word(x.Rhs[0]), Value: c.expr(x.Rhs[0])}
		}
	case *ast.IncDecStmt:
		out := &s.BashPPIncDec{TargetWord: c.word(x.X), Target: c.expr(x.X), Op: c.lit(x.TokPos, x.Tok.String())}
		if id, ok := x.X.(*ast.Ident); ok {
			out.Name = c.ident(id)
		}
		cmd = out
	case *ast.ReturnStmt:
		out := &s.BashPPReturn{Kw: c.lit(x.Return, "return")}
		for _, e := range x.Results {
			out.Results = append(out.Results, c.word(e))
			if len(x.Results) > 1 {
				out.ResultExprs = append(out.ResultExprs, c.expr(e))
			}
		}
		if len(x.Results) == 1 {
			switch e := x.Results[0].(type) {
			case *ast.CallExpr:
				if c.info.Types[e.Fun].IsType() {
					out.Expr = c.expr(e)
				} else {
					out.Call = c.call(e)
				}
			case *ast.FuncLit:
				out.FuncLit = c.funlit(e)
				out.Results = nil
			default:
				out.Expr = c.expr(e)
				if lit, ok := out.Expr.(*s.BashPPFuncLit); ok {
					out.FuncLit, out.Expr, out.Results = lit, nil, nil
				}
			}
		}
		cmd = out
	case *ast.IfStmt:
		out := &s.BashPPIf{Site: s.StartGoIf, If: c.pos(x.If), Cond: c.expr(x.Cond), Then: c.block(x.Body)}
		if x.Init != nil {
			init := c.one(x.Init)
			if decl, ok := init.(*s.BashPPShortDecl); ok {
				out.Init = decl
			} else {
				out.InitStmt = init
			}
			out.Semicolon = c.headerToken(x.If, x.Body.Lbrace, token.SEMICOLON, 0)
		}
		if x.Else != nil {
			out.Else = c.one(x.Else)
			out.ElsePos = c.pos(x.Else.Pos())
		}
		cmd = out
	case *ast.ForStmt:
		leaveBranch := c.pushBranchScope(c.takeStatementLabel(), true)
		body := c.block(x.Body)
		leaveBranch()
		cmd = &s.BashPPFor{For: c.pos(x.For), Init: c.one(x.Init), Cond: c.expr(x.Cond), Post: c.one(x.Post), Body: body, FirstSemi: c.headerToken(x.For, x.Body.Lbrace, token.SEMICOLON, 0), SecondSemi: c.headerToken(x.For, x.Body.Lbrace, token.SEMICOLON, 1)}
	case *ast.RangeStmt:
		leaveBranch := c.pushBranchScope(c.takeStatementLabel(), true)
		body := c.block(x.Body)
		leaveBranch()
		out := &s.BashPPRange{For: c.pos(x.For), Range: c.pos(x.Range), Chan: c.word(x.X), Expr: c.expr(x.X), Body: body}
		var assignments []*s.Stmt
		for i, e := range []ast.Expr{x.Key, x.Value} {
			if e != nil {
				if x.Tok == token.DEFINE {
					out.Names = append(out.Names, c.ident(e.(*ast.Ident)))
				} else {
					temp := &ast.Ident{NamePos: e.Pos(), Name: fmt.Sprintf("%srange_%d_%d", c.prefix, x.TokPos, i)}
					out.Names = append(out.Names, c.ident(temp))
					assign := &ast.AssignStmt{Lhs: []ast.Expr{e}, TokPos: x.TokPos, Tok: token.ASSIGN, Rhs: []ast.Expr{temp}}
					assignments = append(assignments, c.statements(assign)...)
				}
			}
		}
		if x.Tok == token.DEFINE || len(assignments) > 0 {
			out.Define = c.pos(x.TokPos)
		}
		out.Body.Stmts = append(assignments, out.Body.Stmts...)
		cmd = out
	case *ast.BranchStmt:
		if x.Tok == token.GOTO {
			cmd = &s.BashPPGoto{Kw: c.lit(x.TokPos, "goto"), Label: c.ident(x.Label)}
			break
		}
		depth := 0
		if x.Label != nil {
			depth = c.labeledBranchDepth(x)
		}
		cmd = &s.BashPPBranch{Kw: c.lit(x.TokPos, x.Tok.String()), Depth: uint(depth)}
	case *ast.DeferStmt:
		cmd = &s.BashPPDefer{Kw: c.lit(x.Defer, "defer"), Call: c.call(x.Call)}
	case *ast.GoStmt:
		cmd = &s.BashPPGo{Kw: c.lit(x.Go, "go"), Call: c.call(x.Call)}
	case *ast.SendStmt:
		cmd = &s.BashPPSend{Chan: c.word(x.Chan), Arrow: c.pos(x.Arrow), Value: c.word(x.Value), ValueExpr: c.expr(x.Value), ChanExpr: c.expr(x.Chan)}
	case *ast.SelectStmt:
		leaveBranch := c.pushBranchScope(c.takeStatementLabel(), false)
		defer leaveBranch()
		out := &s.BashPPSelect{Select: c.pos(x.Select), Lbrace: c.pos(x.Body.Lbrace), Rbrace: c.pos(x.Body.Rbrace)}
		for _, st := range x.Body.List {
			cc := st.(*ast.CommClause)
			v := &s.BashPPSelectCase{Case: c.pos(cc.Case), Colon: c.pos(cc.Colon), Default: cc.Comm == nil, Comm: c.one(cc.Comm)}
			for _, body := range cc.Body {
				v.Stmts = append(v.Stmts, c.statements(body)...)
			}
			out.Cases = append(out.Cases, v)
		}
		cmd = out
	case *ast.TypeSwitchStmt:
		label := c.takeStatementLabel()
		if x.Init != nil {
			c.fail(x.Init, "type switch initializer")
		}
		guard := &s.BashPPShortDecl{Class: s.ClassR, GoRegion: true}
		switch assign := x.Assign.(type) {
		case *ast.ExprStmt:
			guard.Expr = c.expr(assign.X.(*ast.TypeAssertExpr))
		case *ast.AssignStmt:
			guard.OpPos = c.pos(assign.TokPos)
			for _, lhs := range assign.Lhs {
				guard.Lhs = append(guard.Lhs, c.ident(lhs.(*ast.Ident)))
			}
			guard.Expr = c.expr(assign.Rhs[0].(*ast.TypeAssertExpr))
		}
		leaveBranch := c.pushBranchScope(label, false)
		defer leaveBranch()
		out := &s.BashPPSwitch{Switch: c.pos(x.Switch), TypeSwitch: true, Init: guard, Lbrace: c.pos(x.Body.Lbrace), Rbrace: c.pos(x.Body.Rbrace)}
		for _, st := range x.Body.List {
			cc := st.(*ast.CaseClause)
			v := &s.BashPPSwitchArm{Case: c.pos(cc.Case), Colon: c.pos(cc.Colon)}
			for _, e := range cc.List {
				v.Types = append(v.Types, c.typ(e))
			}
			for _, body := range cc.Body {
				v.Stmts = append(v.Stmts, c.statements(body)...)
			}
			out.Arms = append(out.Arms, v)
		}
		cmd = out
	case *ast.SwitchStmt:
		leaveBranch := c.pushBranchScope(c.takeStatementLabel(), false)
		defer leaveBranch()
		out := &s.BashPPSwitch{Switch: c.pos(x.Switch), Init: c.one(x.Init), Tag: c.expr(x.Tag), Lbrace: c.pos(x.Body.Lbrace), Rbrace: c.pos(x.Body.Rbrace)}
		for _, st := range x.Body.List {
			cc := st.(*ast.CaseClause)
			v := &s.BashPPSwitchArm{Case: c.pos(cc.Case), Colon: c.pos(cc.Colon)}
			for _, e := range cc.List {
				v.Exprs = append(v.Exprs, c.expr(e))
			}
			for _, body := range cc.Body {
				v.Stmts = append(v.Stmts, c.statements(body)...)
			}
			out.Arms = append(out.Arms, v)
		}
		cmd = out
	case *ast.LabeledStmt:
		// The label is carried for goto and for lowering. A labeled loop,
		// switch, or select additionally becomes a named branch scope so a
		// labeled break/continue resolves to a depth; any other statement is
		// only a goto target, so a loop nested inside it must not take the
		// label.
		out := &s.BashPPLabeled{Label: c.ident(x.Label), Colon: c.pos(x.Colon)}
		previous := c.statementLabel
		c.statementLabel = ""
		switch x.Stmt.(type) {
		case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
			c.statementLabel = x.Label.Name
		}
		inner := c.statements(x.Stmt)
		c.statementLabel = previous
		// A statement that converts to several (a range with assignment
		// targets, a tuple assignment) keeps its first as the labeled one; the
		// rest follow it in the block, which is where execution resumes after a
		// goto to the label.
		if len(inner) > 0 {
			out.Stmt = inner[0]
			inner = inner[1:]
		}
		return append([]*s.Stmt{c.stmt(out)}, inner...)
	default:
		c.fail(st, strings.TrimPrefix(fmt.Sprintf("%T", st), "*ast."))
	}
	if cmd == nil {
		return nil
	}
	return []*s.Stmt{c.stmt(cmd)}
}
func (c *converter) one(st ast.Stmt) s.Command {
	if st == nil {
		return nil
	}
	v := c.statements(st)
	if len(v) != 1 {
		c.fail(st, "compound simple statement")
		return nil
	}
	return v[0].Cmd
}

// headerToken finds a punctuation token at header nesting depth zero. Go AST
// omits semicolon positions; scanning the original bytes preserves them.
func (c *converter) headerToken(from, to token.Pos, want token.Token, ordinal int) s.Pos {
	file := c.fset.File(from)
	if file == nil {
		return s.Pos{}
	}
	var data []byte
	for i, f := range c.files {
		if c.fset.File(f.Pos()) == file {
			data = c.sources[i].Data
			break
		}
	}
	start, end := file.Offset(from), file.Offset(to)
	if start < 0 || end > len(data) || start > end {
		return s.Pos{}
	}
	fragment := data[start:end]
	fset := token.NewFileSet()
	sf := fset.AddFile(file.Name(), -1, len(fragment))
	var scan scanner.Scanner
	scan.Init(sf, fragment, nil, 0)
	depth := 0
	for {
		p, tok, _ := scan.Scan()
		if tok == token.EOF {
			break
		}
		switch tok {
		case token.LPAREN, token.LBRACE, token.LBRACK:
			depth++
		case token.RPAREN, token.RBRACE, token.RBRACK:
			depth--
		}
		if depth == 0 && tok == want {
			if ordinal == 0 {
				return c.pos(from + token.Pos(sf.Offset(p)))
			}
			ordinal--
		}
	}
	return s.Pos{}
}
