// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"errors"
	"fmt"
	"go/constant"
	"maps"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Evaluation of the Bash++ function nodes: the P3-A ("typed functions")
// declarations, `return` and `defer`, and the P3-B function LITERALS,
// closures and VARIADIC parameters.
//
// ONE INVOKER, TWO SPELLINGS. A literal is not a second kind of function: it
// is a [bashPPFunc] whose signature came from a [syntax.BashPPFuncLit] instead
// of a declaration, so parameter binding, results, defers and status bridging
// are the same code for both. What a literal adds is WHEN the lexical scope is
// captured — at each evaluation of the literal, not once per name — which is
// what makes two closures from the same factory hold different cells.
//
// TWO NAMESPACES, ALREADY RECONCILED. A Bash++ func binds its parameters and
// named results as TYPED lexical bindings, not as a second kind of shell
// variable, and it leans on a property vars.go already guarantees: an ordinary
// shell assignment to a name a lexical binding owns writes THROUGH to that
// binding (see [Runner.setVar]). So `func f() (n int) { n=5; return }` needs no
// special machinery — `n=5` updates the same cell the bare return reads, and a
// closure over an outer `var` observes writes for the same reason. Only the
// func's own locals live in the shell function scope pushed alongside.

// bashPPFunc is a callable Go-form function: its signature and body, plus the
// lexical scope captured where it was written, which its body's free
// identifiers close over rather than resolving them at the call site.
//
// A named declaration and a function literal are the same thing to every
// caller — same binding rules, same results, same defers — so they are one
// type with two spellings rather than two types with one duplicated invoker.
// Exactly one of decl and lit is set.
type bashPPFunc struct {
	goSourceReceiver *goSourceMethodBinding
	native           *bashPPBridgeValue
	rangeYield       *goSourceRangeYield
	collectYield     *[]bashPPBridgeValue
	decl             *syntax.BashPPFuncDecl
	lit              *syntax.BashPPFuncLit
	scope            *bashPPScope
	typeArgs         map[string]syntax.BashPPTypeExpr
	// bound is the name a literal was bound to by `:=`, kept only so that a
	// diagnostic can say which function the script means. It is not an
	// identity: the same closure may be copied to other names, and a later
	// binding of this one does not rename it.
	bound string
	// receiver is set on a resolved method call or method value. The method
	// declaration itself keeps it nil; resolution clones the function and
	// binds either a copied value cell or the addressable pointer cell.
	receiver *bashPPCell
	// skipArgs is one for a method expression T.M(v, ...), where v supplies
	// the receiver rather than the first ordinary parameter.
	skipArgs int
}

// bashPPType is one script-local named type. Aliases intentionally cannot own
// methods, matching Go's receiver declaration rule.
type bashPPType struct {
	underlying string
	alias      bool
	typeParams []*syntax.BashPPTypeParam
	members    []string
	typeExpr   syntax.BashPPTypeExpr
	fields     []*syntax.BashPPField
}

// name is what diagnostics call the function. A literal has none, so it is
// described by where it came from rather than given a fabricated identifier a
// reader would then look for in the script.
func (f *bashPPFunc) name() string {
	switch {
	case f.decl != nil:
		return f.decl.Name.Value
	case f.bound != "":
		return f.bound
	}
	return "func literal"
}

func (f *bashPPFunc) params() []*syntax.BashPPField {
	if f.decl != nil {
		if len(f.typeArgs) > 0 {
			return bashPPSubstituteFields(f.decl.Params, f.typeArgs)
		}
		return f.decl.Params
	}
	// A literal has no type parameters of its own, but one written inside a
	// generic body carries that instantiation's bindings, so its signature
	// substitutes them exactly as a declared function's does.
	if len(f.typeArgs) > 0 {
		return bashPPSubstituteFields(f.lit.Params, f.typeArgs)
	}
	return f.lit.Params
}

func (f *bashPPFunc) results() []*syntax.BashPPField {
	if f.decl != nil {
		if len(f.typeArgs) > 0 {
			return bashPPSubstituteFields(f.decl.Results, f.typeArgs)
		}
		return f.decl.Results
	}
	if len(f.typeArgs) > 0 {
		return bashPPSubstituteFields(f.lit.Results, f.typeArgs)
	}
	return f.lit.Results
}

func (f *bashPPFunc) typeParams() []*syntax.BashPPTypeParam {
	if f.decl != nil {
		return f.decl.TypeParams
	}
	return nil
}

// cloned copies f for a subshell, deep-copying its captured scope through the
// shared cloner so the aliasing between closures, and between a closure and the
// live scope, survives the copy.
func (f *bashPPFunc) cloned(c *bashPPCloner) *bashPPFunc {
	copied := *f
	copied.scope = c.clone(f.scope)
	copied.receiver = c.cloneCell(f.receiver)
	if f.typeArgs != nil {
		copied.typeArgs = make(map[string]syntax.BashPPTypeExpr, len(f.typeArgs))
		for name, typ := range f.typeArgs {
			copied.typeArgs[name] = typ
		}
	}
	return &copied
}

func (f *bashPPFunc) body() *syntax.Block {
	if f.decl != nil {
		return f.decl.Body
	}
	return f.lit.Body
}

// bashPPFuncHandlePrefix marks a string value as a reference into the runner's
// closure registry.
//
// WHY A HANDLE AND NOT A VALUE. A closure is a Go pointer with a captured
// scope; the shell's value model is strings, and every path a variable takes —
// expansion, `execve`, a subshell's private copy — assumes it can carry the
// value as bytes. A handle keeps that assumption true: the bytes are what the
// shell moves around, while the function itself stays on the runner, where the
// subshell cloner can copy it with the rest of the typed state. The prefix is
// deliberately unmistakable rather than opaque, so `echo $f` shows a reader
// what they are holding instead of a bare integer.
const bashPPFuncHandlePrefix = "func@bashpp:"

// bashPPMakeClosure evaluates a function literal to a callable value,
// capturing the lexical scope AT THIS MOMENT.
//
// The capture is per EVALUATION, not per literal: a literal written inside a
// loop body yields a different closure each time round, over that iteration's
// cells. That is Go's rule, and it is why the registry is appended to rather
// than memoized on the syntax node.
func (r *Runner) bashPPMakeClosure(lit *syntax.BashPPFuncLit) (*bashPPFunc, expand.Variable) {
	fn := &bashPPFunc{lit: lit}
	if r.bashPPScope != nil {
		if r.bashPPGoSource && r.bashPPFuncActive == 0 {
			fn.scope = r.bashPPScope
		} else {
			fn.scope = r.bashPPScope.snapshot()
		}
	}
	// A literal written inside a generic body is part of THAT instantiation:
	// its own body may name the enclosing `T`, and it keeps meaning the type
	// argument of the call that created the closure even if the closure
	// escapes and is invoked later. The bindings travel with the closure for
	// the same reason its captured scope does.
	fn.typeArgs = r.bashPPTypeParamArgs
	return fn, r.bashPPStoreFunc(fn)
}

// bashPPFuncLitType is the concrete signature a function literal names as a
// value. A function-typed target — `var f func(int) int; f = func(n int) int
// {…}` — owns a func type, so the value it is given has to arrive naming one
// too. Without this the closure travels as a bare handle with no declared
// type, and reassigning it reads as an untyped scalar that is rejected as
// "not assignable to func(int)(int)". Building the type from the literal's own
// parameter and result fields keeps its identity exactly the signature the
// programmer wrote, which is what a recursive closure calling itself relies on.
func bashPPFuncLitType(lit *syntax.BashPPFuncLit) *syntax.BashPPFuncType {
	return &syntax.BashPPFuncType{
		Func:      lit.Kw.Pos(),
		Lparen:    lit.Lparen,
		Rparen:    lit.Rparen,
		Params:    lit.Params,
		Results:   lit.Results,
		ResLparen: lit.ResLparen,
		ResRparen: lit.ResRparen,
	}
}

func (r *Runner) bashPPStoreFunc(fn *bashPPFunc) expand.Variable {
	r.bashPPClosures = append(r.bashPPClosures, fn)
	return expand.Variable{
		Set:  true,
		Kind: expand.String,
		Str:  bashPPFuncHandlePrefix + strconv.Itoa(len(r.bashPPClosures)-1),
	}
}

// bashPPClosure resolves a handle back to the closure it names. An unknown or
// malformed handle is simply not a function, which is what lets an ordinary
// string variable share the namespace without ever being mistaken for one.
func (r *Runner) bashPPClosure(value string) (*bashPPFunc, bool) {
	rest, ok := strings.CutPrefix(value, bashPPFuncHandlePrefix)
	if !ok {
		return nil, false
	}
	i, err := strconv.Atoi(rest)
	if err != nil || i < 0 || i >= len(r.bashPPClosures) {
		return nil, false
	}
	return r.bashPPClosures[i], true
}

// bashPPDeferred is one entry on the deferred-call stack: the call to run when
// the enclosing func returns, and the arguments captured — already evaluated —
// at the point `defer` ran, which is what gives Go's "arguments are evaluated
// when the defer statement executes" rule.
type bashPPDeferred struct {
	builtin func()
	native  func(context.Context) error
	testing func()
	agentic bool
	call    *syntax.BashPPCall
	// fn is the function resolved AT DEFER TIME, which matters for a closure:
	// `defer f(1)` must run the f that was current when the defer executed,
	// not whatever f names when the frame unwinds. It is nil when the deferred
	// callee is an ordinary shell command.
	fn *bashPPFunc
	// predeclared names the Bash++ predeclared function deferred, when the
	// callee is one: `defer panic(v)`. It is resolved at defer time like fn,
	// so a later declaration shadowing the name cannot change what unwinds.
	predeclared string
	args        []string
	cells       []*bashPPCell
}

// bashPPReturnState carries a Go-form return out through the body's statement
// loop to the invoker. active records that a return fired at all, which
// distinguishes a bare `return` (values nil) from falling off the end of the
// body.
type bashPPReturnState struct {
	active bool
	values []string
	cells  []*bashPPCell
}

// bashPPFuncDecl registers a typed function, capturing the lexical environment
// it was written in so the body closes over its definition site.
func (r *Runner) bashPPFuncDecl(d *syntax.BashPPFuncDecl) {
	if !r.objectsEnabled() {
		r.errf("bash++ function declaration evaluated with extensions disabled\n")
		r.exit = exitStatus{code: 2}
		return
	}
	name := d.Name.Value
	if required := bashppRequiredAfterDefault(d.Params); required != "" {
		r.errf("BASHPP-EDEFAULT-ORDER: required parameter %q follows a default parameter\n", required)
		r.exit = exitStatus{code: 2}
		return
	}
	if !syntax.BashPPValidIdent(name) {
		r.errf("invalid function name: %q\n", name)
		r.exit = exitStatus{code: 2}
		return
	}
	if err := bashPPValidateTypeParamDecls(d.TypeParams); err != nil {
		r.errf("%v\n", err)
		r.exit.code = 2
		return
	}
	if !r.bashPPCheckEnumSwitches(d) {
		return
	}
	if d.Receiver != nil {
		r.bashPPMethodDecl(d)
		return
	}
	if r.bashPPFuncs == nil {
		r.bashPPFuncs = make(map[string]*bashPPFunc, 4)
	}
	if _, exists := r.bashPPFuncs[name]; exists {
		r.errf("function %s redeclared in this session\n", name)
		r.exit.code = 2
		return
	}
	var captured *bashPPScope
	if r.bashPPScope != nil {
		if r.bashPPGoSource {
			captured = r.bashPPScope
		} else {
			captured = r.bashPPScope.snapshot()
		}
	}
	r.bashPPFuncs[name] = &bashPPFunc{decl: d, scope: captured}
}

func bashPPValidateTypeParamDecls(params []*syntax.BashPPTypeParam) error {
	seen := make(map[string]bool)
	for _, group := range params {
		if group == nil || group.Constraint == nil {
			return fmt.Errorf("BASHPP-EGENERIC-PARAM: type parameter constraint is missing")
		}
		for _, name := range group.Names {
			if name == nil || !syntax.BashPPValidIdent(name.Value) {
				return fmt.Errorf("BASHPP-EGENERIC-PARAM: invalid type parameter")
			}
			if seen[name.Value] {
				return fmt.Errorf("BASHPP-EGENERIC-PARAM: type parameter %s redeclared", name.Value)
			}
			seen[name.Value] = true
		}
	}
	return nil
}

// bashPPCheckEnumSwitches validates exhaustiveness when the function is
// declared, rather than waiting for a call. That gives an uncalled function
// the same fail-closed behavior as a compiled enum switch.
func (r *Runner) bashPPCheckEnumSwitches(d *syntax.BashPPFuncDecl) bool {
	types := make(map[string]string)
	for _, field := range d.Params {
		if field.FieldType == nil {
			continue
		}
		for _, name := range field.Names {
			types[name.Value] = field.FieldType.Value
		}
	}
	var checkStmts func([]*syntax.Stmt) bool
	checkStmts = func(stmts []*syntax.Stmt) bool {
		for _, stmt := range stmts {
			sw, ok := stmt.Cmd.(*syntax.BashPPSwitch)
			if !ok {
				continue
			}
			ident, isIdent := sw.Tag.(*syntax.BashPPIdent)
			typeName := ""
			if isIdent {
				typeName = types[ident.Name.Value]
			}
			if typ := r.bashPPTypes[typeName]; len(typ.members) > 0 {
				covered := make(map[string]bool)
				hasDefault := false
				for _, arm := range sw.Arms {
					if len(arm.Exprs) == 0 {
						hasDefault = true
					} else {
						for _, expr := range arm.Exprs {
							if member, ok := expr.(*syntax.BashPPIdent); ok {
								covered[member.Name.Value] = true
							}
						}
					}
				}
				if !hasDefault {
					for _, member := range typ.members {
						if !covered[member] {
							r.errf("BASHPP-EENUM-NONEXHAUSTIVE: switch on %s is missing member %s or a default arm\n", typeName, member)
							r.exit = exitStatus{code: 2}
							return false
						}
					}
				}
			}
			for _, arm := range sw.Arms {
				if !checkStmts(arm.Stmts) {
					return false
				}
			}
		}
		return true
	}
	return d.Body == nil || checkStmts(d.Body.Stmts)
}

