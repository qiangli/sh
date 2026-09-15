// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Call is the decorator context: the one value every decorator receives,
// whether it is a Bash++ function whose first parameter is the predeclared
// `*Call` or a native [DecoratorFunc] from the [Decorators] registry.
//
// The shape is deliberately go-decorator's, not Python's. A decorator never
// sees or changes the target's signature; it observes the call through this
// context and decides whether the rest of the chain runs by calling [Call.Next].
// Skipping Next skips the body: the target then yields zero results and
// whatever Status the decorator left behind.
type Call struct {
	// Name is the declared name of the decorated function; a method is
	// spelled Type.Method.
	Name string
	// Site is the file:line of the call being decorated, and Caller the
	// function it was made from ("main" at the top level).
	Site   string
	Caller string
	// Args are the call's arguments in shell rendering: `"$@"` for a shell
	// function, the bound parameter values for a typed one.
	Args []string
	// Results are the target's result values once Next has run. A decorator
	// may rewrite them; they are type-checked back into the declared result
	// cells when the chain completes.
	Results []string
	// Status is the exit status the call will report.
	Status int
	// Agentic reports whether the DECLARATION is marked agentic. It is
	// the declaration's contract, not the caller frame's state.
	Agentic bool
	// Advised is the rule id when this decorator was applied by [Advice]
	// rather than written in source, and empty otherwise.
	Advised string

	chain *bashPPDecoratorChain
}

// Next runs the next decorator in the chain, or the body when this is the
// innermost decorator. It may be called more than once — a retry decorator
// does exactly that — and each call re-runs everything inside it.
func (c *Call) Next(ctx context.Context) {
	if c.chain != nil {
		c.chain.next(ctx)
	}
}

// DecoratorArg is one evaluated argument of a decorator line, positional
// when Name is empty.
type DecoratorArg struct {
	Name  string
	Value string
}

// DecoratorFunc is a native decorator. It is resolved by name at call time,
// after any Bash++ function of the same name, so a script may shadow it.
// A non-nil error aborts the call with status 1.
type DecoratorFunc func(ctx context.Context, c *Call, args []DecoratorArg) error

// Decorators registers native decorators by name, mirroring [CommandResolver]:
// the engine exposes only the slot, and every implementation lives with the
// embedder.
func Decorators(m map[string]DecoratorFunc) RunnerOption {
	return func(r *Runner) error {
		r.bashPPNativeDecorators = m
		return nil
	}
}

// DecoratorSpec is one decorator line applied by policy advice: the rule id
// it came from, the decorator to resolve, and its already-evaluated args.
type DecoratorSpec struct {
	ID   string
	Name string
	Args []DecoratorArg
}

// AdviceFunc is consulted each time a function is registered — by
// declaration, `eval`, `source` or redefinition — with its name, source file
// and agentic mark, and answers the decorators policy adds to it.
type AdviceFunc func(name, file string, agentic bool) []DecoratorSpec

// Advice installs registration-time policy advice. Advised decorators are
// add-only and outermost: they wrap every source decorator and never
// interpose between an author's decorator and the body. Application is
// idempotent by rule id, so re-registering never stacks duplicates, and
// `declare -f` keeps printing source decorators only.
func Advice(fn AdviceFunc) RunnerOption {
	return func(r *Runner) error {
		r.bashPPAdvice = fn
		return nil
	}
}

// bashPPDecorated is the side-table entry for a decorated SHELL function. A
// typed function carries its decorators on its declaration; a shell function
// stores only its body in [Runner.Funcs], which embedders may replace, so the
// entry keeps the body identity exactly as the agentic table does.
type bashPPDecorated struct {
	body       *syntax.Stmt
	decorators []*syntax.BashPPDecorator
	advised    []DecoratorSpec
}

