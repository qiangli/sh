// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"strconv"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Evaluation of the Bash++ P3-A ("typed functions") nodes: `func` declarations,
// `return`, and `defer`.
//
// TWO NAMESPACES, ALREADY RECONCILED. A Bash++ func binds its parameters and
// named results as TYPED lexical bindings, not as a second kind of shell
// variable, and it leans on a property vars.go already guarantees: an ordinary
// shell assignment to a name a lexical binding owns writes THROUGH to that
// binding (see [Runner.setVar]). So `func f() (n int) { n=5; return }` needs no
// special machinery — `n=5` updates the same cell the bare return reads, and a
// closure over an outer `var` observes writes for the same reason. Only the
// func's own locals live in the shell function scope pushed alongside.

// bashPPFunc is a registered Go-form function: its declaration and the lexical
// scope captured where it was defined, which its body's free identifiers close
// over rather than resolving them at the call site.
type bashPPFunc struct {
	decl  *syntax.BashPPFuncDecl
	scope *bashPPScope
}

// bashPPDeferred is one entry on the deferred-call stack: the call to run when
// the enclosing func returns, and the arguments captured — already evaluated —
// at the point `defer` ran, which is what gives Go's "arguments are evaluated
// when the defer statement executes" rule.
type bashPPDeferred struct {
	call *syntax.BashPPCall
	args []string
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
	if !syntax.ValidName(name) {
		r.errf("invalid function name: %q\n", name)
		r.exit = exitStatus{code: 2}
		return
	}
	if r.bashPPFuncs == nil {
		r.bashPPFuncs = make(map[string]*bashPPFunc, 4)
	}
	var captured *bashPPScope
	if r.bashPPScope != nil {
		captured = r.bashPPScope.snapshot()
	}
	r.bashPPFuncs[name] = &bashPPFunc{decl: d, scope: captured}
}

// bashPPLookupFunc resolves a single-name call selector to a registered typed
// function. A dotted selector (`pkg.Fn`) is never a local function.
func (r *Runner) bashPPLookupFunc(c *syntax.BashPPCall) (*bashPPFunc, bool) {
	if len(c.Fun) != 1 {
		return nil, false
	}
	fn, ok := r.bashPPFuncs[c.Fun[0].Value]
	return fn, ok
}