func (r *Runner) bashPPMethodDecl(d *syntax.BashPPFuncDecl) {
	recv := d.Receiver
	for _, field := range append(append([]*syntax.BashPPField(nil), d.Params...), d.Results...) {
		for _, name := range field.Names {
			if recv.Name != nil && name.Value == recv.Name.Value && name.Value != "_" {
				r.errf("receiver %s redeclared in method signature\n", recv.Name.Value)
				r.exit.code = 2
				return
			}
		}
	}
	typ, ok := r.bashPPTypes[recv.RecvType.Value]
	if !ok {
		r.errf("invalid receiver type %s (type is not declared in this session)\n", recv.RecvType.Value)
		r.exit.code = 2
		return
	}
	if typ.alias {
		r.errf("invalid receiver type %s (cannot define methods on an alias)\n", recv.RecvType.Value)
		r.exit.code = 2
		return
	}
	wantParams := bashPPTypeParamCount(typ.typeParams)
	if len(recv.TypeParams) != wantParams {
		r.errf("BASHPP-EGENERIC-RECEIVER: %s expects %d receiver type parameter(s); got %d\n", recv.RecvType.Value, wantParams, len(recv.TypeParams))
		r.exit.code = 2
		return
	}
	seenParams := make(map[string]bool, len(recv.TypeParams))
	for _, param := range recv.TypeParams {
		if seenParams[param.Value] {
			r.errf("BASHPP-EGENERIC-RECEIVER: receiver type parameter %s redeclared\n", param.Value)
			r.exit.code = 2
			return
		}
		seenParams[param.Value] = true
	}
	if r.bashPPMethods == nil {
		r.bashPPMethods = make(map[string]map[string]*bashPPFunc)
	}
	methods := r.bashPPMethods[recv.RecvType.Value]
	if methods == nil {
		methods = make(map[string]*bashPPFunc)
		r.bashPPMethods[recv.RecvType.Value] = methods
	}
	if _, exists := methods[d.Name.Value]; exists {
		r.errf("method %s.%s redeclared in this session\n", recv.RecvType.Value, d.Name.Value)
		r.exit.code = 2
		return
	}
	var captured *bashPPScope
	if r.bashPPScope != nil {
		if r.bashPPGoSource {
			captured = r.bashPPScope
		} else {
			captured = r.bashPPScope.snapshot()
		}
	}
	methods[d.Name.Value] = &bashPPFunc{decl: d, scope: captured}
}

// bashPPLookupFunc resolves a call's callee to a callable function: a literal
// in callee position, a function declared with `func`, or a name bound to a
// closure. Selector calls first resolve a local typed root; only an absent
// local root may fall through to package-import evaluation.
//
// A literal is EVALUATED here, which is the correct moment: `func(n int) { …
// }(1)` captures the scope at the point of the call, exactly as the same
// literal bound to a name captures it at the point of the binding.
func (r *Runner) bashPPLookupFunc(c *syntax.BashPPCall) (*bashPPFunc, bool) {
	if pin := r.bashPPGoSourcePin; pin != nil && pin.call == c {
		// A launched task's callee was resolved once, in the parent. Re-running
		// a computed callee here would evaluate it twice; re-reading a variable
		// could find a different function than the `go` statement launched.
		if pin.bound != nil {
			return pin.bound, true
		}
		return r.bashPPClosure(pin.handle)
	}
	if r.bashPPGoSource && c.CalleeExpr != nil {
		if method, ok := c.CalleeExpr.(*syntax.BashPPSelectorExpr); ok && method.MethodValue && !r.bashPPNativeExpr(method.X) {
			fn, err := r.goSourceLocalMethod(method, len(c.Args) > 0)
			if err != nil {
				if !r.bashPPPanicking() {
					r.exit.fatal(err)
				}
				return nil, false
			}
			return fn, true
		}
		cell, err := r.goSourceValueCell(c.CalleeExpr)
		if err != nil {
			r.exit.fatal(err)
			return nil, false
		}
		return r.bashPPClosure(cell.vr.Str)
	}
	if c.FuncLit != nil {
		fn, _ := r.bashPPMakeClosure(c.FuncLit)
		return fn, true
	}
	if len(c.Fun) >= 2 {
		// A selector call resolves to its method first and is instantiated
		// second, exactly as a plain call is. A method's own type parameters
		// are independent of the receiver's, so the receiver bindings the
		// binding step recorded are already on the function when the
		// instantiation step adds the method's; see [Runner.bashPPInstantiateFunc].
		fn, ok := r.bashPPLookupSelectorFunc(c)
		if !ok {
			return nil, false
		}
		return r.bashPPInstantiateFunc(c, fn)
	}
	if len(c.Fun) != 1 {
		return nil, false
	}
	name := c.Fun[0].Value
	if fn, ok := r.bashPPFuncs[name]; ok {
		return r.bashPPInstantiateFunc(c, fn)
	}
	// A closure held in a variable is callable by that variable's name, which
	// is what makes `greet := func(…) { … }; greet(x)` and a returned factory
	// closure work without a second call syntax.
	if vr := r.lookupVar(name); vr.Kind == expand.String {
		fn, ok := r.bashPPClosure(vr.Str)
		if !ok {
			return nil, false
		}
		return r.bashPPInstantiateFunc(c, fn)
	}
	return nil, false
}

// bashPPLookupSelectorFunc resolves the `x.M`, `T.M` and `(*T).M` callee forms
// to the method they name, without instantiating it.
func (r *Runner) bashPPLookupSelectorFunc(c *syntax.BashPPCall) (*bashPPFunc, bool) {
	owner := c.Fun[0].Value
	// A local value is always considered before an import binding. This is
	// deterministic even when the import registry contains the same name.
	if cell := r.bashPPScope.lookup(owner); cell != nil {
		_, typeName := r.bashPPTypes[owner]
		if !typeName || cell.interfaceValue != nil || bashPPSelectorCellType(cell) != nil {
			return r.bashPPBindLocalSelector(c, cell)
		}
	}
	if len(c.Fun) != 2 {
		return nil, false
	}
	method := c.Fun[1].Value
	// T.M(v, ...) selects from T's method set; (*T).M(p, ...) records the
	// pointer method-expression spelling on the call node.
	if _, localType := r.bashPPTypes[owner]; localType {
		rootType := syntax.BashPPTypeExpr(&syntax.BashPPNamedType{Name: &syntax.Lit{Value: owner}})
		if c.PointerMethodExpr {
			rootType = &syntax.BashPPPointerType{Element: rootType}
		}
		sel := r.bashPPResolveSelection(rootType, method, true, false)
		if sel.ambiguous {
			r.errf("BASHPP-ESELECTOR-AMBIGUOUS: ambiguous selector %s.%s\n", bashPPTypeText(rootType), method)
			r.exit.code = 2
			return nil, false
		}
		fn := sel.method
		if fn == nil && sel.interfaceSpec == nil {
			r.errf("%s.%s is not in the method set of %s\n", owner, method, owner)
			r.exit.code = 2
			return nil, false
		}
		if len(c.Args) == 0 {
			r.errf("not enough arguments in call to method expression %s.%s\n", owner, method)
			r.exit.code = 2
			return nil, false
		}
		cell := r.bashPPCellForWord(c.Args[0])
		var actualType syntax.BashPPTypeExpr
		if cell != nil {
			actualType = cell.declType
			if actualType == nil {
				if meta := bashPPCellMeta(cell); meta != nil {
					actualType = meta.typ
				}
			}
		}
		if cell == nil || bashPPTypeText(actualType) != bashPPTypeText(rootType) {
			r.errf("cannot use first argument as %s receiver in %s.%s\n", owner, owner, method)
			r.exit.code = 2
			return nil, false
		}
		bound, ok := r.bashPPBindPromotedMethod(cell, method, sel, false)
		if !ok {
			return nil, false
		}
		bound.skipArgs = 1
		return bound, true
	}
	return nil, false
}

func bashPPSelectorCellType(cell *bashPPCell) syntax.BashPPTypeExpr {
	if cell == nil {
		return nil
	}
	typ := cell.declType
	if typ == nil {
		if meta := bashPPCellMeta(cell); meta != nil {
			typ = meta.typ
		}
	}
	if typ == nil && cell.typeName != "" {
		typ = &syntax.BashPPNamedType{Name: &syntax.Lit{Value: cell.typeName}}
		if cell.pointer {
			typ = &syntax.BashPPPointerType{Element: typ}
		}
	}
	return typ
}

// bashPPBindLocalSelector resolves x.a.b.M against the static field graph
// rooted at x, then performs the final bind against x's live storage. Keeping
// the entire edge path is what preserves addressability and nil diagnostics;
// materializing intermediate shell values would lose both.
func (r *Runner) bashPPBindLocalSelector(c *syntax.BashPPCall, root *bashPPCell) (*bashPPFunc, bool) {
	method := c.Fun[len(c.Fun)-1].Value
	if len(c.Fun) == 2 && root.interfaceValue != nil {
		return r.bashPPBindInterfaceMethod(root.interfaceValue, method)
	}
	typ := bashPPSelectorCellType(root)
	if typ == nil {
		r.errf("BASHPP-ESELECTOR-TYPE: local %s has no typed selector path\n", c.Fun[0].Value)
		r.exit.code = 2
		return nil, false
	}
	var edges []bashPPEmbedEdge
	for _, part := range c.Fun[1 : len(c.Fun)-1] {
		sel := r.bashPPResolveField(typ, part.Value)
		if sel.ambiguous {
			r.errf("BASHPP-ESELECTOR-AMBIGUOUS: ambiguous selector %s.%s\n", bashPPTypeText(typ), part.Value)
			r.exit.code = 2
			return nil, false
		}
		if len(sel.edges) == 0 {
			r.errf("BASHPP-ESELECTOR-UNKNOWN: %s has no field %q\n", bashPPTypeText(typ), part.Value)
			r.exit.code = 2
			return nil, false
		}
		edges = append(edges, sel.edges...)
		typ = sel.fieldType
	}
	sel := r.bashPPResolveSelection(typ, method, true, true)
	if sel.ambiguous {
		r.errf("BASHPP-ESELECTOR-AMBIGUOUS: ambiguous selector %s.%s\n", bashPPTypeText(typ), method)
		r.exit.code = 2
		return nil, false
	}
	if sel.method == nil && sel.interfaceSpec == nil {
		r.errf("type %s has no method %s\n", bashPPTypeText(typ), method)
		r.exit.code = 2
		return nil, false
	}
	sel.edges = append(edges, sel.edges...)
	return r.bashPPBindPromotedMethod(root, method, sel, true)
}

func (r *Runner) bashPPInstantiateFunc(c *syntax.BashPPCall, fn *bashPPFunc) (*bashPPFunc, bool) {
	params := fn.typeParams()
	// A function value that was already instantiated — by the context it was
	// bound in, see [Runner.bashPPContextualFuncValue] — is called at the
	// types it carries. Re-inferring them from this call's arguments would ask
	// a `func() int` value to determine `P` from no arguments at all.
	if len(c.TypeArgs) == 0 && bashPPFullyInstantiated(fn, params) {
		return fn, true
	}
	if len(params) == 0 {
		if len(c.TypeArgs) > 0 {
			r.errf("BASHPP-EGENERIC-ARITY: %s is not generic; got %d type argument(s)\n", fn.name(), len(c.TypeArgs))
			r.exit.code = 2
			return nil, false
		}
		return fn, true
	}
	want := bashPPTypeParamCount(params)
	if len(c.TypeArgs) > 0 && len(c.TypeArgs) != want {
		r.errf("BASHPP-EGENERIC-ARITY: %s expects %d type argument(s); got %d\n", fn.name(), want, len(c.TypeArgs))
		r.exit.code = 2
		return nil, false
	}
	bindings := make(map[string]syntax.BashPPTypeExpr, want)
	if len(c.TypeArgs) > 0 {
		if err := bashPPValidateConcreteTypeArgs(c.TypeArgs); err != nil {
			r.errf("%v\n", err)
			r.exit.code = 2
			return nil, false
		}
		i := 0
		for _, group := range params {
			for _, name := range group.Names {
				bindings[name.Value] = c.TypeArgs[i].ArgType
				i++
			}
		}
	} else if !r.bashPPInferTypeArgs(c, fn, bindings) {
		return nil, false
	}
	if len(bindings) != want {
		r.errf("BASHPP-EGENERIC-INFER: cannot infer type arguments for %s\n", fn.name())
		r.exit.code = 2
		return nil, false
	}
	if !r.bashPPCheckTypeConstraints(fn, params, bindings) {
		return nil, false
	}
	bound := *fn
	// A generic METHOD arrives here already carrying the receiver's bindings,
	// which the binding step read off the receiver value. Those names are
	// disjoint from the method's own — the parser rejects the collision — so
	// the two scopes merge rather than replace, and a signature mentioning
	// both substitutes both.
	if len(fn.typeArgs) > 0 {
		merged := make(map[string]syntax.BashPPTypeExpr, len(fn.typeArgs)+len(bindings))
		maps.Copy(merged, fn.typeArgs)
		maps.Copy(merged, bindings)
		bindings = merged
	}
	bound.typeArgs = bindings
	return &bound, true
}

func bashPPTypeParamCount(params []*syntax.BashPPTypeParam) int {
	var n int
	for _, param := range params {
		n += len(param.Names)
	}
	return n
}

func (r *Runner) bashPPInferTypeArgs(c *syntax.BashPPCall, fn *bashPPFunc, bindings map[string]syntax.BashPPTypeExpr) bool {
	// fn.params() rather than fn.decl.Params: on a generic method the receiver
	// type parameters are already bound, so substituting them first leaves
	// only the method's own parameters open to inference. A parameter typed
	// with a receiver parameter must therefore MATCH its receiver binding
	// instead of rebinding it.
	params := bashppParams(fn.params())
	args := c.Args
	if fn.skipArgs > 0 && len(args) >= fn.skipArgs {
		args = args[fn.skipArgs:]
	}
	for i, arg := range args {
		if i >= len(params) {
			break
		}
		actual := r.bashPPTypeOfArg(arg)
		if actual == nil {
			continue
		}
		if !r.bashPPInferTypeFromParam(params[i].typ, actual, bindings) {
			r.errf("BASHPP-EGENERIC-INFER: conflicting type inference for %s\n", fn.name())
			r.exit.code = 2
			return false
		}
	}
	return true
}