// bashPPDecoratorRung is one resolved-at-call-time link of a chain.
type bashPPDecoratorRung struct {
	name     string
	args     []*syntax.Word
	argNames []*syntax.Lit
	// literal holds pre-evaluated advised arguments; args is nil then.
	literal []DecoratorArg
	advised string
}

// bashPPDecoratorFrame is the target frame's execution context, captured when
// the chain starts and put back for the body: a decorator runs in its own
// ordinary frame, so by the time its Next reaches the body the runner is
// positioned in the decorator's scope, not the target's.
type bashPPDecoratorFrame struct {
	params   []string
	inFunc   bool
	writeEnv expand.WriteEnviron
	scope    *bashPPScope
	agentic  bool
	ret      bashPPReturnState
	typeArgs map[string]syntax.BashPPTypeExpr
	filename string
}

func (r *Runner) bashPPDecoratorFrame() bashPPDecoratorFrame {
	return bashPPDecoratorFrame{
		params: r.Params, inFunc: r.inFunc, writeEnv: r.writeEnv, scope: r.bashPPScope,
		agentic: r.bashPPAgentic, ret: r.bashPPReturn, typeArgs: r.bashPPTypeParamArgs,
		filename: r.filename,
	}
}

func (r *Runner) bashPPRestoreDecoratorFrame(f bashPPDecoratorFrame) {
	r.Params, r.inFunc, r.writeEnv, r.bashPPScope = f.params, f.inFunc, f.writeEnv, f.scope
	r.bashPPAgentic, r.bashPPReturn, r.bashPPTypeParamArgs = f.agentic, f.ret, f.typeArgs
	r.filename = f.filename
}

// bashPPDecoratorChain is one decorated invocation in flight.
type bashPPDecoratorChain struct {
	r     *Runner
	call  *Call
	rungs []bashPPDecoratorRung
	// depth is the rung Next will run; len(rungs) means the body.
	depth int
	// cell is the script-visible Call struct, addressed by ptr; it exists
	// only once a Bash++ decorator needs it.
	cell *bashPPCell
	ptr  *bashPPPointer
	// body runs the target body in the target frame and reports its status
	// and settled results.
	body func(ctx context.Context) (int, []string)
	// frame is the target's context, restored around the body.
	frame bashPPDecoratorFrame
	// stack is the decorator names executing when the chain began; cycles
	// are detected against it, and the body runs with it restored so a
	// decorated function called from a decorated body is not a cycle.
	stack []string
	// targetDepth is the target frame's index on the call stack. The body
	// runs with that frame rotated to the top, so FUNCNAME[0] is still the
	// decorated function and FUNCNAME[1] the innermost decorator.
	targetDepth int
	bodyDefers  []bashPPDeferred
	bodyRan     bool
	failed      bool
}

const bashPPDecoratorCallType = "Call"

// bashPPPredeclaredCallSource is the predeclared Call as the script sees it.
// Args and Results are the shell renderings of the values: this interpreter
// boxes every value as its shell string, so the []any of the design is the
// string slice here, with positions preserved by index.
const bashPPPredeclaredCallSource = `type Call struct {
	Name string
	Site string
	Caller string
	Args []string
	Results []string
	Status int
	Agentic bool
	Advised string
}
func (c *Call) Next() { }
`

// bashPPEnsureCallType installs the predeclared Call type and its Next method
// on first use, unless the script declared its own Call, which shadows the
// predeclared one exactly as a declaration shadows `any`.
func (r *Runner) bashPPEnsureCallType(ctx context.Context) bool {
	if _, exists := r.bashPPTypes[bashPPDecoratorCallType]; exists {
		return r.bashPPPredeclaredCall
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(bashPPPredeclaredCallSource), "")
	if err != nil {
		r.errf("BASHPP-EDECO-CALL: predeclared Call is unavailable: %v\n", err)
		r.exit.code = 1
		return false
	}
	saved := r.exit
	for _, stmt := range f.Stmts {
		switch cm := stmt.Cmd.(type) {
		case *syntax.BashPPDecl:
			r.bashPPDeclare(ctx, cm)
		case *syntax.BashPPFuncDecl:
			r.bashPPFuncDecl(cm)
		}
	}
	if r.exit.code != 0 {
		return false
	}
	r.exit = saved
	if next := r.bashPPMethods[bashPPDecoratorCallType]["Next"]; next != nil {
		next.decoratorNext = true
	}
	r.bashPPPredeclaredCall = true
	return true
}

