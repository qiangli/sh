// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
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
	decl  *syntax.BashPPFuncDecl
	lit   *syntax.BashPPFuncLit
	scope *bashPPScope
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
		return f.decl.Params
	}
	return f.lit.Params
}

func (f *bashPPFunc) results() []*syntax.BashPPField {
	if f.decl != nil {
		return f.decl.Results
	}
	return f.lit.Results
}

// cloned copies f for a subshell, deep-copying its captured scope through the
// shared cloner so the aliasing between closures, and between a closure and the
// live scope, survives the copy.
func (f *bashPPFunc) cloned(c *bashPPCloner) *bashPPFunc {
	copied := *f
	copied.scope = c.clone(f.scope)
	copied.receiver = c.cloneCell(f.receiver)
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
		fn.scope = r.bashPPScope.snapshot()
	}
	return fn, r.bashPPStoreFunc(fn)
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
	call *syntax.BashPPCall
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
}

// bashPPReturnState carries a Go-form return out through the body's statement
// loop to the invoker. active records that a return fired at all, which
// distinguishes a bare `return` (values nil) from falling off the end of the
// body.
type bashPPReturnState struct {
	active bool
	values []string
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
	if !syntax.ValidName(name) {
		r.errf("invalid function name: %q\n", name)
		r.exit = exitStatus{code: 2}
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
		captured = r.bashPPScope.snapshot()
	}
	r.bashPPFuncs[name] = &bashPPFunc{decl: d, scope: captured}
}