func (r *Runner) bashPPTypeOfArg(w *syntax.Word) syntax.BashPPTypeExpr {
	if cell := r.bashPPCellForWord(w); cell != nil {
		if cell.declType != nil {
			return cell.declType
		}
		if meta := bashPPCellMeta(cell); meta != nil && meta.typ != nil {
			return meta.typ
		}
		if cell.typeName != "" {
			name := &syntax.Lit{ValuePos: w.Pos(), ValueEnd: w.End(), Value: cell.typeName}
			typ := syntax.BashPPTypeExpr(&syntax.BashPPNamedType{Name: name})
			if cell.pointer {
				return &syntax.BashPPPointerType{Star: w.Pos(), Element: typ}
			}
			return typ
		}
	}
	value := r.bashPPExprValue(w)
	switch {
	case r.bashPPValueFits("int", value):
		return &syntax.BashPPNamedType{Name: &syntax.Lit{ValuePos: w.Pos(), ValueEnd: w.End(), Value: "int"}}
	case r.bashPPValueFits("bool", value):
		return &syntax.BashPPNamedType{Name: &syntax.Lit{ValuePos: w.Pos(), ValueEnd: w.End(), Value: "bool"}}
	default:
		return &syntax.BashPPNamedType{Name: &syntax.Lit{ValuePos: w.Pos(), ValueEnd: w.End(), Value: "string"}}
	}
}

func (r *Runner) bashPPInferTypeFromParam(param, actual syntax.BashPPTypeExpr, bindings map[string]syntax.BashPPTypeExpr) bool {
	switch p := param.(type) {
	case *syntax.BashPPTypeParamType:
		if prev := bindings[p.Name.Value]; prev != nil {
			return r.bashPPTypeAssignable(actual, prev) && r.bashPPTypeAssignable(prev, actual)
		}
		bindings[p.Name.Value] = actual
		return true
	case *syntax.BashPPPointerType:
		a, ok := actual.(*syntax.BashPPPointerType)
		return ok && r.bashPPInferTypeFromParam(p.Element, a.Element, bindings)
	case *syntax.BashPPCollectionType:
		a, ok := actual.(*syntax.BashPPCollectionType)
		if !ok || p.Kind != a.Kind {
			return false
		}
		if p.Kind == "map" && !r.bashPPInferTypeFromParam(p.Key, a.Key, bindings) {
			return false
		}
		return r.bashPPInferTypeFromParam(p.Element, a.Element, bindings)
	}
	return true
}

func (r *Runner) bashPPCheckTypeConstraints(fn *bashPPFunc, params []*syntax.BashPPTypeParam, bindings map[string]syntax.BashPPTypeExpr) bool {
	constraintBindings := bindings
	if len(fn.typeArgs) > 0 {
		constraintBindings = make(map[string]syntax.BashPPTypeExpr, len(fn.typeArgs)+len(bindings))
		maps.Copy(constraintBindings, fn.typeArgs)
		maps.Copy(constraintBindings, bindings)
	}
	for _, group := range params {
		constraint := bashPPSubstituteType(group.Constraint, constraintBindings)
		for _, name := range group.Names {
			arg := bindings[name.Value]
			if arg == nil {
				continue
			}
			if r.bashPPConstraintSatisfied(arg, constraint) {
				continue
			}
			r.errf("BASHPP-EGENERIC-CONSTRAINT: %s does not satisfy constraint for %s in %s\n", bashPPTypeText(arg), name.Value, fn.name())
			r.exit.code = 2
			return false
		}
	}
	return true
}

func (r *Runner) bashPPTypeSetSatisfied(arg, constraint syntax.BashPPTypeExpr) bool {
	switch c := constraint.(type) {
	case *syntax.BashPPNamedType:
		if c.Name.Value == "comparable" {
			return r.bashPPComparableType(arg, make(map[string]bool))
		}
		return r.bashPPTypeAssignable(arg, c)
	case *syntax.BashPPUnionType:
		for _, term := range c.Terms {
			if r.bashPPTypeSetSatisfied(arg, term) {
				return true
			}
		}
		return false
	case *syntax.BashPPApproxType:
		return bashPPTypeText(r.bashPPUnderlyingType(arg)) == bashPPTypeText(r.bashPPUnderlyingType(c.Term))
	default:
		return r.bashPPTypeAssignable(arg, c)
	}
}

func (r *Runner) bashPPComparableType(typ syntax.BashPPTypeExpr, seen map[string]bool) bool {
	switch x := typ.(type) {
	case *syntax.BashPPNamedType:
		name := x.Name.Value
		decl, found := r.bashPPTypes[name]
		if !found {
			return bashPPBuiltinType(name)
		}
		if seen[bashPPTypeText(x)] || decl.typeExpr == nil {
			return false
		}
		seen[bashPPTypeText(x)] = true
		defer delete(seen, bashPPTypeText(x))
		return r.bashPPComparableType(r.bashPPInstantiateNamedType(x), seen)
	case *syntax.BashPPPointerType:
		return true
	case *syntax.BashPPCollectionType:
		return x.Kind == "array" && r.bashPPComparableType(x.Element, seen)
	case *syntax.BashPPStructType:
		for _, field := range x.Fields {
			if field.FieldTypeExpr == nil || !r.bashPPComparableType(field.FieldTypeExpr, seen) {
				return false
			}
		}
		return true
	case *syntax.BashPPInterfaceType:
		return true
	case *syntax.BashPPTypeParamType:
		return true
	}
	return false
}

func (r *Runner) bashPPInstantiateNamedType(named *syntax.BashPPNamedType) syntax.BashPPTypeExpr {
	decl, ok := r.bashPPTypes[named.Name.Value]
	if !ok || len(decl.typeParams) == 0 {
		if ok && decl.typeExpr != nil {
			return decl.typeExpr
		}
		return named
	}
	want := bashPPTypeParamCount(decl.typeParams)
	if len(named.TypeArgs) != want {
		return named
	}
	bindings := make(map[string]syntax.BashPPTypeExpr, want)
	i := 0
	for _, group := range decl.typeParams {
		for _, name := range group.Names {
			bindings[name.Value] = named.TypeArgs[i].ArgType
			i++
		}
	}
	return bashPPSubstituteType(decl.typeExpr, bindings)
}

func (r *Runner) bashPPValidateNamedTypeArgs(named *syntax.BashPPNamedType) error {
	decl, ok := r.bashPPTypes[named.Name.Value]
	if !ok {
		return nil
	}
	want := bashPPTypeParamCount(decl.typeParams)
	if want == 0 {
		if len(named.TypeArgs) > 0 {
			return fmt.Errorf("BASHPP-EGENERIC-ARITY: %s is not generic; got %d type argument(s)", named.Name.Value, len(named.TypeArgs))
		}
		return nil
	}
	if len(named.TypeArgs) != want {
		return fmt.Errorf("BASHPP-EGENERIC-ARITY: %s expects %d type argument(s); got %d", named.Name.Value, want, len(named.TypeArgs))
	}
	if err := bashPPValidateConcreteTypeArgs(named.TypeArgs); err != nil {
		return err
	}
	bindings := make(map[string]syntax.BashPPTypeExpr, want)
	i := 0
	for _, group := range decl.typeParams {
		for _, param := range group.Names {
			bindings[param.Value] = named.TypeArgs[i].ArgType
			i++
		}
	}
	i = 0
	for _, group := range decl.typeParams {
		constraint := bashPPSubstituteType(group.Constraint, bindings)
		for _, param := range group.Names {
			arg := named.TypeArgs[i].ArgType
			if !r.bashPPConstraintSatisfied(arg, constraint) {
				return fmt.Errorf("BASHPP-EGENERIC-CONSTRAINT: %s does not satisfy constraint for %s in %s", bashPPTypeText(arg), param.Value, named.Name.Value)
			}
			i++
		}
	}
	return nil
}

func bashPPValidateConcreteTypeArgs(args []*syntax.BashPPTypeArg) error {
	for _, arg := range args {
		if arg == nil || arg.ArgType == nil {
			return fmt.Errorf("BASHPP-EGENERIC-ARG: missing concrete type argument")
		}
		if err := bashPPValidateConcreteTypeExpr(arg.ArgType); err != nil {
			return err
		}
	}
	return nil
}