// bashPPShadowPredeclaredCall drops the predeclared Call so a script's own
// `type Call` can take the name. Decorators that named the predeclared type
// stop being decorators, which is the EDECO-SIG diagnostic at their next use.
func (r *Runner) bashPPShadowPredeclaredCall(name string) {
	if name != bashPPDecoratorCallType || !r.bashPPPredeclaredCall {
		return
	}
	delete(r.bashPPTypes, name)
	delete(r.bashPPMethods, name)
	r.bashPPPredeclaredCall = false
}

// bashPPDecoratorSignature reports whether fn is a decorator: its first
// parameter is the predeclared *Call.
func (r *Runner) bashPPDecoratorSignature(fn *bashPPFunc) bool {
	if fn == nil || fn.decl == nil || fn.decl.Receiver != nil {
		return false
	}
	params := bashppParams(fn.params())
	if len(params) == 0 || params[0].variadic {
		return false
	}
	ptr, ok := params[0].typ.(*syntax.BashPPPointerType)
	if !ok {
		return false
	}
	named, ok := ptr.Element.(*syntax.BashPPNamedType)
	if !ok || named.Name == nil || named.Name.Value != bashPPDecoratorCallType || len(named.TypeArgs) > 0 {
		return false
	}
	// A script-declared Call is not the predeclared one.
	if _, exists := r.bashPPTypes[bashPPDecoratorCallType]; exists && !r.bashPPPredeclaredCall {
		return false
	}
	return true
}

// bashPPRegisterDecorators validates a declaration's decorator stack and
// records any advice for name. It reports false when the declaration must be
// refused.
func (r *Runner) bashPPRegisterDecorators(name string, pos syntax.Pos, decorators []*syntax.BashPPDecorator, agentic bool) ([]DecoratorSpec, bool) {
	for _, d := range decorators {
		if strings.Contains(d.Name.Value, ".") {
			r.errf("%sBASHPP-EDECO-RESERVED: @%s: namespaced decorators are reserved\n", r.bashErrPrefix(d.Pos()), d.Name.Value)
			r.exit.code = 2
			return nil, false
		}
		if d.Name.Value == name {
			r.errf("%sBASHPP-EDECO-SELF: %s cannot decorate itself\n", r.bashErrPrefix(d.Pos()), name)
			r.exit.code = 2
			return nil, false
		}
	}
	if r.bashPPAdvice == nil {
		return nil, true
	}
	var advised []DecoratorSpec
	seen := make(map[string]bool)
	for _, spec := range r.bashPPAdvice(name, r.filename, agentic) {
		if spec.Name == "" || spec.ID == "" || seen[spec.ID] {
			continue
		}
		seen[spec.ID] = true
		advised = append(advised, spec)
	}
	return advised, true
}

// bashPPDecoratedShellFunc returns the side-table entry for name when its
// body is still the registered one.
func (r *Runner) bashPPDecoratedShellFunc(name string) *bashPPDecorated {
	entry := r.bashPPDecoratedFuncs[name]
	if entry == nil || entry.body != r.Funcs[name] {
		return nil
	}
	return entry
}

func bashPPDecoratorRungs(decorators []*syntax.BashPPDecorator, advised []DecoratorSpec) []bashPPDecoratorRung {
	rungs := make([]bashPPDecoratorRung, 0, len(decorators)+len(advised))
	for _, spec := range advised {
		rungs = append(rungs, bashPPDecoratorRung{name: spec.Name, literal: spec.Args, advised: spec.ID})
	}
	for _, d := range decorators {
		rungs = append(rungs, bashPPDecoratorRung{name: d.Name.Value, args: d.Args, argNames: d.ArgNames})
	}
	return rungs
}