// bashPPCallArgValues evaluates each call argument to a single string. Values
// follow the dialect's existing convention that a bare word is its own literal
// and an expansion is expanded, exactly as a `:=` right-hand side does, so
// `f($x)` passes the value of x while `f(x)` passes the string "x".
func (r *Runner) bashPPCallArgValues(c *syntax.BashPPCall) []string {
	args := make([]string, len(c.Args))
	for i, w := range c.Args {
		args[i] = r.bashPPExprValue(w)
	}
	return args
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

// bashPPInvoke calls a typed function with already-evaluated arguments and
// returns its result values. The exit status is set on r: an explicit `return`
// of values succeeds with status 0, while a result-less function keeps the
// body's last status (or the code named by a bash-style `return n`).
func (r *Runner) bashPPInvoke(ctx context.Context, fn *bashPPFunc, args []string) []string {
	decl := fn.decl
	params := bashppParamNames(decl.Params)
	if len(args) != len(params) {
		r.errf("%s: expected %d argument(s), got %d\n", decl.Name.Value, len(params), len(args))
		r.exit = exitStatus{code: 2}
		return nil
	}
	if limit, _ := strconv.Atoi(r.envGet("FUNCNEST")); limit > 0 && len(r.callStack) >= limit {
		r.errf("%s: maximum function nesting level exceeded (%d)\n", decl.Name.Value, limit)
		r.exit.code = 1
		return nil
	}

	// Save the caller's execution context. The func runs with its own shell
	// function scope (so `local` and non-`local` assignments behave as in a
	// shell function) and its own typed scope chained to the captured
	// definition scope (so closures resolve where they were written).
	oldParams := r.Params
	r.Params = args
	oldInFunc := r.inFunc
	r.inFunc = true
	origEnv := r.writeEnv
	r.writeEnv = &overlayEnviron{parent: r.writeEnv, funcScope: true}
	origScope := r.bashPPScope
	r.bashPPScope = newBashPPScope(fn.scope)
	r.callStack = append(r.callStack, callFrame{funcName: decl.Name.Value})
	deferMark := len(r.bashPPDeferStack)
	oldReturn := r.bashPPReturn
	r.bashPPReturn = bashPPReturnState{}
	r.bashPPFuncActive++
	defer func() { r.bashPPFuncActive-- }()

	// Parameters and named results are typed bindings; a shell assignment in
	// the body writes through to them, which is what lets a named result be
	// set with `n=5` and read back by a bare return.
	for i, name := range params {
		_ = r.bashPPScope.declare(name, expand.Variable{Set: true, Kind: expand.String, Str: args[i]}, false)
	}
	resultNames := bashppResultNames(decl.Results)
	for _, name := range resultNames {
		if name != "" {
			_ = r.bashPPScope.declare(name, expand.Variable{Set: true, Kind: expand.String, Str: ""}, false)
		}
	}

	if decl.Body != nil {
		r.stmts(ctx, decl.Body.Stmts)
	}

	results := r.bashPPFinishResults(decl, resultNames)

	// Deferred calls run as the frame unwinds, in reverse of the order they
	// were deferred, unless a hard shell `exit` is terminating everything.
	if !r.exit.exiting {
		r.bashPPRunDefers(ctx, deferMark)
	} else {
		r.bashPPDeferStack = r.bashPPDeferStack[:deferMark]
	}

	r.writeEnv = origEnv
	r.bashPPScope = origScope
	r.callStack = r.callStack[:len(r.callStack)-1]
	r.Params = oldParams
	r.inFunc = oldInFunc
	r.bashPPReturn = oldReturn
	// A Go-form return is consumed at the func boundary, exactly as a shell
	// function's `return` is in [Runner.call]; it must not unwind the caller.
	r.exit.returning = false
	return results
}

// bashPPShortDeclCall invokes a typed function for `x := f()` / `a, b := g()`
// and binds its results to the left-hand names positionally, reporting an arity
// mismatch against the function's actual results rather than the call's text.
func (r *Runner) bashPPShortDeclCall(ctx context.Context, d *syntax.BashPPShortDecl, fn *bashPPFunc) {
	results := r.bashPPInvoke(ctx, fn, r.bashPPCallArgValues(d.Call))
	if r.exit.err != nil {
		return
	}
	if len(d.Lhs) != len(results) {
		r.errf("assignment mismatch: %d variable(s) but %d value(s)\n",
			len(d.Lhs), len(results))
		r.exit = exitStatus{code: 2}
		return
	}
	for i, lhs := range d.Lhs {
		if !syntax.ValidName(lhs.Value) {
			r.errf("invalid variable name: %q\n", lhs.Value)
			r.exit = exitStatus{code: 2}
			return
		}
		r.bashPPDeclareName(lhs.Value, expand.Variable{Set: true, Kind: expand.String, Str: results[i]})
	}
}

// bashPPFinishResults reconciles what a function returns with what it declared,
// setting the exit status as a side effect and reporting an arity mismatch.
func (r *Runner) bashPPFinishResults(decl *syntax.BashPPFuncDecl, resultNames []string) []string {
	count := bashppResultCount(decl.Results)
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
			r.errf("%s: too many return values for a function with no results\n", decl.Name.Value)
			r.exit.code = 2
		}
		return nil
	}

	// A return that names values must name exactly as many as declared.
	if ret.active && len(ret.values) > 0 {
		if len(ret.values) != count {
			r.errf("%s: returned %d value(s) but declared %d result(s)\n",
				decl.Name.Value, len(ret.values), count)
			r.exit.code = 2
			return nil
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

// bashPPReturnStmt evaluates a Go-form return, recording its values and
// unwinding the body through the shell's existing return machinery.
func (r *Runner) bashPPReturnStmt(ctx context.Context, ret *syntax.BashPPReturn) {
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
	r.bashPPDeferStack = append(r.bashPPDeferStack, bashPPDeferred{
		call: d.Call,
		args: r.bashPPCallArgValues(d.Call),
	})
}

// bashPPRunDefers runs the deferred calls pushed above mark, most recent first,
// then trims the stack back to mark. A return in flight is paused across the
// defers and resumes afterwards, matching Go, where a deferred call runs even
// as the function is returning.
func (r *Runner) bashPPRunDefers(ctx context.Context, mark int) {
	pending := r.bashPPDeferStack[mark:]
	// Detach so a deferred call which itself defers cannot grow the slice we
	// are iterating.
	r.bashPPDeferStack = r.bashPPDeferStack[:mark]
	savedReturning := r.exit.returning
	r.exit.returning = false
	var failed exitStatus
	deferFailed := false
	for i := len(pending) - 1; i >= 0; i-- {
		d := pending[i]
		r.exit = exitStatus{}
		if fn, ok := r.bashPPLookupFunc(d.call); ok {
			r.bashPPInvoke(ctx, fn, d.args)
		} else {
			// A deferred call to something that is not a typed function runs as an
			// ordinary command, which is what makes `defer log(...)` reach a shell
			// helper of that name.
			r.call(ctx, d.call.Pos(), append([]string{d.call.Fun[len(d.call.Fun)-1].Value}, d.args...))
		}
		// Cleanup failures are observable. Keep the first failure in execution
		// order while still running every remaining defer, then restore the
		// enclosing function's return status when all cleanups succeeded.
		if !deferFailed && (!r.exit.ok() || r.exit.err != nil || r.exit.fatalExit) {
			failed, deferFailed = r.exit, true
		}
	}
	if deferFailed {
		r.exit = failed
	} else {
		r.exit.returning = savedReturning
	}
}

// bashppParamNames flattens a parameter list into its declared names in order.
func bashppParamNames(fields []*syntax.BashPPField) []string {
	var names []string
	for _, f := range fields {
		for _, n := range f.Names {
			names = append(names, n.Value)
		}
	}
	return names
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