func bashPPValidateConcreteTypeExpr(typ syntax.BashPPTypeExpr) error {
	switch x := typ.(type) {
	case *syntax.BashPPUnionType, *syntax.BashPPApproxType:
		return fmt.Errorf("BASHPP-EGENERIC-ARG: %s is a constraint expression, not a concrete type", bashPPTypeText(typ))
	case *syntax.BashPPNamedType:
		return bashPPValidateConcreteTypeArgs(x.TypeArgs)
	case *syntax.BashPPCollectionType:
		if x.Key != nil {
			if err := bashPPValidateConcreteTypeExpr(x.Key); err != nil {
				return err
			}
		}
		if x.Element != nil {
			return bashPPValidateConcreteTypeExpr(x.Element)
		}
	case *syntax.BashPPPointerType:
		if x.Element != nil {
			return bashPPValidateConcreteTypeExpr(x.Element)
		}
	case *syntax.BashPPStructType:
		for _, field := range x.Fields {
			if err := bashPPValidateConcreteTypeExpr(field.FieldTypeExpr); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Runner) bashPPConstraintSatisfied(arg, constraint syntax.BashPPTypeExpr) bool {
	// `any` is satisfied by every type, so there is nothing to decide and no
	// need for the argument to be a concrete type. gosource lowers `any` to a
	// literal empty interface, which would otherwise be routed through the
	// implements check and reject an uninstantiated type parameter — the form
	// a generic type takes inside its own declaration, as in the Tour's
	// `type List[T any] struct { next *List[T]; val T }`. A type parameter
	// used as a type argument is checked against a real constraint where the
	// generic type is instantiated, which is the first point a concrete type
	// exists to check.
	if bashPPEmptyInterfaceType(constraint) {
		return true
	}
	switch c := constraint.(type) {
	case *syntax.BashPPNamedType:
		switch c.Name.Value {
		case "any":
			return true
		case "comparable":
			return r.bashPPComparableType(arg, make(map[string]bool))
		default:
			if iface, ok := r.bashPPInterfaceType(c); ok {
				return r.bashPPImplements(arg, iface) == nil && r.bashPPInterfaceTypeSetSatisfied(arg, iface, make(map[*syntax.BashPPInterfaceType]bool))
			}
			return r.bashPPTypeAssignable(arg, c)
		}
	case *syntax.BashPPInterfaceType:
		return r.bashPPImplements(arg, c) == nil && r.bashPPInterfaceTypeSetSatisfied(arg, c, make(map[*syntax.BashPPInterfaceType]bool))
	case *syntax.BashPPUnionType, *syntax.BashPPApproxType:
		return r.bashPPTypeSetSatisfied(arg, constraint)
	}
	return false
}

// bashPPEmptyInterfaceType reports whether typ is spelled as an interface with
// no methods and no type terms, that is, `any`.
func bashPPEmptyInterfaceType(typ syntax.BashPPTypeExpr) bool {
	iface, ok := typ.(*syntax.BashPPInterfaceType)
	return ok && len(iface.Elems) == 0 && len(iface.Methods) == 0
}

func (r *Runner) bashPPCellForWord(w *syntax.Word) *bashPPCell {
	if w == nil || len(w.Parts) != 1 || r.bashPPScope == nil {
		return nil
	}
	lit, ok := w.Parts[0].(*syntax.Lit)
	if !ok || !syntax.BashPPValidIdent(lit.Value) {
		return nil
	}
	return r.bashPPScope.lookup(lit.Value)
}

// bashPPPointerCell wraps a pointer value in an anonymous cell. `&x`, `&T{…}`
// and `new(T)` name storage that no variable holds, so there is no cell to
// carry their provenance to a parameter; this makes one.
func bashPPPointerCell(ptr *bashPPPointer) *bashPPCell {
	// A pointer variable's own shell binding is a set, empty string: the
	// pointer travels in pointerValue, not in the text. An anonymous pointer
	// cell must look the same, or reading it reports an unset name.
	vr := expand.Variable{Set: true, Kind: expand.String}
	if ptr == nil {
		return &bashPPCell{pointer: true, nilPointer: true, vr: vr}
	}
	cell := &bashPPCell{pointer: true, pointerValue: ptr, vr: vr, declType: &syntax.BashPPPointerType{Element: ptr.elem}}
	if named, ok := ptr.elem.(*syntax.BashPPNamedType); ok && named.Name != nil {
		cell.typeName = named.Name.Value
	}
	return cell
}

// bashPPStructuredArgCell reports the provenance cell of a call argument that
// has no scalar form — a struct, collection, pointer or interface value, plus
// the `&x` / `&T{…}` / `new(T)` and composite-literal spellings that produce
// one without naming a variable. A scalar argument returns (nil, nil) so it
// keeps being evaluated as an expression rather than passed by name.
func (r *Runner) bashPPStructuredArgCell(w *syntax.Word, expr syntax.BashPPExpr) (*bashPPCell, error) {
	if cell, handled, err := r.goSourceCallableCell(expr); handled {
		return cell, err
	}
	switch x := expr.(type) {
	case *syntax.BashPPAddressExpr, *syntax.BashPPNewExpr:
		ptr, err := r.bashPPPointerExprValue(expr)
		if err != nil {
			return nil, err
		}
		return bashPPPointerCell(ptr), nil
	case *syntax.BashPPConvertExpr:
		// `f([]byte(s))`: a conversion whose result is a collection travels as
		// the cell holding it; see bashPPConvertCollectionCell in
		// bashpp_collection_convert.go. Scalar conversions report false.
		cell, handled, err := r.bashPPConvertCollectionCell(x)
		if !handled {
			return nil, nil
		}
		return cell, err
	case *syntax.BashPPCompositeLit:
		if x.LitType == nil {
			return nil, nil
		}
		value, meta, err := r.bashPPEvalComposite(x, nil)
		if err != nil {
			return nil, err
		}
		cell := &bashPPCell{declType: x.LitType}
		if named, ok := x.LitType.(*syntax.BashPPNamedType); ok && named.Name != nil {
			cell.typeName = named.Name.Value
		}
		bashPPStoreCellValue(cell, value, meta)
		return cell, nil
	case *syntax.BashPPDerefExpr, *syntax.BashPPIndexExpr, *syntax.BashPPSelectorExpr, *syntax.BashPPSliceExpr:
		// `f(*p)`, `f(xs[0])`, `f(v.Inner)`, `f(xs[1:])`: a read that yields structured
		// storage is passed as the value it is. A scalar read has no metadata
		// and is left to the scalar evaluator, which also owns the diagnostic
		// when the read itself fails.
		value, meta, err := r.bashPPReadExpr(expr)
		if err != nil || meta == nil {
			return nil, nil
		}
		cell := &bashPPCell{declType: meta.typ}
		if named, ok := meta.typ.(*syntax.BashPPNamedType); ok && named.Name != nil {
			cell.typeName = named.Name.Value
		}
		bashPPStoreCellValue(cell, value, meta)
		return cell, nil
	}
	if id, ok := expr.(*syntax.BashPPIdent); ok && r.bashPPScope != nil {
		if cell := r.bashPPScope.lookup(id.Name.Value); bashPPStructuredCell(cell) {
			return cell, nil
		}
		return nil, nil
	}
	if cell := r.bashPPCellForWord(w); bashPPStructuredCell(cell) {
		return cell, nil
	}
	return nil, nil
}

// bashPPStructuredCell reports whether a binding holds a value with no scalar
// spelling, so that passing or returning it has to carry the cell itself.
func bashPPStructuredCell(cell *bashPPCell) bool {
	return cell != nil && (cell.pointer || cell.interfaceValue != nil || cell.vr.Kind == expand.Object)
}

// bashPPCellForArg resolves the provenance cell of one call argument. A named
// operand is the variable itself, so a pointer argument keeps its identity.
// `&x` and `new(T)` name freshly taken storage that no variable holds; the
// pointer is wrapped in an anonymous cell so it travels the same parameter
// binding path a named pointer does, instead of arriving as a nil pointer.
func (r *Runner) bashPPCellForArg(w *syntax.Word, expr syntax.BashPPExpr) *bashPPCell {
	if cell := r.bashPPCellForWord(w); cell != nil {
		return cell
	}
	switch expr.(type) {
	case *syntax.BashPPAddressExpr, *syntax.BashPPNewExpr:
	default:
		return nil
	}
	ptr, err := r.bashPPPointerExprValue(expr)
	if err != nil {
		r.errf("%s%v\n", r.bashErrPrefix(expr.Pos()), err)
		r.exit = exitStatus{code: 2}
		return nil
	}
	if ptr == nil {
		return nil
	}
	return bashPPPointerCell(ptr)
}

// bashPPGoSourceArgCell gives a Go-source argument that names no variable its
// own provenance cell. A Go call's argument is an expression, so `do(21)` has
// to arrive carrying the kind and type of 21 — without that the callee sees
// only text, and an interface parameter has no dynamic type to switch on.
// Classic Bash++ calls are untouched: there a bare word is its own literal.
func (r *Runner) bashPPGoSourceArgCell(w *syntax.Word, expr syntax.BashPPExpr) *bashPPCell {
	if !r.bashPPGoSource || expr == nil {
		return nil
	}
	if structured, err := r.bashPPStructuredArgCell(w, expr); err == nil && structured != nil {
		return structured
	}
	value, err := r.bashPPEvalScalarExpr(expr)
	if err != nil || value.value == nil || value.value.Kind() == constant.Unknown {
		return nil
	}
	cell := &bashPPCell{
		vr:         expand.Variable{Set: true, Kind: expand.String, Str: bashPPScalarString(value.value)},
		scalarKind: value.value.Kind(),
	}
	if value.typ != "" {
		cell.typeName = value.typ
		cell.declType = &syntax.BashPPNamedType{Name: &syntax.Lit{Value: value.typ}}
	}
	return cell
}

// bashPPTypedCallArgs binds the positioned Go-form arguments of a call whose
// result is consumed as a value. Each argument is evaluated once: a structured
// operand travels through its provenance cell, everything else through the
// scalar evaluator, so `Abs(v)` on a struct and `Abs(x*2)` on a number both
// reach the callee instead of the second form forcing the first to be refused
// as "not a scalar".
func (r *Runner) bashPPTypedCallArgs(call *syntax.BashPPCall, fn *bashPPFunc) (result []string, success bool, failure error) {
	defer func() {
		if success && !r.goSourceFinalizeReceiver(fn) {
			result = nil
			success = false
			failure = errBashPPScalarInterrupted
		}
	}()
	if r.bashPPGoSource && !call.Ellipsis.IsValid() {
		return r.goSourceCallArguments(call, fn)
	}
	if len(call.ArgExprs) != len(call.Args) {
		return nil, false, fmt.Errorf("BASHPP-EEXPR-CALL: inconsistent positioned scalar arguments")
	}
	args := make([]string, len(call.ArgExprs))
	cells := make([]*bashPPCell, len(call.ArgExprs))
	channels := make([]*bashPPChannel, len(call.ArgExprs))
	interfaces := make([]*bashPPInterfaceValue, len(call.ArgExprs))
	for i, expr := range call.ArgExprs {
		if r.bashPPGoSource {
			if cell, handled, err := r.goSourceChannelValueCell(expr); handled {
				if err != nil {
					return nil, false, err
				}
				if cell.channel != nil && cell.channelOwner != r.bashPPConcurrent {
					return nil, false, fmt.Errorf("channel belongs to another task group")
				}
				channels[i], cells[i], args[i] = cell.channel, cell, cell.vr.String()
				continue
			}
		}
		structured, err := r.bashPPStructuredArgCell(call.Args[i], expr)
		if err != nil {
			return nil, false, err
		}
		if structured != nil {
			copied := bashPPCopyAssignmentCell(structured)
			// A value copy never carries direct channel authority; that is
			// restored only from separately checked owner provenance.
			copied.channel, copied.channelOwner = nil, nil
			cells[i], interfaces[i] = copied, copied.interfaceValue
			args[i] = r.bashPPExprValue(call.Args[i])
			if _, ok := r.bashPPClosure(copied.vr.Str); ok {
				args[i] = copied.vr.Str
			}
			continue
		}
		value, err := r.bashPPEvalScalarExpr(expr)
		if err != nil {
			return nil, false, err
		}
		text := bashPPScalarString(value.value)
		cell := &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: text}, scalarKind: value.value.Kind()}
		if value.typ != "" {
			cell.declType = &syntax.BashPPNamedType{Name: &syntax.Lit{Value: value.typ}}
		}
		cells[i], args[i] = cell, text
	}
	r.bashPPCallInterfaces = interfaces
	bound, ok := r.bashPPBindCall(fn, args, channels, cells, nil, len(args))
	return bound, ok, nil
}

func (r *Runner) bashPPBindInterfaceMethod(iv *bashPPInterfaceValue, method string) (*bashPPFunc, bool) {
	if iv == nil || iv.nilIface {
		r.errf("nil interface has no method %s\n", method)
		r.exit.code = 2
		return nil, false
	}
	if iv.cell == nil {
		r.errf("interface value has no dynamic receiver for method %s\n", method)
		r.exit.code = 2
		return nil, false
	}
	sel := r.bashPPResolveSelection(iv.dynamic, method, true, false)
	if sel.ambiguous {
		r.errf("BASHPP-ESELECTOR-AMBIGUOUS: ambiguous selector %s.%s\n", bashPPTypeText(iv.dynamic), method)
		r.exit.code = 2
		return nil, false
	}
	if sel.method == nil && sel.interfaceSpec == nil {
		r.errf("type %s has no method %s\n", bashPPTypeText(iv.dynamic), method)
		r.exit.code = 2
		return nil, false
	}
	return r.bashPPBindPromotedMethod(iv.cell, method, sel, false)
}

func (r *Runner) bashPPBindMethod(cell *bashPPCell, method string, addressable bool) (*bashPPFunc, bool) {
	fn := r.bashPPMethods[cell.typeName][method]
	if fn == nil {
		r.errf("type %s has no method %s\n", cell.typeName, method)
		r.exit.code = 2
		return nil, false
	}
	ptrRecv := fn.decl.Receiver.Pointer
	if ptrRecv && !cell.pointer && !addressable {
		r.errf("method %s has pointer receiver and requires an addressable %s\n", method, cell.typeName)
		r.exit.code = 2
		return nil, false
	}
	if !ptrRecv && cell.pointer && cell.nilPointer {
		r.errf("value method %s called using nil *%s pointer\n", method, cell.typeName)
		r.exit.code = 2
		return nil, false
	}
	bound := *fn
	bound.typeArgs = bashPPMethodTypeArgs(fn, cell)
	if ptrRecv {
		bound.receiver = cell
	} else {
		copyCell := *cell
		if cell.pointer && cell.pointerValue != nil {
			value, meta, typ, err := cell.pointerValue.read()
			if err != nil {
				r.errf("%v\n", err)
				r.exit.code = 2
				return nil, false
			}
			copyCell = bashPPCell{declType: typ, typeName: cell.typeName}
			if r.bashPPGoSource && meta != nil {
				copyCell.vr = expand.Variable{Set: true, Kind: expand.Object, Obj: value}
				copyCell.valueMeta = meta
				copyCell.object = &bashPPObjectIdentity{collection: meta}
			} else {
				bashPPStoreCellValue(&copyCell, value, meta)
			}
		}
		if copyCell.vr.Kind == expand.Object {
			if meta := bashPPCellMeta(&copyCell); bashPPValueMeta(meta) {
				value, copiedMeta := bashPPCopyArrayValue(copyCell.vr.Obj, meta)
				if r.bashPPGoSource {
					// A typed value copy copies fields, not referenced storage.
					copyCell.vr = expand.Variable{Set: true, Kind: expand.Object, Obj: value}
				} else {
					copyCell.vr = expand.NewObject(value)
				}
				copyCell.valueMeta = copiedMeta
				copyCell.object = &bashPPObjectIdentity{collection: copiedMeta}
			}
		}
		copyCell.pointer, copyCell.nilPointer = false, false
		bound.receiver = &copyCell
	}
	return &bound, true
}

func bashPPMethodTypeArgs(fn *bashPPFunc, cell *bashPPCell) map[string]syntax.BashPPTypeExpr {
	if fn == nil || fn.decl == nil || fn.decl.Receiver == nil || len(fn.decl.Receiver.TypeParams) == 0 || cell == nil {
		return nil
	}
	return bashPPMethodTypeBindings(fn, cell.declType)
}

func bashPPMethodTypeBindings(fn *bashPPFunc, typ syntax.BashPPTypeExpr) map[string]syntax.BashPPTypeExpr {
	if fn == nil || fn.decl == nil || fn.decl.Receiver == nil || len(fn.decl.Receiver.TypeParams) == 0 {
		return nil
	}
	if pointer, ok := typ.(*syntax.BashPPPointerType); ok {
		typ = pointer.Element
	}
	named, ok := typ.(*syntax.BashPPNamedType)
	if !ok || len(named.TypeArgs) != len(fn.decl.Receiver.TypeParams) {
		return nil
	}
	bindings := make(map[string]syntax.BashPPTypeExpr, len(named.TypeArgs))
	for i, param := range fn.decl.Receiver.TypeParams {
		bindings[param.Value] = named.TypeArgs[i].ArgType
	}
	return bindings
}

// bashPPCallArgValues evaluates each call argument to a single string. Values
// follow the dialect's existing convention that a bare word is its own literal
// and an expansion is expanded, exactly as a `:=` right-hand side does, so
// `f($x)` passes the value of x while `f(x)` passes the string "x".
//
// A trailing `...` spreads its argument instead of passing it: `f(xs...)`
// hands the elements of xs to a variadic parameter, one argument each.
func (r *Runner) bashPPCallArgValues(c *syntax.BashPPCall) []string {
	values, _ := r.bashPPCallArgValuesWithCells(c)
	return values
}

func (r *Runner) bashPPCallArgValuesWithCells(c *syntax.BashPPCall) ([]string, []*bashPPCell) {
	args := make([]string, 0, len(c.Args))
	cells := make([]*bashPPCell, 0, len(c.Args))
	for i, word := range c.Args {
		if c.Ellipsis.IsValid() && i == len(c.Args)-1 {
			values := r.bashPPSpreadValues(word)
			args = append(args, values...)
			cells = append(cells, make([]*bashPPCell, len(values))...)
			break
		}
		var argExpr syntax.BashPPExpr
		if i < len(c.ArgExprs) {
			argExpr = c.ArgExprs[i]
		}
		var copied *bashPPCell
		cell := r.bashPPCellForArg(word, argExpr)
		if cell == nil {
			cell = r.bashPPGoSourceArgCell(word, argExpr)
		}
		if cell != nil {
			copied = bashPPCopyAssignmentCell(cell)
			// Direct channel authority is restored only from the separately checked
			// owner provenance. A value copy cannot grant an unverified capability.
			copied.channel = nil
			copied.channelOwner = nil
		}
		value := r.bashPPExprValue(word)
		if r.bashPPGoSource && copied != nil && copied.vr.Kind == expand.String {
			value = copied.vr.Str
		}
		args = append(args, value)
		cells = append(cells, copied)
	}
	return args, cells
}

// bashPPSpreadValues expands the `xs...` argument into the values it passes.
//
// The spread reads the NAMED variable rather than the word's expansion because
// that is the only spelling that can carry more than one value: `$xs` has
// already been joined into a single string by the time an expansion is done,
// so spreading it would pass one argument no matter how many elements it held.
// An indexed variable — which is what a variadic parameter binds — spreads
// element by element; a scalar spreads as itself; an unset name spreads as
// nothing, so forwarding an empty variadic parameter passes zero arguments.
func (r *Runner) bashPPSpreadValues(w *syntax.Word) []string {
	if len(w.Parts) == 1 {
		if lit, ok := w.Parts[0].(*syntax.Lit); ok && syntax.BashPPValidIdent(lit.Value) {
			vr := r.lookupVar(lit.Value)
			switch {
			case !vr.IsSet():
				return nil
			case vr.Kind == expand.Indexed:
				return append([]string(nil), vr.List...)
			case vr.Kind == expand.String:
				return []string{vr.Str}
			}
		}
	}
	return []string{r.bashPPExprValue(w)}
}