// bashPPNewDecoratorChain prepares the chain for one invocation. It is called
// from inside the target frame — after the agentic, argument and FUNCNEST
// gates and inside the trap bracket — so the frame it captures is the one the
// body must run in.
func (r *Runner) bashPPNewDecoratorChain(name string, rungs []bashPPDecoratorRung, args []string, agentic bool) *bashPPDecoratorChain {
	call := &Call{Name: name, Args: append([]string(nil), args...), Agentic: agentic}
	pos := r.curStmtPos
	if pos.IsValid() {
		call.Site = fmt.Sprintf("%s:%d", r.filename, pos.Line())
	}
	call.Caller = "main"
	// The target's own frame is already on the stack; its caller is the one
	// below it.
	if n := len(r.callStack); n >= 2 {
		call.Caller = r.callStack[n-2].funcName
	}
	chain := &bashPPDecoratorChain{r: r, call: call, rungs: rungs, frame: r.bashPPDecoratorFrame(),
		stack: append([]string(nil), r.bashPPDecoratorStack...), targetDepth: len(r.callStack) - 1}
	call.chain = chain
	return chain
}

// run executes the whole chain, outermost decorator first, and reports
// whether it completed. On completion call.Status and call.Results are the
// outcome; the caller installs them.
func (c *bashPPDecoratorChain) run(ctx context.Context) bool {
	c.next(ctx)
	return !c.failed
}

// next is Call.Next: it runs the rung at the current depth, or the body past
// the last one, then restores the depth so the same decorator may call it
// again.
func (c *bashPPDecoratorChain) next(ctx context.Context) {
	r := c.r
	if c.failed || r.exit.exiting || r.bashPPPanicking() {
		return
	}
	depth := c.depth
	c.depth++
	defer func() { c.depth = depth }()
	if depth >= len(c.rungs) {
		c.runBody(ctx)
		return
	}
	rung := c.rungs[depth]
	c.call.Advised = rung.advised
	// A cycle is a decorator's own chain reaching a decorator that is
	// already running: the target is on the decorator stack and so is the
	// rung. The same decorator stacked twice on one declaration is not a
	// cycle, nor is a decorated function called from a decorated body.
	if bashPPDecoratorActive(r.bashPPDecoratorStack, c.call.Name) && bashPPDecoratorActive(r.bashPPDecoratorStack, rung.name) {
		c.fail("BASHPP-EDECO-CYCLE: decorator %s is already decorating an active call\n", rung.name)
		return
	}
	if fn := r.bashPPFuncs[rung.name]; fn != nil {
		if !r.bashPPDecoratorSignature(fn) {
			c.fail("BASHPP-EDECO-SIG: %s is not a decorator: its first parameter must be *Call\n", rung.name)
			return
		}
		c.runScripted(ctx, rung, fn)
		return
	}
	if native := r.bashPPNativeDecorators[rung.name]; native != nil {
		c.runNative(ctx, rung, native)
		return
	}
	if r.Funcs[rung.name] != nil {
		c.fail("BASHPP-EDECO-SIG: %s is not a decorator: a shell function has no *Call parameter\n", rung.name)
		return
	}
	c.fail("BASHPP-EDECO-UNDEF: decorator %s is not defined\n", rung.name)
}

func bashPPDecoratorActive(stack []string, name string) bool {
	for _, active := range stack {
		if active == name {
			return true
		}
	}
	return false
}

// fail records a decorator diagnostic with status 1 and marks the chain
// failed. The failure sequence lets an enclosing chain — one whose decorator
// is itself decorated — notice that a nested chain failed inside an
// otherwise ordinary frame.
func (c *bashPPDecoratorChain) fail(format string, args ...any) {
	r := c.r
	r.errf(format, args...)
	r.exit.code = 1
	r.bashPPDecoratorFailSeq++
	c.failed = true
}