func (r *Runner) bashPPMethodDecl(d *syntax.BashPPFuncDecl) {
	recv := d.Receiver
	for _, field := range append(append([]*syntax.BashPPField(nil), d.Params...), d.Results...) {
		for _, name := range field.Names {
			if name.Value == recv.Name.Value && name.Value != "_" {
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
		captured = r.bashPPScope.snapshot()
	}
	methods[d.Name.Value] = &bashPPFunc{decl: d, scope: captured}
}

// bashPPLookupFunc resolves a call's callee to a callable function: a literal
// in callee position, a function declared with `func`, or a name bound to a
// closure. A dotted selector (`pkg.Fn`) is never a local function.
//
// A literal is EVALUATED here, which is the correct moment: `func(n int) { …
// }(1)` captures the scope at the point of the call, exactly as the same
// literal bound to a name captures it at the point of the binding.
func (r *Runner) bashPPLookupFunc(c *syntax.BashPPCall) (*bashPPFunc, bool) {
	if c.FuncLit != nil {
		fn, _ := r.bashPPMakeClosure(c.FuncLit)
		return fn, true
	}
	if len(c.Fun) == 2 {
		owner, method := c.Fun[0].Value, c.Fun[1].Value
		// A local value is always considered before an import binding. This is
		// deterministic even when the import registry contains the same name.
		if _, localType := r.bashPPTypes[owner]; !localType {
			if cell := r.bashPPScope.lookup(owner); cell != nil {
				if cell.typeName == "" {
					r.errf("%s.%s: %s is a local value with no methods\n", owner, method, owner)
					r.exit.code = 2
					return nil, false
				}
				return r.bashPPBindMethod(cell, method, true)
			}
		}
		// T.M(v, ...) selects from T's method set; (*T).M(p, ...) records the
		// pointer method-expression spelling on the call node.
		if _, localType := r.bashPPTypes[owner]; localType {
			methods := r.bashPPMethods[owner]
			fn := methods[method]
			if fn == nil || (fn.decl.Receiver.Pointer && !c.PointerMethodExpr) {
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
			if cell == nil || cell.typeName != owner || cell.pointer != c.PointerMethodExpr {
				r.errf("cannot use first argument as %s receiver in %s.%s\n", owner, owner, method)
				r.exit.code = 2
				return nil, false
			}
			bound := *fn
			if fn.decl.Receiver.Pointer {
				bound.receiver = cell
			} else {
				if cell.pointer && cell.nilPointer {
					r.errf("value method %s called using nil *%s pointer\n", method, owner)
					r.exit.code = 2
					return nil, false
				}
				copyCell := *cell
				copyCell.pointer, copyCell.nilPointer = false, false
				bound.receiver = &copyCell
			}
			bound.skipArgs = 1
			return &bound, true
		}
		return nil, false
	}
	if len(c.Fun) != 1 {
		return nil, false
	}
	name := c.Fun[0].Value
	if fn, ok := r.bashPPFuncs[name]; ok {
		return fn, true
	}
	// A closure held in a variable is callable by that variable's name, which
	// is what makes `greet := func(…) { … }; greet(x)` and a returned factory
	// closure work without a second call syntax.
	if vr := r.lookupVar(name); vr.Kind == expand.String {
		return r.bashPPClosure(vr.Str)
	}
	return nil, false
}

func (r *Runner) bashPPCellForWord(w *syntax.Word) *bashPPCell {
	if w == nil || len(w.Parts) != 1 || r.bashPPScope == nil {
		return nil
	}
	lit, ok := w.Parts[0].(*syntax.Lit)
	if !ok || !syntax.ValidName(lit.Value) {
		return nil
	}
	return r.bashPPScope.lookup(lit.Value)
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
	if ptrRecv {
		bound.receiver = cell
	} else {
		copyCell := *cell
		copyCell.pointer, copyCell.nilPointer = false, false
		bound.receiver = &copyCell
	}
	return &bound, true
}

// bashPPCallArgValues evaluates each call argument to a single string. Values
// follow the dialect's existing convention that a bare word is its own literal
// and an expansion is expanded, exactly as a `:=` right-hand side does, so
// `f($x)` passes the value of x while `f(x)` passes the string "x".
//
// A trailing `...` spreads its argument instead of passing it: `f(xs...)`
// hands the elements of xs to a variadic parameter, one argument each.
func (r *Runner) bashPPCallArgValues(c *syntax.BashPPCall) []string {
	args := make([]string, 0, len(c.Args))
	for i, w := range c.Args {
		if c.Ellipsis.IsValid() && i == len(c.Args)-1 {
			args = append(args, r.bashPPSpreadValues(w)...)
			break
		}
		args = append(args, r.bashPPExprValue(w))
	}
	return args
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
		if lit, ok := w.Parts[0].(*syntax.Lit); ok && syntax.ValidName(lit.Value) {
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
func (r *Runner) bashPPCallValues(c *syntax.BashPPCall, fn *bashPPFunc) ([]string, bool) {
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
	args := r.bashPPCallArgValues(c)
	names := c.ArgNames
	positional := len(args) - len(names)
	if fn.skipArgs > 0 {
		args = args[fn.skipArgs:]
		positional -= fn.skipArgs
	}
	if len(names) > 0 || bashppHasDefaults(fn.params()) {
		return r.bashPPBindCall(fn, args, names, positional)
	}
	return args, true
}

// bashPPBindCall applies the Bash# positional-then-named/default binding
// contract. It is deliberately entered only when a call uses a name or the
// signature has a default, so the established P3 arity diagnostics remain
// byte-for-byte unchanged for ordinary Go-form calls.
func (r *Runner) bashPPBindCall(fn *bashPPFunc, supplied []string, names []*syntax.Lit, positional int) ([]string, bool) {
	params := bashppParams(fn.params())
	fail := func(format string, args ...any) ([]string, bool) {
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
	bound := make([]bool, len(params))
	for i := 0; i < positional; i++ {
		values[i], bound[i] = supplied[i], true
	}
	for i, name := range names {
		index := byName[name.Value]
		if bound[index] {
			return fail("BASHPP-EARG-DUPLICATE-BINDING: parameter %q is supplied positionally and by name\n", name.Value)
		}
		values[index], bound[index] = supplied[positional+i], true
	}
	for i, param := range params {
		if bound[i] {
			continue
		}
		if param.defaultValue != nil {
			values[i], bound[i] = r.bashPPExprValue(param.defaultValue), true
			continue
		}
		return fail("BASHPP-EARG-MISSING: %s requires argument %q\n", fn.name(), param.name)
	}
	return values, true
}

// bashPPExprValue evaluates the small expression vocabulary admitted by the
// P3-A call grammar. A bare identifier is an expression, not a literal word;
// resolve it through the existing lexical/shell environment when it names a
// live binding. Other bare words remain string literals, preserving the
// convenient `f(hello)` spelling for an unquoted string argument.
func (r *Runner) bashPPExprValue(w *syntax.Word) string {
	if len(w.Parts) == 1 {
		if lit, ok := w.Parts[0].(*syntax.Lit); ok && syntax.ValidName(lit.Value) {
			if vr := r.lookupVar(lit.Value); vr.IsSet() {
				return vr.Str
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
	if !ok || !syntax.ValidName(lit.Value) || !r.lookupVar(lit.Value).IsSet() {
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
		if !ok || !syntax.ValidName(lit.Value) || !r.lookupVar(lit.Value).IsSet() {
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
	params := bashppParams(fn.params())
	if !r.bashPPCheckArgs(fn, params, args) {
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
			if param.name != "" {
				rest := append([]string(nil), args[i:]...)
				_ = r.bashPPScope.declare(param.name,
					expand.Variable{Set: true, Kind: expand.Indexed, List: rest}, false)
			}
			break
		}
		_ = r.bashPPScope.declare(param.name,
			expand.Variable{Set: true, Kind: expand.String, Str: args[i]}, false)
		if base := strings.TrimPrefix(param.declared, "*"); base != "" {
			if _, ok := r.bashPPTypes[base]; ok {
				cell := r.bashPPScope.lookup(param.name)
				cell.typeName = base
				cell.pointer = strings.HasPrefix(param.declared, "*")
				cell.nilPointer = cell.pointer && args[i] == ""
			}
		}
	}
	resultNames := bashppResultNames(fn.results())
	for _, name := range resultNames {
		if name != "" {
			_ = r.bashPPScope.declare(name, expand.Variable{Set: true, Kind: expand.String, Str: ""}, false)
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
	results := r.bashPPSettleResults(fn, resultNames)

	// Deferred calls run as the frame unwinds — on a normal return and on a
	// panic alike, which is the point of them — but not through a hard shell
	// `exit`, which is terminating everything.
	if !r.exit.exiting {
		r.bashPPRunDefers(ctx, frame.deferMark)
	} else {
		r.bashPPDeferStack = r.bashPPDeferStack[:frame.deferMark]
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
	r          *Runner
	params     []string
	inFunc     bool
	writeEnv   expand.WriteEnviron
	scope      *bashPPScope
	callDepth  int
	deferMark  int
	ret        bashPPReturnState
	deferDepth int
}

// bashPPEnterFrame pushes the frame fn's body runs in: its own shell function
// scope (so `local` and non-`local` assignments behave as in a shell function),
// its own typed scope chained to the captured definition scope (so closures
// resolve where they were written), its own positional parameters, and its own
// mark on the deferred-call stack.
func (r *Runner) bashPPEnterFrame(fn *bashPPFunc, args []string) *bashPPFrame {
	frame := &bashPPFrame{
		r:          r,
		params:     r.Params,
		inFunc:     r.inFunc,
		writeEnv:   r.writeEnv,
		scope:      r.bashPPScope,
		callDepth:  len(r.callStack),
		deferMark:  len(r.bashPPDeferStack),
		ret:        r.bashPPReturn,
		deferDepth: r.bashPPDeferDepth,
	}
	r.Params = args
	r.inFunc = true
	r.writeEnv = &overlayEnviron{parent: r.writeEnv, funcScope: true}
	r.bashPPScope = newBashPPScope(fn.scope)
	if fn.decl != nil && fn.decl.Receiver != nil && fn.receiver != nil {
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
	results := r.bashPPInvoke(ctx, fn, args)
	// A call abandoned by panic or hard termination produced no values. Do not
	// turn that control transfer into a secondary assignment-mismatch error;
	// the caller's frame must get the original unwind unchanged.
	if r.bashPPPanicking() || r.exit.exiting || r.exit.fatalExit || r.exit.err != nil {
		return
	}
	if len(d.Lhs) != len(results) {
		r.errf("assignment mismatch: %d variable(s) but %d value(s)\n",
			len(d.Lhs), len(results))
		r.exit = exitStatus{code: 2}
		return
	}
	resultTypes := bashppResultTypes(fn.results())
	for i, lhs := range d.Lhs {
		if !syntax.ValidName(lhs.Value) {
			r.errf("invalid variable name: %q\n", lhs.Value)
			r.exit = exitStatus{code: 2}
			return
		}
		r.bashPPDeclareName(lhs.Value, expand.Variable{Set: true, Kind: expand.String, Str: results[i]})
		if i < len(resultTypes) {
			declared := resultTypes[i]
			base := strings.TrimPrefix(declared, "*")
			if _, ok := r.bashPPTypes[base]; ok {
				cell := r.bashPPScope.lookup(lhs.Value)
				cell.typeName = base
				cell.pointer = strings.HasPrefix(declared, "*")
				cell.nilPointer = cell.pointer && results[i] == ""
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
		for i, name := range resultNames {
			if name != "" && i < len(ret.values) {
				r.setVarString(name, ret.values[i])
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
	vals := make([]string, len(ret.Results))
	for i, w := range ret.Results {
		vals[i] = r.bashPPExprValue(w)
	}
	r.bashPPReturn = bashPPReturnState{active: true, values: vals}
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
	entry := bashPPDeferred{call: d.Call}
	if fn, ok := r.bashPPLookupFunc(d.Call); ok {
		args, ok := r.bashPPCallValues(d.Call, fn)
		if !ok {
			return
		}
		entry.fn, entry.args = fn, args
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
	// A call this frame deferred runs one frame deeper than this one, and that
	// depth is the whole of recover's "called directly by a deferred function"
	// rule; see [Runner.bashPPRecover].
	r.bashPPDeferDepth = len(r.callStack) + 1
	defer func() {
		r.bashPPDeferDepth = savedDeferDepth
		r.bashPPPanic.running = false
	}()
	var failed exitStatus
	deferFailed := false
	for i := len(pending) - 1; i >= 0; i-- {
		d := pending[i]
		r.exit = exitStatus{}
		// A cleanup runs even while a panic is unwinding — that is the whole
		// point of it — so the panic stops halting statements for the length
		// of this call, without ceasing to be recoverable by it.
		r.bashPPPanic.running = r.bashPPPanic.active
		switch {
		case d.fn != nil:
			r.bashPPInvoke(ctx, d.fn, d.args)
		case d.predeclared != "":
			// `defer panic(v)` and `defer recover()`. The latter is the shape
			// Go documents as not working, and it does not work here either,
			// for the reason it does not there: recover IS the deferred call,
			// so nothing deferred it in turn — see [Runner.bashPPRecover].
			r.bashPPPredeclared(d.predeclared, d.call, d.args)
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
		if !deferFailed && (!r.exit.ok() || r.exit.err != nil) {
			failed, deferFailed = r.exit, true
		}
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
	variadic     bool
	defaultValue *syntax.Word
}

// bashppParams flattens a parameter list into one slot per declared name, plus
// a slot for an unnamed variadic group — `func f(...int)` — which accepts
// arguments without binding them.
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
			params = append(params, bashPPParam{name: name, declared: declared, variadic: true})
			continue
		}
		for _, n := range f.Names {
			params = append(params, bashPPParam{name: n.Value, declared: declared, defaultValue: f.Default})
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
func (r *Runner) bashPPCheckArgs(fn *bashPPFunc, params []bashPPParam, args []string) bool {
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
	switch declared {
	case "int", "int8", "int16", "int32", "int64":
		_, err := strconv.ParseInt(value, 10, 64)
		return err == nil
	case "uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		_, err := strconv.ParseUint(value, 10, 64)
		return err == nil
	case "float32", "float64":
		_, err := strconv.ParseFloat(value, 64)
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