// bashPPCallValues evaluates a call's arguments for fn, refusing a spread the
// callee cannot accept. Go rejects `f(xs...)` when f is not variadic, and so
// does this: silently passing the elements would make the two spellings mean
// the same thing and hide the mistake.
func (r *Runner) bashPPCallValues(c *syntax.BashPPCall, fn *bashPPFunc) (result []string, success bool) {
	defer func() {
		if success && !r.goSourceFinalizeReceiver(fn) {
			result = nil
			success = false
		}
	}()
	if c.Ellipsis.IsValid() && !bashppVariadic(fn.params()) {
		r.errf("cannot use ... in call to non-variadic %s\n", fn.name())
		r.exit = exitStatus{code: 2}
		return nil, false
	}
	if r.bashPPGoSource && len(c.ArgExprs) == len(c.Args) {
		args, ok, err := r.goSourceCallArguments(c, fn)
		if err != nil {
			if !errors.Is(err, errBashPPScalarInterrupted) {
				r.errf("%s%v\n", r.bashErrPrefix(c.Pos()), err)
				r.exit = exitStatus{code: 2}
			}
			r.bashPPShortFailureSeq++
			return nil, false
		}
		return args, ok
	}
	r.bashPPCallCells = nil
	r.bashPPCallChannels = nil
	if required := bashppRequiredAfterDefault(fn.params()); required != "" {
		r.errf("BASHPP-EDEFAULT-ORDER: required parameter %q follows a default parameter\n", required)
		r.exit = exitStatus{code: 2}
		return nil, false
	}
	if c.Ellipsis.IsValid() && !bashppVariadic(fn.params()) {
		r.errf("cannot use ... in call to non-variadic %s\n", fn.name())
		r.exit = exitStatus{code: 2}
		return nil, false
	}
	// Capture typed argument provenance before evaluating any argument. An
	// argument expansion may itself invoke a Bash++ function, whose ephemeral
	// call metadata must not overwrite the authority belonging to this call.
	channels := make([]*bashPPChannel, len(c.Args))
	interfaces := make([]*bashPPInterfaceValue, len(c.Args))
	for i, word := range c.Args {
		channel, owner := r.bashPPDirectChannel(word)
		if owner == r.bashPPConcurrent {
			channels[i] = channel
		}
		if cell := r.bashPPCellForWord(word); cell != nil {
			interfaces[i] = cell.interfaceValue
		}
	}
	args, cells := r.bashPPCallArgValuesWithCells(c)
	r.bashPPCallChannels = nil
	r.bashPPCallInterfaces = nil
	names := c.ArgNames
	positional := len(args) - len(names)
	if fn.skipArgs > 0 {
		args = args[fn.skipArgs:]
		channels = channels[fn.skipArgs:]
		interfaces = interfaces[fn.skipArgs:]
		cells = cells[fn.skipArgs:]
		positional -= fn.skipArgs
	}
	if len(names) > 0 || bashppHasDefaults(fn.params()) {
		r.bashPPCallInterfaces = interfaces
		return r.bashPPBindCall(fn, args, channels, cells, names, positional)
	}
	r.bashPPCallChannels = channels
	r.bashPPCallInterfaces = interfaces
	r.bashPPCallCells = cells
	return args, true
}

// bashPPBindCall applies the Bash# positional-then-named/default binding
// contract. It is deliberately entered only when a call uses a name or the
// signature has a default, so the established P3 arity diagnostics remain
// byte-for-byte unchanged for ordinary Go-form calls.
func (r *Runner) bashPPBindCall(fn *bashPPFunc, supplied []string, suppliedChannels []*bashPPChannel, suppliedCells []*bashPPCell, names []*syntax.Lit, positional int) ([]string, bool) {
	params := bashppParams(fn.params())
	fail := func(format string, args ...any) ([]string, bool) {
		r.bashPPCallChannels = nil
		r.bashPPCallInterfaces = nil
		r.bashPPCallCells = nil
		r.errf(format, args...)
		r.exit = exitStatus{code: 2}
		return nil, false
	}
	if positional > len(params) {
		return fail("BASHPP-EARG-COUNT: %s accepts at most %d arguments; got %d\n",
			fn.name(), len(params), len(supplied))
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name.Value] {
			return fail("BASHPP-EKWARG-DUPLICATE: argument %q is supplied more than once\n", name.Value)
		}
		seen[name.Value] = true
	}
	byName := make(map[string]int, len(params))
	for i, param := range params {
		if param.name != "" {
			byName[param.name] = i
		}
	}
	for _, name := range names {
		if _, ok := byName[name.Value]; !ok {
			return fail("BASHPP-EKWARG-UNKNOWN: %s has no parameter named %q\n", fn.name(), name.Value)
		}
	}
	values := make([]string, len(params))
	channels := make([]*bashPPChannel, len(params))
	interfaces := make([]*bashPPInterfaceValue, len(params))
	cells := make([]*bashPPCell, len(params))
	bound := make([]bool, len(params))
	for i := 0; i < positional; i++ {
		values[i], bound[i] = supplied[i], true
		if i < len(suppliedCells) {
			cells[i] = suppliedCells[i]
		}
		if i < len(suppliedChannels) {
			channels[i] = suppliedChannels[i]
		}
		if i < len(r.bashPPCallInterfaces) {
			interfaces[i] = r.bashPPCallInterfaces[i]
		}
	}
	for i, name := range names {
		index := byName[name.Value]
		if bound[index] {
			return fail("BASHPP-EARG-DUPLICATE-BINDING: parameter %q is supplied positionally and by name\n", name.Value)
		}
		values[index], bound[index] = supplied[positional+i], true
		if source := positional + i; source < len(suppliedCells) {
			cells[index] = suppliedCells[source]
		}
		if source := positional + i; source < len(suppliedChannels) {
			channels[index] = suppliedChannels[source]
		}
		if source := positional + i; source < len(r.bashPPCallInterfaces) {
			interfaces[index] = r.bashPPCallInterfaces[source]
		}
	}
	for i, param := range params {
		if bound[i] {
			continue
		}
		if param.defaultValue != nil {
			if cell := r.bashPPCellForWord(param.defaultValue); cell != nil {
				cells[i] = bashPPCopyAssignmentCell(cell)
			}
			values[i], bound[i] = r.bashPPExprValue(param.defaultValue), true
			if channel, owner := r.bashPPDirectChannel(param.defaultValue); owner == r.bashPPConcurrent {
				channels[i] = channel
			}
			continue
		}
		return fail("BASHPP-EARG-MISSING: %s requires argument %q\n", fn.name(), param.name)
	}
	r.bashPPCallChannels = channels
	r.bashPPCallInterfaces = interfaces
	r.bashPPCallCells = cells
	return values, true
}

// bashPPExprValue evaluates the small expression vocabulary admitted by the
// P3-A call grammar. A bare identifier is an expression, not a literal word;
// resolve it through the existing lexical/shell environment when it names a
// live binding. Other bare words remain string literals, preserving the
// convenient `f(hello)` spelling for an unquoted string argument.
func (r *Runner) bashPPExprValue(w *syntax.Word) string {
	if len(w.Parts) == 1 {
		if lit, ok := w.Parts[0].(*syntax.Lit); ok && syntax.BashPPValidIdent(lit.Value) {
			if vr := r.lookupVar(lit.Value); vr.IsSet() {
				return vr.String()
			}
		}
	}
	return r.literal(w)
}

// bashPPRewriteAssign turns a bare identifier RHS into the equivalent short
// parameter expansion for the duration of assignment evaluation. Shell
// syntax has no bare-expression form, while Go-form function bodies do:
// `result = input` must copy input's value rather than the literal text.
func (r *Runner) bashPPRewriteAssign(as *syntax.Assign) *syntax.Assign {
	if r.bashPPFuncActive == 0 || as == nil || as.Value == nil || len(as.Value.Parts) != 1 {
		return as
	}
	lit, ok := as.Value.Parts[0].(*syntax.Lit)
	if !ok || !syntax.BashPPValidIdent(lit.Value) || !r.lookupVar(lit.Value).IsSet() {
		return as
	}
	cp := *as
	cp.Value = &syntax.Word{Parts: []syntax.WordPart{&syntax.ParamExp{
		Dollar: lit.Pos(), Short: true, Param: lit,
	}}}
	return &cp
}

// bashPPRewriteCommandArgs gives bare identifiers their Go-form expression
// meaning inside a typed function body. The command name remains a shell word;
// subsequent words which name live lexical bindings become short parameter
// expansions for this invocation only.
func (r *Runner) bashPPRewriteCommandArgs(args []*syntax.Word) []*syntax.Word {
	if len(args) < 2 {
		return args
	}
	out := append([]*syntax.Word(nil), args[:1]...)
	for i := 1; i < len(args); {
		combined := &syntax.Word{Parts: append([]syntax.WordPart(nil), args[i].Parts...)}
		bestValue, bestEnd := "", -1
		for j := i; j < len(args); j++ {
			if j > i {
				if args[j-1].End() != args[j].Pos() {
					break
				}
				combined.Parts = append(combined.Parts, args[j].Parts...)
			}
			if value, ok := r.bashPPResolveWord(combined); ok {
				bestValue, bestEnd = value, j+1
			}
		}
		if bestEnd >= 0 {
			out = append(out, &syntax.Word{Parts: []syntax.WordPart{&syntax.SglQuoted{
				Left: args[i].Pos(), Right: args[bestEnd-1].End(), Value: bestValue,
			}}})
			i = bestEnd
			continue
		}
		word := args[i]
		if r.bashPPFuncActive == 0 {
			out = append(out, word)
			i++
			continue
		}
		if len(word.Parts) != 1 {
			out = append(out, word)
			i++
			continue
		}
		lit, ok := word.Parts[0].(*syntax.Lit)
		if !ok || !syntax.BashPPValidIdent(lit.Value) || !r.lookupVar(lit.Value).IsSet() {
			out = append(out, word)
			i++
			continue
		}
		out = append(out, &syntax.Word{Parts: []syntax.WordPart{&syntax.ParamExp{
			Dollar: lit.Pos(), Short: true, Param: lit,
		}}})
		i++
	}
	return out
}