// runBody runs the target in its own frame and records the outcome on the
// context. Deferred calls the body scheduled are set aside so that they run
// when the TARGET frame unwinds, after the decorators, rather than when the
// innermost decorator's frame does.
func (c *bashPPDecoratorChain) runBody(ctx context.Context) {
	r := c.r
	decorator := r.bashPPDecoratorFrame()
	stack := r.bashPPDecoratorStack
	r.bashPPDecoratorStack = c.stack
	r.bashPPRestoreDecoratorFrame(c.frame)
	top := len(r.callStack) - 1
	rotated := c.targetDepth >= 0 && c.targetDepth < top
	if rotated {
		target := r.callStack[c.targetDepth]
		copy(r.callStack[c.targetDepth:], r.callStack[c.targetDepth+1:top+1])
		r.callStack[top] = target
	}
	mark := len(r.bashPPDeferStack)
	status, results := c.body(ctx)
	if rotated && len(r.callStack) == top+1 {
		target := r.callStack[top]
		copy(r.callStack[c.targetDepth+1:top+1], r.callStack[c.targetDepth:top])
		r.callStack[c.targetDepth] = target
	}
	if len(r.bashPPDeferStack) > mark {
		c.bodyDefers = append(c.bodyDefers, r.bashPPDeferStack[mark:]...)
		r.bashPPDeferStack = r.bashPPDeferStack[:mark]
	}
	r.bashPPRestoreDecoratorFrame(decorator)
	r.bashPPDecoratorStack = stack
	c.bodyRan = true
	if r.exit.exiting || r.bashPPPanicking() {
		return
	}
	c.call.Status = status
	c.call.Results = results
	r.exit = exitStatus{}
	c.sync()
}

// runNative invokes a registry decorator with the rung's evaluated args.
func (c *bashPPDecoratorChain) runNative(ctx context.Context, rung bashPPDecoratorRung, fn DecoratorFunc) {
	r := c.r
	args := rung.literal
	if rung.args != nil {
		args = make([]DecoratorArg, 0, len(rung.args))
		positional := len(rung.args) - len(rung.argNames)
		for i, w := range rung.args {
			arg := DecoratorArg{Value: r.bashPPExprValue(w)}
			if i >= positional {
				arg.Name = rung.argNames[i-positional].Value
			}
			args = append(args, arg)
		}
	}
	c.load()
	r.bashPPDecoratorStack = append(r.bashPPDecoratorStack, rung.name)
	err := fn(ctx, c.call, args)
	r.bashPPDecoratorStack = r.bashPPDecoratorStack[:len(r.bashPPDecoratorStack)-1]
	if err != nil {
		c.fail("BASHPP-EDECO-NATIVE: @%s: %v\n", rung.name, err)
		return
	}
	c.sync()
}

// bashPPDecoratorContextName is the hidden binding the synthesized call
// passes as the decorator's *Call argument. It lives in a scope pushed for
// the duration of that one call, so the script never sees it.
const bashPPDecoratorContextName = "__bashpp_call"

