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
	"strconv"
	"strings"

	s "mvdan.cc/sh/v3/syntax"
)

type converter struct {
	packagePath   string
	importAliases map[string]string
	syntheticPos  token.Pos
	prefix        string
	fset          *token.FileSet
	files         []*ast.File
	sources       []Source
	info          *types.Info
	renames       map[types.Object]string
	err           error
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
		c.err = fmt.Errorf("%s: gosource: unsupported %s", c.fset.Position(n.Pos()), what)
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
func (c *converter) text(n ast.Node) string {
	var b bytes.Buffer
	original := map[*ast.Ident]string{}
	ast.Inspect(n, func(node ast.Node) bool {
		if id, ok := node.(*ast.Ident); ok {
			if name := c.renames[c.info.ObjectOf(id)]; name != "" {
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
	return b.String()
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
func (c *converter) typ(e ast.Expr) s.BashPPTypeExpr {
	if e == nil {
		return nil
	}
	switch x := e.(type) {
	case *ast.Ident:
		return &s.BashPPNamedType{Name: c.ident(x)}
	case *ast.SelectorExpr:
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
		recv.RecvType = c.lit(t.Pos(), c.text(t))
		o.Receiver = recv
	}
	return o
}
func (c *converter) importSpec(g *ast.GenDecl, i *ast.ImportSpec) *s.BashPPImport {
	path, _ := strconv.Unquote(i.Path.Value)
	out := &s.BashPPImport{Site: s.StartImport, Class: s.ClassR, Kw: c.lit(g.TokPos, "import"), Path: &s.DblQuoted{Left: c.pos(i.Path.Pos()), Right: c.pos(i.Path.End() - 1), Parts: []s.WordPart{c.lit(i.Path.Pos()+1, path)}}}
	if i.Name != nil {
		out.Alias = c.ident(i.Name)
	} else if obj := c.info.Implicits[i]; c.renames[obj] != "" {
		out.Alias = c.lit(i.Path.Pos(), c.renames[obj])
	}
	return out
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
			typeName := types.TypeString(obj.Type(), func(p *types.Package) string {
				if p.Path() == c.packagePath {
					return ""
				}
				if alias := c.importAliases[p.Path()]; alias != "" {
					return alias
				}
				return p.Name()
			})
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
		return &s.BashPPSelectorExpr{X: c.expr(x.X), Dot: c.pos(x.Sel.Pos() - 1), Sel: c.ident(x.Sel)}
	case *ast.IndexExpr:
		return &s.BashPPIndexExpr{X: c.expr(x.X), Lbrack: c.pos(x.Lbrack), Rbrack: c.pos(x.Rbrack), Index: c.expr(x.Index)}
	case *ast.SliceExpr:
		return &s.BashPPSliceExpr{X: c.expr(x.X), Lbrack: c.pos(x.Lbrack), Rbrack: c.pos(x.Rbrack), Low: c.expr(x.Low), High: c.expr(x.High), Max: c.expr(x.Max), Colon: c.pos(x.Lbrack + 1), SecondColon: func() s.Pos {
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
		if id, ok := x.Fun.(*ast.Ident); ok {
			if obj, ok := c.info.Uses[id].(*types.Builtin); ok && obj.Name() == "new" {
				return &s.BashPPNewExpr{New: c.ident(id), Lparen: c.pos(x.Lparen), Rparen: c.pos(x.Rparen), AllocType: c.typ(x.Args[0])}
			}
		}
		if c.info.Types[x.Fun].IsType() && len(x.Args) == 1 {
			var typeLit *s.Lit
			if id, ok := x.Fun.(*ast.Ident); ok {
				typeLit = c.ident(id)
			} else {
				typeLit = c.lit(x.Fun.Pos(), c.text(x.Fun))
				typeLit.ValueEnd = c.pos(x.Fun.End())
			}
			return &s.BashPPConvertExpr{ConvType: typeLit, ConvTypeExpr: c.typ(x.Fun), Lparen: c.pos(x.Lparen), Rparen: c.pos(x.Rparen), X: c.expr(x.Args[0])}
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
	out := &s.BashPPCall{Lparen: c.pos(x.Lparen), Rparen: c.pos(x.Rparen), Ellipsis: c.pos(x.Ellipsis)}
	var simple func(ast.Expr) bool
	simple = func(e ast.Expr) bool {
		switch v := e.(type) {
		case *ast.Ident, *ast.FuncLit:
			return true
		case *ast.SelectorExpr:
			return simple(v.X)
		case *ast.IndexExpr:
			return simple(v.X)
		case *ast.IndexListExpr:
			return simple(v.X)
		}
		return false
	}
	var callee func(ast.Expr)
	callee = func(e ast.Expr) {
		switch v := e.(type) {
		case *ast.Ident:
			out.Fun = append(out.Fun, c.ident(v))
		case *ast.SelectorExpr:
			callee(v.X)
			out.Fun = append(out.Fun, c.ident(v.Sel))
		case *ast.FuncLit:
			out.FuncLit = c.funlit(v)
		case *ast.IndexExpr:
			callee(v.X)
			out.TypeArgs = append(out.TypeArgs, &s.BashPPTypeArg{ArgType: c.typ(v.Index)})
		case *ast.IndexListExpr:
			callee(v.X)
			for _, t := range v.Indices {
				out.TypeArgs = append(out.TypeArgs, &s.BashPPTypeArg{ArgType: c.typ(t)})
			}
		default:
			c.fail(e, "call target")
		}
	}
	if simple(x.Fun) {
		callee(x.Fun)
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
	if out.ArgType != nil {
		out.ArgExprs = nil
	}
	return out
}
func (c *converter) statements(st ast.Stmt) []*s.Stmt {
	var cmd s.Command
	switch x := st.(type) {
	case *ast.EmptyStmt:
		return nil
	case *ast.BlockStmt:
		cmd = c.block(x)
	case *ast.ExprStmt:
		if call, ok := x.X.(*ast.CallExpr); ok {
			cmd = c.call(call)
		} else if recv, ok := x.X.(*ast.UnaryExpr); ok && recv.Op == token.ARROW {
			cmd = &s.BashPPReceive{Arrow: c.pos(recv.OpPos), Chan: c.word(recv.X)}
		} else {
			c.fail(x, "expression statement")
		}
	case *ast.DeclStmt:
		g := x.Decl.(*ast.GenDecl)
		var out []*s.Stmt
		for _, spec := range g.Specs {
			switch v := spec.(type) {
			case *ast.ValueSpec:
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
			}
			if len(x.Rhs) == 1 {
				switch rhs := x.Rhs[0].(type) {
				case *ast.FuncLit:
					out.FuncLit = c.funlit(rhs)
					out.Rhs = nil
				case *ast.CallExpr:
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
							}
							out.Call = nil
							out.Rhs = nil
						}
					}
				case *ast.UnaryExpr:
					if rhs.Op == token.ARROW {
						out.Recv = &s.BashPPReceive{Arrow: c.pos(rhs.OpPos), Chan: c.word(rhs.X)}
						out.Rhs = nil
					} else {
						out.Expr = c.expr(rhs)
					}
				default:
					out.Expr = c.expr(rhs)
				}
			}
			cmd = out
		} else if x.Tok == token.ASSIGN {
			out := &s.BashPPAssign{Eq: c.pos(x.TokPos), Target: c.word(x.Lhs[0]), Value: c.word(x.Rhs[0]), TargetExpr: c.expr(x.Lhs[0])}
			for _, e := range x.Lhs {
				if id, ok := e.(*ast.Ident); ok {
					out.Names = append(out.Names, c.ident(id))
				} else if len(x.Lhs) > 1 {
					c.fail(x, "tuple structured assignment")
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
			}
		}
		cmd = out
	case *ast.IfStmt:
		out := &s.BashPPIf{Site: s.StartGoIf, If: c.pos(x.If), Cond: c.expr(x.Cond), Then: c.block(x.Body)}
		if x.Init != nil {
			init := c.one(x.Init)
			var ok bool
			out.Init, ok = init.(*s.BashPPShortDecl)
			if !ok {
				c.fail(x.Init, "if initializer")
			}
		}
		if x.Else != nil {
			out.Else = c.one(x.Else)
			out.ElsePos = c.pos(x.Else.Pos())
		}
		cmd = out
	case *ast.ForStmt:
		cmd = &s.BashPPFor{For: c.pos(x.For), Init: c.one(x.Init), Cond: c.expr(x.Cond), Post: c.one(x.Post), Body: c.block(x.Body), FirstSemi: c.headerToken(x.For, x.Body.Lbrace, token.SEMICOLON, 0), SecondSemi: c.headerToken(x.For, x.Body.Lbrace, token.SEMICOLON, 1)}
	case *ast.RangeStmt:
		out := &s.BashPPRange{For: c.pos(x.For), Define: c.pos(x.TokPos), Range: c.pos(x.Range), Chan: c.word(x.X), Expr: c.expr(x.X), Body: c.block(x.Body)}
		for _, e := range []ast.Expr{x.Key, x.Value} {
			if e != nil {
				if id, ok := e.(*ast.Ident); ok {
					out.Names = append(out.Names, c.ident(id))
				} else {
					c.fail(e, "range assignment target")
				}
			}
		}
		cmd = out
	case *ast.BranchStmt:
		if x.Label != nil {
			c.fail(x, "labeled branch")
		}
		cmd = &s.BashPPBranch{Kw: c.lit(x.TokPos, x.Tok.String())}
	case *ast.DeferStmt:
		cmd = &s.BashPPDefer{Kw: c.lit(x.Defer, "defer"), Call: c.call(x.Call)}
	case *ast.GoStmt:
		cmd = &s.BashPPGo{Kw: c.lit(x.Go, "go"), Call: c.call(x.Call)}
	case *ast.SendStmt:
		cmd = &s.BashPPSend{Chan: c.word(x.Chan), Arrow: c.pos(x.Arrow), Value: c.word(x.Value)}
	case *ast.SelectStmt:
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
		if x.Init != nil {
			c.fail(x.Init, "type switch initializer")
		}
		out := &s.BashPPSwitch{Switch: c.pos(x.Switch), TypeSwitch: true, Init: c.one(x.Assign), Lbrace: c.pos(x.Body.Lbrace), Rbrace: c.pos(x.Body.Rbrace)}
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
	case *ast.SwitchStmt:
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