// bashPPInvoke calls a typed function with already-evaluated arguments and
// returns its result values. The exit status is set on r: an explicit `return`
// of values succeeds with status 0, while a result-less function keeps the
// body's last status (or the code named by a bash-style `return n`).
func (r *Runner) bashPPInvoke(ctx context.Context, fn *bashPPFunc, args []string) []string {
	r.bashPPResultCells = nil
	callChannels := r.bashPPCallChannels
	callInterfaces := r.bashPPCallInterfaces
	callCells := r.bashPPCallCells
	callSpread := r.bashPPCallSpread
	r.bashPPCallCells = nil
	r.bashPPCallChannels = nil
	r.bashPPCallInterfaces = nil
	r.bashPPCallSpread = false
	if fn.native != nil {
		return r.goSourceInvokeNative(ctx, fn, args, callCells)
	}
	if fn.rangeYield != nil {
		return r.goSourceInvokeRangeYield(ctx, fn, args, callCells)
	}
	if fn.collectYield != nil {
		return r.goSourceInvokeCollectYield(fn, args, callCells)
	}
	if fn.decl != nil && fn.decl.Agentic != nil && !r.bashPPAgentic {
		r.bashPPAgenticCallError(r.curStmtPos, fn.name())
		return nil
	}
	params := bashppParams(fn.params())
	// A function named as an argument becomes a function value before the
	// type check looks at it, so a parameter declared `func() int` accepts
	// `f` the same way it accepts a closure; see [Runner.bashPPBindFuncValueArgs].
	args, bound := r.bashPPBindFuncValueArgs(params, args)
	if !bound {
		return nil
	}
	if !r.bashPPCheckArgs(fn, params, args, callSpread) {
		return nil
	}
	if !r.bashPPCheckChannelArgs(fn, params, callChannels, callCells) {
		return nil
	}
	if limit, _ := strconv.Atoi(r.envGet("FUNCNEST")); limit > 0 && len(r.callStack) >= limit {
		r.errf("%s: maximum function nesting level exceeded (%d)\n", fn.name(), limit)
		r.exit.code = 1
		return nil
	}

	// Save the caller's execution context and restore it with a defer, so
	// that EVERY exit path — a return, a panic unwinding through this frame,
	// a hard exit, a host-level failure — leaves the caller's params, scope,
	// environment and call stack exactly as they were. See [bashPPFrame].
	frame := r.bashPPEnterFrame(fn, args)
	defer frame.leave()
	if r.goSourceTesting != nil {
		defer r.bashPPTestingUnwind(ctx, frame.deferMark)
	}
	shortFailureMark := r.bashPPShortFailureSeq

	// Parameters and named results are typed bindings; a shell assignment in
	// the body writes through to them, which is what lets a named result be
	// set with `n=5` and read back by a bare return.
	//
	// The variadic parameter binds the REMAINING arguments as an indexed
	// variable, so the body reads them with the array spellings the shell
	// already has — `${rest[@]}`, `${#rest[@]}` — and can forward them with
	// `rest...`. Zero remaining arguments still bind the name, to an empty
	// list: a variadic parameter is never unset, exactly as a nil slice in Go
	// is still a slice.
	for i, param := range params {
		if param.variadic {
			// An original Go program's variadic parameter IS a slice: it is
			// printed as one, measured with len, indexed, resliced and passed
			// on. The indexed shell binding below carries none of that, so a
			// Go-source frame binds a real slice cell instead.
			if r.bashPPGoSource && param.name != "" {
				if !r.goSourceBindVariadic(param, args[i:], callCells[min(i, len(callCells)):], callSpread) {
					return nil
				}
				break
			}
			if param.name != "" {
				rest := append([]string(nil), args[i:]...)
				_ = r.bashPPScope.declare(param.name,
					expand.Variable{Set: true, Kind: expand.Indexed, List: rest}, false)
				// The indexed binding above is what lets the body keep using
				// `${rest[@]}`; this collection meta rides alongside it so a
				// two-variable `for i, v := range rest` sees each element's value
				// and declared type instead of falling through to the scalar
				// range path, which has no notion of a list at all and would
				// treat len(rest) as an integer to count up to.
				r.bashPPScope.lookup(param.name).valueMeta = &bashPPCollectionMeta{
					kind:     "slice",
					typ:      &syntax.BashPPCollectionType{Kind: "slice", Element: param.typ},
					sequence: make([]*bashPPCollectionMeta, len(rest)),
				}
			}
			break
		}
		if param.name == "" {
			continue
		}
		_ = r.bashPPScope.declare(param.name,
			expand.Variable{Set: true, Kind: expand.String, Str: args[i]}, false)
		if i < len(callCells) && callCells[i] != nil {
			copy, err := r.goSourceExpectedCell(callCells[i], param.typ)
			if err != nil {
				r.exit.fatal(err)
				return nil
			}
			copy = bashPPCopyAssignmentCell(copy)
			copy.channel = nil
			copy.channelOwner = nil
			copy.constant = false
			copy.vr.ReadOnly = false
			copy.vr.Exported = false
			if err := r.bashPPBindInterfaceParam(copy, param.typ); err != nil {
				r.errf("%v\n", err)
				r.exit = exitStatus{code: 2}
				return nil
			}
			if param.typ != nil {
				copy.declType = param.typ
			}
			r.bashPPScope.entries[param.name] = copy
		}
		if i < len(callChannels) && callChannels[i] != nil {
			cell := r.bashPPScope.lookup(param.name)
			cell.channel, cell.channelOwner = callChannels[i], r.bashPPConcurrent
		}
		if i < len(callInterfaces) && callInterfaces[i] != nil && !(r.bashPPGoSource && i < len(callCells) && callCells[i] != nil) {
			cell := r.bashPPScope.lookup(param.name)
			cell.interfaceValue = callInterfaces[i]
		}
		if base := strings.TrimPrefix(param.declared, "*"); base != "" {
			if _, ok := r.bashPPTypes[base]; ok {
				cell := r.bashPPScope.lookup(param.name)
				cell.typeName = base
				cell.pointer = strings.HasPrefix(param.declared, "*")
				cell.nilPointer = cell.pointer && args[i] == ""
				if r.bashPPGoSource && cell.pointer {
					cell.nilPointer = cell.pointerValue == nil
				}
			}
		}
	}
	resultNames := bashppResultNames(fn.results())
	if r.bashPPGoSource {
		if !r.goSourceDeclareResults(ctx, fn.results()) {
			return nil
		}
	} else {
		for _, name := range resultNames {
			if name != "" {
				_ = r.bashPPScope.declare(name, expand.Variable{Set: true, Kind: expand.String, Str: ""}, false)
			}
		}
	}

	if body := fn.body(); body != nil {
		r.stmts(ctx, body.Stmts)
	}

	// Results are settled BEFORE the deferred calls run, as Go sets a
	// function's results before running its defers. That ordering is what
	// lets a deferred call change a NAMED result — including the recovering
	// defer, whose whole job is to replace the value an abandoned frame would
	// otherwise have failed to produce.
	shortDeclFailed := r.bashPPShortFailureSeq != shortFailureMark
	var results []string
	if !shortDeclFailed {
		results = r.bashPPSettleResults(fn, resultNames)
	}

	// Deferred calls run as the frame unwinds — on a normal return and on a
	// panic alike, which is the point of them — but not through a hard shell
	// `exit`, which is terminating everything.
	if !r.exit.exiting {
		r.bashPPRunDefers(ctx, frame.deferMark)
	} else if !r.bashPPTestingCancelUnwind(ctx, frame.deferMark) {
		r.bashPPDeferStack = r.bashPPDeferStack[:frame.deferMark]
	}
	if shortDeclFailed {
		r.exit = exitStatus{code: 2}
		return nil
	}

	// An explicit `exit` reached while a panic was unwinding terminates the
	// script with the status it named, and the panic is neither reported nor
	// propagated: the shell is leaving on purpose, not crashing.
	if r.bashPPPanicSettledByExit() {
		return nil
	}
	if r.bashPPPanicking() {
		// The frame was abandoned, not returned from: it has no results, and
		// the panic continues into the caller unless this was the last frame
		// that could have recovered it.
		if r.bashPPFuncActive == 1 {
			r.bashPPPanicTerminate()
		} else {
			r.bashPPUnwind()
		}
		return nil
	}
	// A named result may have been reassigned by a deferred call, so it is
	// read here rather than trusted from before the defers ran.
	results = r.bashPPFinalResults(results, resultNames)
	resultTypes := bashppResultTypeExprs(fn.results())
	r.bashPPResultCells = make([]*bashPPCell, len(results))
	for i := range results {
		var source *bashPPCell
		if i < len(resultNames) && resultNames[i] != "" {
			source = r.bashPPScope.lookup(resultNames[i])
		} else if i < len(r.bashPPReturn.cells) {
			source = r.bashPPReturn.cells[i]
		}
		if i < len(resultTypes) && !r.bashPPCheckChannelResult(fn, resultTypes[i], source) {
			r.bashPPResultCells = nil
			return nil
		}
		if source != nil {
			r.bashPPResultCells[i] = bashPPCopyAssignmentCell(source)
		} else {
			r.bashPPResultCells[i] = &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: results[i]}}
		}
		if i < len(resultTypes) {
			var err error
			r.bashPPResultCells[i], err = r.goSourceExpectedCell(r.bashPPResultCells[i], resultTypes[i])
			if err != nil {
				r.exit.fatal(err)
				r.bashPPResultCells = nil
				return nil
			}
			r.bashPPResultCells[i].declType = resultTypes[i]
			r.bashPPResultCells[i].typeName = bashPPNamedTypeBase(resultTypes[i])
		}
	}
	// A Go-form return is consumed at the func boundary, exactly as a shell
	// function's `return` is in [Runner.call]; it must not unwind the caller.
	r.exit.returning = false
	return results
}

// bashPPFrame is one Go-form invocation's saved caller state.
//
// It exists so that entering and leaving a frame are ONE decision each rather
// than a dozen assignments repeated per exit path. Every field here is
// something a body can change and a caller must not observe changed; leaving
// restores all of them, and because it is called through a defer it restores
// them even on the paths that do not reach the end of the invoker.
type bashPPFrame struct {
	agentic    bool
	r          *Runner
	params     []string
	inFunc     bool
	writeEnv   expand.WriteEnviron
	scope      *bashPPScope
	callDepth  int
	deferMark  int
	ret        bashPPReturnState
	deferDepth int
	// typeArgs is the caller's type parameter bindings, restored on leave so
	// a generic frame's `T` cannot outlive the call that bound it.
	typeArgs map[string]syntax.BashPPTypeExpr
}

// bashPPEnterFrame pushes the frame fn's body runs in: its own shell function
// scope (so `local` and non-`local` assignments behave as in a shell function),
// its own typed scope chained to the captured definition scope (so closures
// resolve where they were written), its own positional parameters, and its own
// mark on the deferred-call stack.
func (r *Runner) bashPPEnterFrame(fn *bashPPFunc, args []string) *bashPPFrame {
	frame := &bashPPFrame{
		agentic:    r.bashPPAgentic,
		r:          r,
		params:     r.Params,
		inFunc:     r.inFunc,
		writeEnv:   r.writeEnv,
		scope:      r.bashPPScope,
		callDepth:  len(r.callStack),
		deferMark:  len(r.bashPPDeferStack),
		ret:        r.bashPPReturn,
		deferDepth: r.bashPPDeferDepth,
		typeArgs:   r.bashPPTypeParamArgs,
	}
	// The callee's own type arguments REPLACE the caller's rather than
	// extending them. An ordinary function called from inside a generic body
	// has none, and must not inherit a `T` it never declared.
	r.bashPPTypeParamArgs = fn.typeArgs
	r.bashPPAgentic = fn.decl != nil && fn.decl.Agentic != nil
	r.Params = args
	r.inFunc = true
	r.writeEnv = &overlayEnviron{parent: r.writeEnv, funcScope: true}
	r.bashPPScope = newBashPPScope(fn.scope)
	if fn.decl != nil && fn.decl.Receiver != nil && fn.decl.Receiver.Name != nil && fn.decl.Receiver.Name.Value != "_" && fn.receiver != nil {
		recv := fn.decl.Receiver
		if recv.Pointer {
			r.bashPPScope.entries[recv.Name.Value] = fn.receiver
		} else {
			copyCell := *fn.receiver
			r.bashPPScope.entries[recv.Name.Value] = &copyCell
		}
	}
	r.callStack = append(r.callStack, callFrame{funcName: fn.name()})
	r.bashPPReturn = bashPPReturnState{}
	r.bashPPFuncActive++
	return frame
}

// leave restores the caller's execution context.
//
// It is deliberately tolerant about depth: it truncates the call and defer
// stacks back to where the frame began rather than popping a fixed count, so a
// frame abandoned mid-unwind cannot leave a deeper stack behind or pop one
// entry too many.
func (f *bashPPFrame) leave() {
	r := f.r
	r.bashPPAgentic = f.agentic
	r.writeEnv = f.writeEnv
	r.bashPPScope = f.scope
	if len(r.callStack) > f.callDepth {
		r.callStack = r.callStack[:f.callDepth]
	}
	if len(r.bashPPDeferStack) > f.deferMark {
		r.bashPPDeferStack = r.bashPPDeferStack[:f.deferMark]
	}
	r.Params = f.params
	r.inFunc = f.inFunc
	r.bashPPReturn = f.ret
	r.bashPPDeferDepth = f.deferDepth
	r.bashPPTypeParamArgs = f.typeArgs
	r.bashPPFuncActive--
}

// bashPPShortDeclCall invokes a typed function for `x := f()` / `a, b := g()`
// and binds its results to the left-hand names positionally, reporting an arity
// mismatch against the function's actual results rather than the call's text.
func (r *Runner) bashPPShortDeclCall(ctx context.Context, d *syntax.BashPPShortDecl, fn *bashPPFunc) {
	args, ok := r.bashPPCallValues(d.Call, fn)
	if !ok {
		return
	}
	shortFailureMark := r.bashPPShortFailureSeq
	results := r.bashPPInvoke(ctx, fn, args)
	// A call abandoned by panic or hard termination produced no values. Do not
	// turn that control transfer into a secondary assignment-mismatch error;
	// the caller's frame must get the original unwind unchanged.
	if r.bashPPPanicking() || r.exit.exiting || r.exit.fatalExit || r.exit.err != nil || r.bashPPShortFailureSeq != shortFailureMark {
		return
	}
	if len(d.Lhs) != len(results) {
		r.errf("assignment mismatch: %d variable(s) but %d value(s)\n",
			len(d.Lhs), len(results))
		r.exit = exitStatus{code: 2}
		return
	}
	resultTypes := bashppResultTypes(fn.results())
	resultTypeExprs := bashppResultTypeExprs(fn.results())
	for i, lhs := range d.Lhs {
		// Go's blank identifier discards the result: `_, err := f()` declares
		// no binding for the first result, so there is nothing to look up and
		// nothing to attach that result's provenance to.
		if lhs.Value == "_" {
			continue
		}
		if !syntax.BashPPValidIdent(lhs.Value) {
			r.errf("invalid variable name: %q\n", lhs.Value)
			r.exit = exitStatus{code: 2}
			return
		}
		r.bashPPDeclareName(lhs.Value, expand.Variable{Set: true, Kind: expand.String, Str: results[i]})
		target := r.bashPPScope.lookup(lhs.Value)
		if target == nil {
			continue
		}
		if i < len(r.bashPPResultCells) && r.bashPPResultCells[i] != nil {
			source := r.bashPPResultCells[i]
			target.channel, target.channelOwner = source.channel, source.channelOwner
			// A structured result is the value itself, not its text: a
			// pointer, an interface and an object payload have to survive the
			// hand-off or the binding names an empty string.
			target.interfaceValue = source.interfaceValue
			target.pointer, target.nilPointer, target.pointerValue = source.pointer, source.nilPointer, source.pointerValue
			if source.vr.Kind == expand.Object || source.pointer || source.interfaceValue != nil {
				target.vr, target.object, target.valueMeta = source.vr, source.object, source.valueMeta
			}
		}
		if i < len(resultTypeExprs) {
			target.declType = resultTypeExprs[i]
		}
		if i < len(resultTypes) {
			declared := resultTypes[i]
			base := strings.TrimPrefix(declared, "*")
			if _, ok := r.bashPPTypes[base]; ok {
				target.typeName = base
				target.pointer = strings.HasPrefix(declared, "*")
				// A pointer binding's text is always empty, so the result cell
				// is what distinguishes a live pointer from a nil one.
				target.nilPointer = target.pointer && target.pointerValue == nil && results[i] == ""
			}
		}
	}
}

// bashPPSettleResults reconciles what a function returns with what it declared,
// setting the exit status as a side effect and reporting an arity mismatch.
//
// It runs BEFORE the frame's deferred calls, and for a NAMED result that
// matters twice over: the returned value is written into the result's binding,
// so a deferred call sees what is being returned and can replace it, and
// [Runner.bashPPFinalResults] then reads the binding back after the defers
// have had their say. That is Go's rule — "the deferred functions run after
// the result parameters are set" — expressed in the only two places that can
// observe it.
func (r *Runner) bashPPSettleResults(fn *bashPPFunc, resultNames []string) []string {
	count := bashppResultCount(fn.results())
	ret := r.bashPPReturn

	// A result-less function keeps shell semantics for `return`: a bare return
	// or falling off the end yields the body's last status, and `return n`
	// yields status n, mirroring the builtin.
	if count == 0 {
		if ret.active && len(ret.values) == 1 {
			if code, ok := bashppExitCode(ret.values[0]); ok {
				r.exit.code = code
			}
		} else if ret.active && len(ret.values) > 1 {
			r.errf("%s: too many return values for a function with no results\n", fn.name())
			r.exit.code = 2
		}
		return nil
	}

	// A return that names values must name exactly as many as declared.
	if ret.active && len(ret.values) > 0 {
		if len(ret.values) != count {
			r.errf("%s: returned %d value(s) but declared %d result(s)\n",
				fn.name(), len(ret.values), count)
			r.exit.code = 2
			return nil
		}
		resultTypes := bashppResultTypeExprs(fn.results())
		for i, name := range resultNames {
			if name != "" && i < len(ret.values) {
				target := r.bashPPScope.lookup(name)
				if target != nil && i < len(ret.cells) && ret.cells[i] != nil {
					constantBinding := target.constant
					readonlyBinding, exportedBinding := target.vr.ReadOnly, target.vr.Exported
					*target = *bashPPCopyAssignmentCell(ret.cells[i])
					target.constant = constantBinding
					target.vr.ReadOnly, target.vr.Exported = readonlyBinding, exportedBinding
				} else {
					r.setVarString(name, ret.values[i])
					target = r.bashPPScope.lookup(name)
				}
				if target != nil && i < len(resultTypes) {
					target.declType = resultTypes[i]
					target.typeName = bashPPNamedTypeBase(resultTypes[i])
				}
			}
		}
		r.exit.clear()
		return ret.values
	}

	// A bare return, or the end of the body, yields the current values of the
	// named results (their zero value if never assigned). An unnamed result
	// with no explicit return yields its zero value, an empty string.
	out := make([]string, 0, count)
	for _, name := range resultNames {
		if name == "" {
			out = append(out, "")
			continue
		}
		out = append(out, r.envGet(name))
	}
	// If the declaration mixed styles or listed only unnamed results, pad to
	// the declared arity with zero values so callers always see `count` items.
	for len(out) < count {
		out = append(out, "")
	}
	r.exit.clear()
	return out
}