// runScripted invokes a Bash++ decorator as an ordinary frame through
// bashPPInvoke, so defer, panic unwinding, recover and FUNCNEST all apply,
// and the frame is visible in FUNCNAME. The decorator's own arguments are
// evaluated here, per invocation, by the same binding contract as any call.
func (c *bashPPDecoratorChain) runScripted(ctx context.Context, rung bashPPDecoratorRung, fn *bashPPFunc) {
	r := c.r
	if !c.materialize(ctx) {
		c.failed = true
		return
	}
	c.store()
	callWords := []*syntax.Word{{Parts: []syntax.WordPart{&syntax.Lit{Value: bashPPDecoratorContextName}}}}
	var names []*syntax.Lit
	if rung.args != nil {
		callWords = append(callWords, rung.args...)
		names = rung.argNames
	} else {
		for _, arg := range rung.literal {
			callWords = append(callWords, &syntax.Word{Parts: []syntax.WordPart{&syntax.SglQuoted{Value: arg.Value}}})
			if arg.Name != "" {
				names = append(names, &syntax.Lit{Value: arg.Name})
			}
		}
	}
	call := &syntax.BashPPCall{Fun: []*syntax.Lit{{Value: rung.name}}, Args: callWords, ArgNames: names}

	scope := r.bashPPScope
	r.bashPPScope = newBashPPScope(scope)
	ctxCell := &bashPPCell{declType: &syntax.BashPPPointerType{Element: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: bashPPDecoratorCallType}}}, typeName: bashPPDecoratorCallType}
	bashPPStoreCellValue(ctxCell, c.ptr, nil)
	r.bashPPScope.entries[bashPPDecoratorContextName] = ctxCell
	r.bashPPDecoratorStack = append(r.bashPPDecoratorStack, rung.name)
	failSeq := r.bashPPDecoratorFailSeq
	args, ok := r.bashPPCallValues(call, fn)
	if ok {
		r.bashPPInvoke(ctx, fn, args)
	}
	r.bashPPDecoratorStack = r.bashPPDecoratorStack[:len(r.bashPPDecoratorStack)-1]
	r.bashPPScope = scope
	// A binding diagnostic, a dialect diagnostic (status 2), a nested
	// decorator failure, an exit or a panic all abort the call; the
	// decorator's ordinary last status does not, because the outcome is what
	// it left on the context.
	if !ok || r.exit.code == 2 || r.exit.exiting || r.exit.fatalExit || r.bashPPPanicking() || failSeq != r.bashPPDecoratorFailSeq {
		c.failed = true
		return
	}
	r.exit = exitStatus{}
	c.load()
}

// materialize creates the script-visible Call on first need.
func (c *bashPPDecoratorChain) materialize(ctx context.Context) bool {
	r := c.r
	if c.cell != nil {
		return true
	}
	if !r.bashPPEnsureCallType(ctx) {
		return false
	}
	lit := &syntax.BashPPCompositeLit{LitType: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: bashPPDecoratorCallType}}}
	ptr, err := r.bashPPCompositeAddress(lit)
	if err != nil {
		r.errf("BASHPP-EDECO-CALL: %v\n", err)
		r.exit.code = 1
		return false
	}
	c.ptr, c.cell = ptr, ptr.target
	if r.bashPPDecoratorChains == nil {
		r.bashPPDecoratorChains = make(map[*bashPPCell]*bashPPDecoratorChain)
	}
	r.bashPPDecoratorChains[c.cell] = c
	return true
}

func (c *bashPPDecoratorChain) release() {
	if c.cell != nil {
		delete(c.r.bashPPDecoratorChains, c.cell)
	}
}

// store writes the Go-side context into the script-visible struct.
func (c *bashPPDecoratorChain) store() {
	if c.cell == nil {
		return
	}
	obj, _ := c.cell.vr.Obj.(map[string]any)
	meta := bashPPCellMeta(c.cell)
	if obj == nil || meta == nil {
		return
	}
	set := func(field string, value any, child *bashPPCollectionMeta) {
		bashPPStorageSetField(obj, meta.mapping, field, value, child)
	}
	strings := func(field string, values []string) {
		typ := &syntax.BashPPCollectionType{Kind: "slice", Element: &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "string"}}}
		child := &bashPPCollectionMeta{kind: "slice", typ: typ, sequence: make([]*bashPPCollectionMeta, len(values))}
		seq := make([]any, len(values))
		for i, v := range values {
			seq[i] = v
		}
		set(field, seq, child)
	}
	set("Name", c.call.Name, nil)
	set("Site", c.call.Site, nil)
	set("Caller", c.call.Caller, nil)
	set("Status", c.call.Status, nil)
	set("Agentic", c.call.Agentic, nil)
	set("Advised", c.call.Advised, nil)
	strings("Args", c.call.Args)
	strings("Results", c.call.Results)
}