// bashPPFinalResults is what the caller actually receives, read after the
// frame's deferred calls have run.
//
// Only a NAMED result is re-read. An unnamed result has no binding a deferred
// call could reach, in Go or here, so its settled value is final; re-reading a
// name that does not exist would replace it with an empty string.
func (r *Runner) bashPPFinalResults(settled []string, resultNames []string) []string {
	out := settled
	for i, name := range resultNames {
		if name == "" || i >= len(out) {
			continue
		}
		out[i] = r.envGet(name)
	}
	return out
}

// bashPPReturnStmt evaluates a Go-form return, recording its values and
// unwinding the body through the shell's existing return machinery.
func (r *Runner) bashPPReturnStmt(ctx context.Context, ret *syntax.BashPPReturn) {
	if r.bashPPGoSource && len(ret.ResultExprs) > 0 {
		r.goSourceReturnValues(ret.ResultExprs)
		return
	}
	if r.bashPPBridgeHandles(ret.Call) {
		values, err := r.bashPPBridgeCall(ctx, ret.Call)
		if err != nil {
			if !r.bashPPPanicking() {
				r.exit.fatal(err)
			}
			return
		}
		result := bashPPReturnState{active: true}
		for _, value := range values {
			var cell *bashPPCell
			if scalar, err := value.scalar(); err == nil {
				cell = &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: bashPPScalarString(scalar.value)}, scalarKind: scalar.value.Kind(), typeName: value.Type, declType: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: value.Type}}}
			} else {
				copy := value
				cell = &bashPPCell{vr: expand.NewObject(&copy)}
			}
			result.values = append(result.values, cell.vr.String())
			result.cells = append(result.cells, cell)
		}
		r.bashPPReturn = result
		r.exit.returning = true
		return
	}
	if ret.Call != nil {
		// `return float64(f)` is a conversion, not a call; the Go front end
		// cannot tell the two apart from syntax alone and delivers both as a
		// call, so the conversion is recognized here rather than refused as an
		// undeclared callable.
		if conv, ok := r.bashPPConversionCall(ret.Call); ok {
			r.bashPPReturnScalarExpr(conv)
			return
		}
		if cell, handled, err := r.goSourceBuiltinResult(ret.Call); handled {
			if err != nil {
				r.bashPPShortFailureSeq++
				return
			}
			r.bashPPReturn = bashPPReturnState{active: true, values: []string{cell.vr.String()}, cells: []*bashPPCell{bashPPCopyAssignmentCell(cell)}}
			r.exit.returning = true
			return
		}
		fn, ok := r.bashPPLookupFunc(ret.Call)
		if !ok {
			r.bashPPShortFailureSeq++
			if r.exit.code == 0 {
				r.errf("BASHPP-ERETURN-CALL: return requires a declared callable\n")
				r.exit = exitStatus{code: 2}
			}
			return
		}
		args, ok := r.bashPPCallValues(ret.Call, fn)
		if !ok {
			r.bashPPShortFailureSeq++
			return
		}
		failureMark := r.bashPPShortFailureSeq
		values := r.bashPPInvoke(ctx, fn, args)
		if r.exit.code != 0 && !r.bashPPPanicking() {
			r.bashPPShortFailureSeq++
			return
		}
		if r.bashPPPanicking() || r.exit.exiting || r.exit.fatalExit || r.exit.err != nil || r.bashPPShortFailureSeq != failureMark {
			return
		}
		cells := append([]*bashPPCell(nil), r.bashPPResultCells...)
		r.bashPPReturn = bashPPReturnState{active: true, values: values, cells: cells}
		r.exit.returning = true
		return
	}
	if ret.FuncLit != nil {
		// `return func(…) { … }` — the factory idiom. The closure captures the
		// frame that is about to unwind, which is exactly what makes it useful:
		// the cells stay alive because the capture holds them, not because the
		// frame does.
		_, vr := r.bashPPMakeClosure(ret.FuncLit)
		r.bashPPReturn = bashPPReturnState{active: true, values: []string{vr.Str}}
		r.exit.returning = true
		return
	}
	if ret.Expr != nil {
		r.bashPPReturnScalarExpr(ret.Expr)
		return
	}
	vals := make([]string, len(ret.Results))
	cells := make([]*bashPPCell, len(ret.Results))
	for i, w := range ret.Results {
		vals[i] = r.bashPPExprValue(w)
		if source := r.bashPPCellForWord(w); source != nil {
			cells[i] = bashPPCopyAssignmentCell(source)
		}
	}
	r.bashPPReturn = bashPPReturnState{active: true, values: vals, cells: cells}
	r.exit.returning = true
}

// bashPPReturnScalarExpr settles a single scalar result, retaining the value's
// type so a defined type reaches the caller as itself.
func (r *Runner) bashPPReturnScalarExpr(expr syntax.BashPPExpr) {
	if cell, handled, err := r.goSourceNilValueCell(expr); handled {
		if err != nil {
			r.exit.fatal(err)
			return
		}
		r.bashPPReturn = bashPPReturnState{active: true, values: []string{cell.vr.String()}, cells: []*bashPPCell{cell}}
		r.exit.returning = true
		return
	}

	if cell, handled, err := r.goSourceChannelValueCell(expr); handled {
		if err != nil {
			r.bashPPGoSendError(expr, err)
			return
		}
		r.bashPPReturn = bashPPReturnState{active: true, values: []string{cell.vr.String()}, cells: []*bashPPCell{cell}}
		r.exit.returning = true
		return
	}
	// An imported value returned as itself — `return color.RGBAModel`,
	// `return image.Rect(…)` — crosses back as the authenticated native handle
	// the dependency owns, never flattened into text. Imported scalars keep
	// their existing scalar cells; see [goSourceNativeValueCell].
	if r.bashPPGoSource && r.bashPPNativeExpr(expr) {
		value, err := r.bashPPBridgeExpr(expr)
		if err != nil {
			// Native evaluation may already have recorded a Go panic or exit.
			// Keep that state so deferred recover and cancellation can unwind.
			if errors.Is(err, errBashPPScalarInterrupted) || r.bashPPPanicking() || r.exit.exiting || r.exit.fatalExit {
				return
			}
			r.exit.fatal(&goSourceError{prefix: r.bashErrPrefix(expr.Pos()), err: err})
			r.bashPPShortFailureSeq++
			return
		}
		cell := goSourceNativeValueCell(value)
		r.bashPPReturn = bashPPReturnState{active: true, values: []string{cell.vr.String()}, cells: []*bashPPCell{cell}}
		r.exit.returning = true
		return
	}
	// A returned value need not be scalar: `return &V{…}`, `return *p` and
	// `return v.Inner` all name storage the caller receives as a value, and
	// the cell is the only thing that can carry it across the boundary.
	structured, structuredErr := r.bashPPStructuredArgCell(nil, expr)
	if structuredErr != nil {
		r.errf("%v\n", structuredErr)
		r.exit = exitStatus{code: 2}
		r.bashPPShortFailureSeq++
		return
	}
	if structured != nil {
		r.bashPPReturn = bashPPReturnState{active: true, values: []string{structured.vr.String()}, cells: []*bashPPCell{structured}}
		r.exit.returning = true
		return
	}
	value, err := r.bashPPEvalScalarExpr(expr)
	if err != nil {
		if errors.Is(err, errBashPPScalarInterrupted) {
			return
		}
		r.errf("%v\n", err)
		r.exit = exitStatus{code: 2}
		r.bashPPShortFailureSeq++
		return
	}
	text := bashPPScalarString(value.value)
	cell := &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: text}, scalarKind: value.value.Kind()}
	if value.typ != "" {
		cell.typeName = value.typ
		cell.declType = &syntax.BashPPNamedType{Name: &syntax.Lit{Value: value.typ}}
	}
	r.bashPPReturn = bashPPReturnState{active: true, values: []string{text}, cells: []*bashPPCell{cell}}
	r.exit.returning = true
}

// bashPPDeferStmt records a deferred call, evaluating its arguments now.
func (r *Runner) bashPPDeferStmt(ctx context.Context, d *syntax.BashPPDefer) {
	if d.Call == nil {
		return
	}
	if r.bashPPFuncActive == 0 {
		// The parser claims the call-shaped form because stock bash rejects it,
		// but defer has meaning only in a Go-form function body. Diagnose this
		// Class-R misuse instead of silently queuing a cleanup that can never run.
		r.errf("defer: not inside a Bash++ function\n")
		r.exit.code = 2
		return
	}
	// Both the function and its arguments are fixed HERE, as Go fixes them:
	// a literal is captured now, and a name resolves to the function it names
	// now, so a later rebinding cannot change which cleanup runs.
	entry := bashPPDeferred{call: d.Call, agentic: r.bashPPAgentic}
	if invoke, handled := r.goSourceCaptureDeferredClose(d.Call); handled {
		if invoke != nil {
			entry.builtin = invoke
			r.bashPPDeferStack = append(r.bashPPDeferStack, entry)
		}
		return
	}
	if invoke, handled := r.bashPPTestingCapture(d.Call); handled {
		if invoke != nil {
			entry.testing = invoke
			r.bashPPDeferStack = append(r.bashPPDeferStack, entry)
		}
		return
	}
	if invoke, handled, err := r.bashPPNativeCapture(ctx, d.Call); handled {
		if err != nil {
			if !r.bashPPPanicking() {
				r.exit.fatal(err)
			}
			return
		}
		entry.native = invoke
		r.bashPPDeferStack = append(r.bashPPDeferStack, entry)
		return
	}
	if fn, ok := r.bashPPLookupFunc(d.Call); ok {
		args, ok := r.bashPPCallValues(d.Call, fn)
		if !ok {
			return
		}
		entry.fn, entry.args = fn, args
		entry.cells = r.bashPPCallCells
		r.bashPPCallCells = nil
	} else {
		if r.exit.code != 0 {
			return
		}
		// A predeclared callee is resolved here for the same reason a declared
		// one is: what the defer names is fixed now, not at unwind time.
		entry.predeclared = bashPPPredeclaredCall(d.Call)
		entry.args = r.bashPPCallArgValues(d.Call)
	}
	r.bashPPDeferStack = append(r.bashPPDeferStack, entry)
}

// bashPPRunDefers runs the deferred calls pushed above mark, most recent first,
// then trims the stack back to mark. A return in flight is paused across the
// defers and resumes afterwards, matching Go, where a deferred call runs even
// as the function is returning.
//
// A PANIC IN FLIGHT IS ALSO PAUSED, in a different sense: it stays recorded,
// because that is what a deferred call recovers, but the halt it imposes is
// lifted for the duration of each call, because otherwise no cleanup would run
// at all. If a deferred call recovers, the loop keeps going with the panic
// gone; if one panics itself, the new panic replaces the old and the remaining
// cleanups still run, exactly as Go keeps unwinding.
func (r *Runner) bashPPRunDefers(ctx context.Context, mark int) {
	// Copy before truncating: merely slicing would retain the backing array, so
	// a deferred call which itself defers could overwrite an entry we have not
	// run yet when append reuses that capacity.
	pending := append([]bashPPDeferred(nil), r.bashPPDeferStack[mark:]...)
	r.bashPPDeferStack = r.bashPPDeferStack[:mark]
	savedReturning := r.exit.returning
	r.exit.returning = false
	savedDeferDepth := r.bashPPDeferDepth
	savedAgentic := r.bashPPAgentic
	// A call this frame deferred runs one frame deeper than this one, and that
	// depth is the whole of recover's "called directly by a deferred function"
	// rule; see [Runner.bashPPRecover].
	r.bashPPDeferDepth = len(r.callStack) + 1
	defer func() {
		r.bashPPAgentic = savedAgentic
		r.bashPPDeferDepth = savedDeferDepth
		r.bashPPPanic.running = false
	}()
	var failed exitStatus
	deferFailed := false
	var testingControl *goSourceTestingExit
	for i := len(pending) - 1; i >= 0; i-- {
		d := pending[i]
		r.bashPPAgentic = d.agentic
		r.exit = exitStatus{}
		// A cleanup runs even while a panic is unwinding — that is the whole
		// point of it — so the panic stops halting statements for the length
		// of this call, without ceasing to be recoverable by it.
		r.bashPPPanic.running = r.bashPPPanic.active
		builtinPanicDepth := len(r.bashPPPanic.chain)
		runDeferred := func() {
			switch {
			case d.builtin != nil:
				d.builtin()
			case d.native != nil:
				if err := d.native(ctx); err != nil && !r.bashPPPanicking() {
					r.exit.fatal(err)
				}
			case d.testing != nil:
				d.testing()
			case d.fn != nil:
				r.bashPPCallCells = d.cells
				r.bashPPInvoke(ctx, d.fn, d.args)
			case d.predeclared != "":
				// `defer panic(v)` and `defer recover()`. The latter is the shape
				// Go documents as not working, and it does not work here either,
				// for the reason it does not there: recover IS the deferred call,
				// so nothing deferred it in turn — see [Runner.bashPPRecover].
				if bashPPValueBuiltin(d.predeclared) {
					r.bashPPRunValueBuiltin(d.predeclared, d.call)
				} else {
					r.bashPPPredeclared(d.predeclared, d.call, d.args)
				}
			case len(d.call.Fun) > 1:
				// A deferred SELECTOR is dispatched exactly as a direct one is,
				// through the import evaluator, so `defer fmt.Println(x)` reaches
				// the package rather than a shell command named after the final
				// selector element. Its arguments were evaluated at defer time and
				// are handed over as values, so the call the evaluator makes is the
				// one the defer described.
				r.bashPPEvalSelector(ctx, d.call, d.args)
			case len(d.call.Fun) > 0:
				// A deferred call to something that is not a typed function runs as an
				// ordinary command, which is what makes `defer log(...)` reach a shell
				// helper of that name.
				r.call(ctx, d.call.Pos(), append([]string{d.call.Fun[0].Value}, d.args...))
			}
		}
		if r.goSourceTesting != nil {
			if control := bashPPTestingCatch(runDeferred); control != nil {
				testingControl = control
			}
		} else {
			runDeferred()
		}
		// An explicit `exit` inside a cleanup terminates the script there and
		// then: the remaining cleanups do not run, and any panic in flight is
		// discarded rather than reported, the precedence `os.Exit` has over a
		// panic in Go.
		r.bashPPPanic.running = false
		if r.exit.exiting || r.exit.fatalExit {
			r.bashPPPanicSettledByExit()
			return
		}
		// Cleanup failures are observable. Keep the first failure in execution
		// order while still running every remaining defer, then restore the
		// enclosing function's return status when all cleanups succeeded.
		// A deferred GoSource builtin can start a recoverable panic. Its
		// unwind status is not a failed shell cleanup to restore after a later
		// defer recovers; the panic state carries that control transfer.
		builtinPanic := d.builtin != nil && len(r.bashPPPanic.chain) > builtinPanicDepth
		if !deferFailed && !builtinPanic && (!r.exit.ok() || r.exit.err != nil) {
			failed, deferFailed = r.exit, true
		}
	}
	if testingControl != nil {
		panic(*testingControl)
	}
	if r.bashPPPanicking() {
		// The frame is still being abandoned; its status is the panic's, not
		// the last cleanup's.
		return
	}
	if deferFailed {
		r.exit = failed
	} else {
		r.exit.returning = savedReturning
	}
}