// load reads the fields a decorator may have rewritten back into the
// Go-side context.
func (c *bashPPDecoratorChain) load() {
	if c.cell == nil {
		return
	}
	obj, _ := c.cell.vr.Obj.(map[string]any)
	if obj == nil {
		return
	}
	if v, ok := bashPPStorageGet(obj, "Status"); ok {
		c.call.Status = bashPPDecoratorInt(v)
	}
	if v, ok := bashPPStorageGet(obj, "Results"); ok {
		c.call.Results = bashPPDecoratorStrings(v)
	}
	if v, ok := bashPPStorageGet(obj, "Args"); ok {
		c.call.Args = bashPPDecoratorStrings(v)
	}
}

// sync pushes the Go-side context into the struct when one exists.
func (c *bashPPDecoratorChain) sync() { c.store() }

func bashPPDecoratorInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(x))
		return n
	}
	n, _ := strconv.Atoi(fmt.Sprint(v))
	return n
}

func bashPPDecoratorStrings(v any) []string {
	switch x := v.(type) {
	case nil:
		return nil
	case []any:
		out := make([]string, len(x))
		for i, e := range x {
			if e == nil {
				continue
			}
			out[i] = fmt.Sprint(e)
		}
		return out
	case []string:
		return append([]string(nil), x...)
	}
	return nil
}

// bashPPDecoratorNext answers the predeclared Call.Next method: it finds the
// chain the receiver belongs to and advances it.
func (r *Runner) bashPPDecoratorNext(ctx context.Context, fn *bashPPFunc) []string {
	r.bashPPResultCells = nil
	r.bashPPCallCells, r.bashPPCallChannels, r.bashPPCallInterfaces, r.bashPPCallSpread = nil, nil, nil, false
	var chain *bashPPDecoratorChain
	if fn.receiver != nil && fn.receiver.pointerValue != nil {
		chain = r.bashPPDecoratorChains[fn.receiver.pointerValue.target]
	}
	if chain == nil {
		r.errf("BASHPP-EDECO-CALL: Next called outside a decorator chain\n")
		r.exit.code = 1
		return nil
	}
	chain.load()
	chain.next(ctx)
	if !chain.failed && !r.exit.exiting && !r.bashPPPanicking() {
		r.exit = exitStatus{}
	}
	return nil
}

// bashPPInvokeDecorated runs a typed function's body through its decorator
// chain. It returns the settled results, or false when the chain failed;
// the caller's own settle path is bypassed because the context is now the
// source of truth for status and results.
func (r *Runner) bashPPInvokeDecorated(ctx context.Context, fn *bashPPFunc, args []string, resultNames []string, rungs []bashPPDecoratorRung) ([]string, bool) {
	name := fn.name()
	if recv := fn.decl.Receiver; recv != nil && recv.RecvType != nil && fn.decl.Name != nil {
		name = recv.RecvType.Value + "." + fn.decl.Name.Value
	}
	chain := r.bashPPNewDecoratorChain(name, rungs, args, fn.decl.Agentic != nil)
	defer chain.release()
	chain.body = func(ctx context.Context) (int, []string) {
		if body := fn.body(); body != nil {
			r.stmts(ctx, body.Stmts)
		}
		if r.exit.exiting || r.bashPPPanicking() {
			return int(r.exit.code), nil
		}
		results := r.bashPPSettleResults(fn, resultNames)
		if len(resultNames) > 0 && results != nil {
			results = r.bashPPFinalResults(results, resultNames)
		}
		return int(r.exit.code), results
	}
	ok := chain.run(ctx)
	if len(chain.bodyDefers) > 0 {
		r.bashPPDeferStack = append(r.bashPPDeferStack, chain.bodyDefers...)
	}
	if !ok || r.exit.exiting || r.bashPPPanicking() {
		return nil, false
	}
	count := bashppResultCount(fn.results())
	results := chain.call.Results
	r.exit = exitStatus{code: uint8(chain.call.Status)}
	if count == 0 {
		if len(results) != 0 {
			r.errf("BASHPP-EDECO-RESULT: %s declares no results; decorator supplied %d\n", fn.name(), len(results))
			r.exit.code = 1
			return nil, false
		}
		return nil, true
	}
	if len(results) == 0 {
		// A skipped body yields zero results.
		results = make([]string, count)
		resultTypes := bashppResultTypeExprs(fn.results())
		for i := range results {
			if i < len(resultTypes) {
				// A scalar zero renders as itself; a structured zero has
				// no string form and stays empty, as an unnamed result does.
				if zero, meta := r.bashPPZeroValue(resultTypes[i]); meta == nil && zero != nil {
					results[i] = fmt.Sprint(zero)
				}
			}
		}
	}
	if len(results) != count {
		r.errf("BASHPP-EDECO-RESULT: %s declares %d result(s); decorator supplied %d\n", fn.name(), count, len(results))
		r.exit.code = 1
		return nil, false
	}
	resultTypes := bashppResultTypeExprs(fn.results())
	for i, value := range results {
		if i >= len(resultTypes) || resultTypes[i] == nil {
			continue
		}
		if declared := bashPPTypeText(resultTypes[i]); !r.bashPPValueFits(declared, value) {
			r.errf("BASHPP-EDECO-RESULT: %s: cannot use %q as %s result %d\n", fn.name(), value, declared, i+1)
			r.exit.code = 1
			return nil, false
		}
		if i < len(resultNames) && resultNames[i] != "" {
			r.setVarString(resultNames[i], value)
		}
	}
	// Results were settled by the chain, so the frame's own return state
	// must not settle them again.
	r.bashPPReturn = bashPPReturnState{}
	return results, true
}

// bashPPCallDecorated runs a shell function's body through its decorator
// chain, positioned exactly where Runner.call would run the body.
func (r *Runner) bashPPCallDecorated(ctx context.Context, name string, entry *bashPPDecorated, args []string, run func(context.Context)) {
	rungs := bashPPDecoratorRungs(entry.decorators, entry.advised)
	chain := r.bashPPNewDecoratorChain(name, rungs, args, r.bashPPAgenticFunc(name))
	defer chain.release()
	chain.body = func(ctx context.Context) (int, []string) {
		run(ctx)
		if !r.exit.exiting && !r.bashPPPanicking() {
			r.exit.returning = false
		}
		return int(r.exit.code), nil
	}
	ok := chain.run(ctx)
	if len(chain.bodyDefers) > 0 {
		r.bashPPDeferStack = append(r.bashPPDeferStack, chain.bodyDefers...)
	}
	if !ok || r.exit.exiting || r.bashPPPanicking() {
		return
	}
	r.exit = exitStatus{code: uint8(chain.call.Status)}
}

// bashPPPrintDecorators writes a function's source decorators in the shape
// `declare -f` prints them, one per line above the declaration. Advised
// decorators are policy, not source, and are never printed.
func (r *Runner) bashPPPrintDecorators(decorators []*syntax.BashPPDecorator) {
	for _, d := range decorators {
		var buf strings.Builder
		buf.WriteString("@")
		buf.WriteString(d.Name.Value)
		buf.WriteString("(")
		positional := len(d.Args) - len(d.ArgNames)
		for i, arg := range d.Args {
			if i > 0 {
				buf.WriteString(", ")
			}
			if i >= positional {
				buf.WriteString(d.ArgNames[i-positional].Value)
				buf.WriteString(": ")
			}
			var w strings.Builder
			syntax.NewPrinter().Print(&w, arg)
			buf.WriteString(w.String())
		}
		buf.WriteString(")\n")
		r.out(buf.String())
	}
}