// bashPPParam is one parameter slot: the name it binds, the type it declares,
// and whether it is the variadic one. Flattening the field groups into slots
// once is what keeps the arity check, the binding loop and the diagnostics
// counting the same things.
type bashPPParam struct {
	name         string
	declared     string
	typ          syntax.BashPPTypeExpr
	variadic     bool
	defaultValue *syntax.Word
}

// bashppParams flattens a parameter list into one slot per declared name, plus
// slots for unnamed parameters, which accept arguments without binding them.
func bashppParams(fields []*syntax.BashPPField) []bashPPParam {
	var params []bashPPParam
	for _, f := range fields {
		declared := ""
		if f.FieldType != nil {
			declared = f.FieldType.Value
		}
		if f.Variadic() {
			name := ""
			if len(f.Names) > 0 {
				name = f.Names[0].Value
			}
			params = append(params, bashPPParam{name: name, declared: declared, typ: f.FieldTypeExpr, variadic: true})
			continue
		}
		if len(f.Names) == 0 {
			params = append(params, bashPPParam{declared: declared, typ: f.FieldTypeExpr})
			continue
		}
		for _, n := range f.Names {
			params = append(params, bashPPParam{name: n.Value, declared: declared, typ: f.FieldTypeExpr, defaultValue: f.Default})
		}
	}
	return params
}

func bashppHasDefaults(fields []*syntax.BashPPField) bool {
	for _, field := range fields {
		if field.Default != nil {
			return true
		}
	}
	return false
}

func bashppRequiredAfterDefault(fields []*syntax.BashPPField) string {
	sawDefault := false
	for _, field := range fields {
		if field.Default != nil {
			sawDefault = true
			continue
		}
		if sawDefault && !field.Variadic() && len(field.Names) > 0 {
			return field.Names[0].Value
		}
	}
	return ""
}

// bashppVariadic reports whether a parameter list ends in a `...T` group.
func bashppVariadic(fields []*syntax.BashPPField) bool {
	return len(fields) > 0 && fields[len(fields)-1].Variadic()
}

// bashPPCheckArgs applies the call's arity and type rules, reporting the first
// failure and returning false.
//
// ARITY. A non-variadic function takes exactly its parameters. A variadic one
// takes at least its fixed parameters and any number beyond them, including
// none — which is why the two are worded differently: "expected 2" and
// "expected at least 2" tell a reader which rule they broke.
//
// SPREAD. `f(xs...)` hands the variadic parameter one whole slice rather than
// one element per argument, so the trailing argument is checked against []T by
// the binding that receives it — see [Runner.goSourceBindSpreadVariadic] — and
// not against T here, which would reject every spread.
func (r *Runner) bashPPCheckArgs(fn *bashPPFunc, params []bashPPParam, args []string, spread bool) bool {
	fixed := len(params)
	variadic := fixed > 0 && params[fixed-1].variadic
	if variadic {
		fixed--
	}
	switch {
	case variadic && len(args) < fixed:
		r.errf("%s: expected at least %d argument(s), got %d\n", fn.name(), fixed, len(args))
		r.exit = exitStatus{code: 2}
		return false
	case !variadic && len(args) != fixed:
		r.errf("%s: expected %d argument(s), got %d\n", fn.name(), fixed, len(args))
		r.exit = exitStatus{code: 2}
		return false
	}
	for i, arg := range args {
		param := params[min(i, len(params)-1)]
		if spread && param.variadic {
			continue
		}
		if signature, ok := param.typ.(*syntax.BashPPFuncType); ok {
			actual, found := r.bashPPClosure(arg)
			if !found || bashPPFieldsSignature(actual.params()) != bashPPFieldsSignature(signature.Params) || bashPPFieldsSignature(actual.results()) != bashPPFieldsSignature(signature.Results) {
				r.errf("BASHPP-EARG-FUNCTYPE: %s requires %s for parameter %s\n", fn.name(), bashPPTypeText(signature), param.name)
				r.exit = exitStatus{code: 2}
				return false
			}
			continue
		}
		if r.bashPPValueFits(param.declared, arg) {
			continue
		}
		where := param.name
		if where == "" {
			where = strconv.Itoa(i + 1)
		}
		r.errf("%s: cannot use %q as %s value for parameter %s\n",
			fn.name(), arg, param.declared, where)
		r.exit = exitStatus{code: 2}
		return false
	}
	return true
}

// bashPPValueFits reports whether value is admissible for a parameter declared
// with the given type.
//
// It is deliberately NARROW. Values in this dialect are strings, so a type is
// checked only where the string form has an unambiguous membership test: the
// numeric and boolean types, and `func`, whose values are the runner's own
// closure handles and therefore exactly recognizable. Every other spelling —
// `string`, a dotted selector, a name the script declared with `type` — is
// accepted, because guessing at a membership rule for it would reject values
// the phase has no way to construct an opinion about. An untyped parameter
// (`func f(v)`) declares nothing and so admits everything.
func (r *Runner) bashPPValueFits(declared, value string) bool {
	seen := make(map[string]bool)
	for {
		typ, ok := r.bashPPTypes[declared]
		if !ok {
			break
		}
		if seen[declared] {
			return false
		}
		seen[declared] = true
		if typ.underlying == "enum" {
			for _, member := range typ.members {
				if value == member {
					return true
				}
			}
			return false
		}
		declared = typ.underlying
	}
	switch declared {
	case "int":
		_, err := strconv.ParseInt(value, 10, strconv.IntSize)
		return err == nil
	case "int8":
		_, err := strconv.ParseInt(value, 10, 8)
		return err == nil
	case "int16":
		_, err := strconv.ParseInt(value, 10, 16)
		return err == nil
	case "int32", "rune":
		_, err := strconv.ParseInt(value, 10, 32)
		return err == nil
	case "int64":
		_, err := strconv.ParseInt(value, 10, 64)
		return err == nil
	case "uint", "uintptr":
		_, err := strconv.ParseUint(value, 10, strconv.IntSize)
		return err == nil
	case "uint8", "byte":
		_, err := strconv.ParseUint(value, 10, 8)
		return err == nil
	case "uint16":
		_, err := strconv.ParseUint(value, 10, 16)
		return err == nil
	case "uint32":
		_, err := strconv.ParseUint(value, 10, 32)
		return err == nil
	case "uint64":
		_, err := strconv.ParseUint(value, 10, 64)
		return err == nil
	case "float32":
		_, err := strconv.ParseFloat(value, 32)
		return err == nil
	case "float64":
		_, err := strconv.ParseFloat(value, 64)
		return err == nil
	case "complex64":
		_, err := strconv.ParseComplex(value, 64)
		return err == nil
	case "complex128":
		_, err := strconv.ParseComplex(value, 128)
		return err == nil
	case "bool":
		return value == "true" || value == "false"
	case "func":
		_, ok := r.bashPPClosure(value)
		return ok
	}
	return true
}

// bashppResultNames lists one entry per result slot: the name for a named
// result, and the empty string for an unnamed one.
func bashppResultNames(fields []*syntax.BashPPField) []string {
	var names []string
	for _, f := range fields {
		if len(f.Names) == 0 {
			names = append(names, "")
			continue
		}
		for _, n := range f.Names {
			names = append(names, n.Value)
		}
	}
	return names
}

func bashppResultTypes(fields []*syntax.BashPPField) []string {
	var types []string
	for _, f := range fields {
		declared := ""
		if f.FieldType != nil {
			declared = f.FieldType.Value
		}
		count := len(f.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			types = append(types, declared)
		}
	}
	return types
}

func bashppResultTypeExprs(fields []*syntax.BashPPField) []syntax.BashPPTypeExpr {
	var types []syntax.BashPPTypeExpr
	for _, field := range fields {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			types = append(types, field.FieldTypeExpr)
		}
	}
	return types
}

func bashPPSubstituteFields(fields []*syntax.BashPPField, typeArgs map[string]syntax.BashPPTypeExpr) []*syntax.BashPPField {
	if len(typeArgs) == 0 {
		return fields
	}
	out := make([]*syntax.BashPPField, len(fields))
	for i, field := range fields {
		cp := *field
		if field.FieldTypeExpr != nil {
			cp.FieldTypeExpr = bashPPSubstituteType(field.FieldTypeExpr, typeArgs)
			if field.FieldType != nil {
				cp.FieldType = &syntax.Lit{ValuePos: field.FieldType.Pos(), ValueEnd: field.FieldType.End(), Value: bashPPTypeText(cp.FieldTypeExpr)}
			}
		}
		out[i] = &cp
	}
	return out
}

func bashPPSubstituteType(typ syntax.BashPPTypeExpr, typeArgs map[string]syntax.BashPPTypeExpr) syntax.BashPPTypeExpr {
	switch x := typ.(type) {
	case *syntax.BashPPTypeParamType:
		if arg := typeArgs[x.Name.Value]; arg != nil {
			return arg
		}
	case *syntax.BashPPNamedType:
		// A type parameter used in a BODY statement is parsed as an ordinary
		// named type: the declaration recognizers never see the enclosing
		// parameter list, so only signature positions get the dedicated
		// BashPPTypeParamType marker. Resolving the name here is what makes
		// `var zero T` inside the body mean the same as `T` in the signature.
		// A Go type parameter shadows any same-named type for the whole
		// function, so a binding wins over the script's namespace.
		if len(x.TypeArgs) == 0 {
			if arg := typeArgs[x.Name.Value]; arg != nil {
				return arg
			}
		}
		cp := *x
		cp.TypeArgs = append([]*syntax.BashPPTypeArg(nil), x.TypeArgs...)
		for i, arg := range cp.TypeArgs {
			ac := *arg
			ac.ArgType = bashPPSubstituteType(ac.ArgType, typeArgs)
			cp.TypeArgs[i] = &ac
		}
		return &cp
	case *syntax.BashPPFuncType:
		cp := *x
		cp.Params = bashPPSubstituteFields(x.Params, typeArgs)
		cp.Results = bashPPSubstituteFields(x.Results, typeArgs)
		return &cp
	case *syntax.BashPPCollectionType:
		cp := *x
		cp.Key = bashPPSubstituteType(x.Key, typeArgs)
		cp.Element = bashPPSubstituteType(x.Element, typeArgs)
		return &cp
	case *syntax.BashPPPointerType:
		cp := *x
		cp.Element = bashPPSubstituteType(x.Element, typeArgs)
		return &cp
	case *syntax.BashPPStructType:
		cp := *x
		cp.Fields = bashPPSubstituteFields(x.Fields, typeArgs)
		return &cp
	case *syntax.BashPPInterfaceType:
		cp := *x
		cp.Elems = append([]*syntax.BashPPInterfaceElem(nil), x.Elems...)
		for i, elem := range cp.Elems {
			ec := *elem
			ec.Embedded = bashPPSubstituteType(ec.Embedded, typeArgs)
			if ec.Method != nil {
				mc := *ec.Method
				mc.Params = bashPPSubstituteFields(mc.Params, typeArgs)
				mc.Results = bashPPSubstituteFields(mc.Results, typeArgs)
				ec.Method = &mc
			}
			cp.Elems[i] = &ec
		}
		cp.Methods = append([]*syntax.BashPPMethodSpec(nil), x.Methods...)
		for i, method := range cp.Methods {
			mc := *method
			mc.Params = bashPPSubstituteFields(mc.Params, typeArgs)
			mc.Results = bashPPSubstituteFields(mc.Results, typeArgs)
			cp.Methods[i] = &mc
		}
		return &cp
	case *syntax.BashPPUnionType:
		cp := *x
		cp.Terms = append([]syntax.BashPPTypeExpr(nil), x.Terms...)
		for i, term := range cp.Terms {
			cp.Terms[i] = bashPPSubstituteType(term, typeArgs)
		}
		return &cp
	case *syntax.BashPPApproxType:
		cp := *x
		cp.Term = bashPPSubstituteType(cp.Term, typeArgs)
		return &cp
	}
	return typ
}

// bashppResultCount is the number of values a function returns.
func bashppResultCount(fields []*syntax.BashPPField) int {
	count := 0
	for _, f := range fields {
		if len(f.Names) == 0 {
			count++
			continue
		}
		count += len(f.Names)
	}
	return count
}

// bashppExitCode parses a bash-style return status, which is taken modulo 256
// exactly as the shell's own `return` builtin does.
func bashppExitCode(s string) (uint8, bool) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return uint8(n), true
}
