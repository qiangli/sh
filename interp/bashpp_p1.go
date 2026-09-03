// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"bytes"
	"context"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Evaluation of the Bash++ P1 ("Day-1") nodes.
//
// Like its counterpart in sh/syntax, this file is deliberately separate from
// runner.go: the evaluation can be written, reviewed and merged without
// touching a line the certification workstream owns. What runner.go owns is
// one `case` arm per node in the existing command type switch, each a single
// call into this file.
//
// Every P1 node is now reached from source: the parser claims the var/const
// declarations (sh/syntax/bashpp_decl.go), the `type` bodies and the `:=`
// short declarations (sh/syntax/bashpp_short.go), and the Go-form call, and
// runner.go dispatches each to its arm below. [Runner.bashPPIf] alone remains
// unreachable from source — brace-form `if` is a recorded Day-1 deferral (see
// sh/syntax/bashpp_braceif_decision.go) — and its runner arm exists only so a
// hand-built tree receives the owner's diagnostic rather than the generic
// "unhandled command node" fallback.
//
// ONE VALUE MODEL, NOT TWO. `:=` binds through the existing expand.Object
// machinery whenever the value is structured. Introducing a second
// representation for "a Go value in the shell" is the most likely way for this
// phase to do lasting damage: the object model already answers how a value
// crosses execve (as JSON), how it interpolates, and what happens under
// `set -o posix`, and a parallel model would have to answer all three again
// and would inevitably answer at least one differently.

// Every function below takes a ctx it may not use. That is deliberate: these
// are the bodies of the `case` arms in runner.go's command type switch, which
// is passed a ctx, and a uniform signature keeps each arm a single call. The
// arms that execute a body — the call path, via the typed-function machinery
// in bashpp_func.go and the eval toolchain — do use it.
//
// bashPPDeclare evaluates var, const and type declarations.
//
// The three share an implementation because the interpreter treats them
// identically apart from mutability. `const` additionally marks the variable
// read-only, which reuses the shell's existing readonly machinery rather than
// inventing a Bash++ notion of immutability — a const that could be reassigned
// by `declare` would be a lie, and the shell already knows how to refuse that.
func (r *Runner) bashPPDeclare(ctx context.Context, d *syntax.BashPPDecl) {
	if !r.objectsEnabled() {
		// Fail-safe: a Bash++ node can only be parsed under LangBashPP, so one
		// reaching a runner that has extensions off is a bug in the caller —
		// a dialect mismatch between parser and runner, or a hand-built tree —
		// not an input error, so it must be loud rather than silent.
		r.errf("bash++ declaration evaluated with extensions disabled\n")
		r.exit = exitStatus{code: 2}
		return
	}
	name := d.Name.Value
	if !syntax.ValidName(name) {
		r.errf("invalid variable name: %q\n", name)
		r.exit = exitStatus{code: 2}
		return
	}
	if r.bashPPScope == nil {
		// A runner which reached a Bash++ node without Reset having built the
		// outermost block; give it one rather than binding nowhere.
		r.bashPPScope = newBashPPScope(nil)
	}

	// The typed forms are not claimed by the parser yet, so DeclType is nil on
	// every node that reaches this from source. It is read here rather than
	// ignored because the zero value it implies is already the answer: a bare
	// `var x int` has no initializer, and [Runner.bashPPValue] gives an empty
	// Init the zero value. When the typed site lands, what changes is the
	// zero value's KIND, which is the one thing DeclType will then name.
	vr := r.bashPPValue(ctx, d.Init)
	// A declaration which shadows an exported shell variable inherits the
	// export, so the child process and the script agree on the value. It does
	// not export a name the shell was not already exporting: a Go declaration
	// is a local, and locals do not cross execve.
	if prev := r.writeEnv.Get(name); prev.Exported {
		vr.Exported = true
	}
	// `const` marks the cell readonly as well as refusing assignment through
	// the scope, so the shell's own readonly paths — `unset`, `declare -r` —
	// see it too; a const those could quietly reassign would be a lie.
	if err := r.bashPPScope.declare(name, vr, d.Site == syntax.StartConst); err != nil {
		r.errf("%s%v\n", r.bashErrPrefix(d.Pos()), err)
		r.exit = exitStatus{code: 2}
	}
}

// bashPPShortDecl evaluates `x := 42` and `x, y := f()`.
//
// The tuple form is bound positionally. A length mismatch is a hard error
// rather than a partial bind: binding what fits and leaving the rest at their
// previous values would leave the shell in a state no reader could predict
// from the source line.
func (r *Runner) bashPPShortDecl(ctx context.Context, d *syntax.BashPPShortDecl) {
	if !r.objectsEnabled() {
		r.errf("bash++ short declaration evaluated with extensions disabled\n")
		r.exit = exitStatus{code: 2}
		return
	}
	if r.bashPPScope == nil {
		r.bashPPScope = newBashPPScope(nil)
	}
	// `greet := func(who string) { … }` binds the FUNCTION, not a call's
	// results, so it is answered before the call and tuple paths: there is one
	// value, it is the closure, and it captures the scope at this point.
	if d.FuncLit != nil {
		if len(d.Lhs) != 1 {
			r.errf("assignment mismatch: %d variable(s) but 1 value(s)\n", len(d.Lhs))
			r.exit = exitStatus{code: 2}
			return
		}
		name := d.Lhs[0].Value
		if !syntax.ValidName(name) {
			r.errf("invalid variable name: %q\n", name)
			r.exit = exitStatus{code: 2}
			return
		}
		fn, vr := r.bashPPMakeClosure(d.FuncLit)
		fn.bound = name
		r.bashPPDeclareName(name, vr)
		return
	}
	// `x := f(1)` / `a, b := f()` binds a typed function's results. It is
	// handled before the tuple arity check below because a call's single text
	// Rhs never matches a multi-name left-hand side; the real arity check is
	// against the function's declared results, done inside.
	if d.Call != nil {
		if fn, ok := r.bashPPLookupFunc(d.Call); ok {
			r.bashPPShortDeclCall(ctx, d, fn)
			return
		}
	}
	if len(d.Lhs) != 1 && len(d.Lhs) != len(d.Rhs) {
		r.errf("assignment mismatch: %d variable(s) but %d value(s)\n",
			len(d.Lhs), len(d.Rhs))
		r.exit = exitStatus{code: 2}
		return
	}
	if len(d.Lhs) == 1 {
		name := d.Lhs[0].Value
		if !syntax.ValidName(name) {
			r.errf("invalid variable name: %q\n", name)
			r.exit = exitStatus{code: 2}
			return
		}
		r.bashPPDeclareName(name, r.bashPPValue(ctx, d.Rhs))
		return
	}
	for i, lhs := range d.Lhs {
		if !syntax.ValidName(lhs.Value) {
			r.errf("invalid variable name: %q\n", lhs.Value)
			r.exit = exitStatus{code: 2}
			return
		}
		r.bashPPDeclareName(lhs.Value, r.bashPPValue(ctx, d.Rhs[i:i+1]))
	}
}

// bashPPDeclareName binds one name in the innermost block, reporting a
// redeclaration the way the keyword forms do. `:=` differs from `var` in Go by
// permitting a redeclaration when at least one name on the left is new, which
// is a rule about the whole left-hand side and so belongs to the site that has
// one; this is only the per-name half the two forms share.
func (r *Runner) bashPPDeclareName(name string, vr expand.Variable) {
	if prev := r.writeEnv.Get(name); prev.Exported {
		vr.Exported = true
	}
	if err := r.bashPPScope.declare(name, vr, false); err != nil {
		r.errf("%v\n", err)
		r.exit = exitStatus{code: 2}
	}
}

// bashPPValue turns an unevaluated right-hand side into a variable.
//
// A scalar stays a STRING, deliberately. The design of record settles this:
// making `x := 42` produce an object would mean `echo $x` had to unwrap it,
// every arithmetic context had to unwrap it, and every external command had to
// be handed something. A shell variable holding "42" already behaves correctly
// in all three. Objects earn their keep for structured values, which have no
// faithful string form; they cost more than they return for scalars.
func (r *Runner) bashPPValue(ctx context.Context, words []*syntax.Word) expand.Variable {
	switch len(words) {
	case 0:
		// A bare declaration: `var x int`. The zero value is the empty
		// string. Set is true all the same — a declared identifier holds its
		// zero value, it is not absent — so `${x-fallback}` yields the empty
		// string rather than the fallback, and execEnv does not treat the
		// binding as an unset name to be scrubbed from the child's
		// environment.
		return expand.Variable{Set: true, Kind: expand.String, Str: ""}
	case 1:
		return expand.Variable{Set: true, Kind: expand.String, Str: r.literal(words[0])}
	default:
		strs := make([]string, len(words))
		for i, w := range words {
			strs[i] = r.literal(w)
		}
		return expand.Variable{Set: true, Kind: expand.Indexed, List: strs}
	}
}

// bashPPCall evaluates a Go call in command position.
//
// A call resolves in order: a typed function declared in this session (P3-A),
// then an imported selector via the eval toolchain, and finally an honest
// diagnostic for a shape no phase implements. Every shape reaching this node
// is Class R — already a bash syntax error — so a diagnostic takes nothing
// away from any working script, which is exactly why a diagnostic is
// permitted here and forbidden on a Class E shape.
func (r *Runner) bashPPCall(ctx context.Context, c *syntax.BashPPCall) {
	// A call to a typed function declared in this session runs the function.
	// It is checked before the external eval toolchain so a user's own `func`
	// always wins over a same-named tool binding.
	if fn, ok := r.bashPPLookupFunc(c); ok {
		args, ok := r.bashPPCallValues(c, fn)
		if !ok {
			return
		}
		r.bashPPInvoke(ctx, fn, args)
		return
	}
	if r.bashPPEnabled() && !r.PosixMode() && len(c.Fun) >= 1 {
		req, err := r.bashPPEvalRequest()
		if err == nil {
			req.Selector = make([]string, len(c.Fun))
			for i, lit := range c.Fun {
				req.Selector[i] = lit.Value
			}
			req.Args = make([]string, len(c.Args))
			for i, arg := range c.Args {
				var b bytes.Buffer
				_ = syntax.NewPrinter().Print(&b, arg)
				req.Args[i] = b.String()
			}
			err = r.bashPPTools.eval.Call(ctx, req)
		}
		if err != nil {
			r.exit.fatal(err)
		}
		return
	}
	name := ""
	for i, lit := range c.Fun {
		if i > 0 {
			name += "."
		}
		name += lit.Value
	}
	r.errf("bash++: %s(...) is recognized but not implemented in this phase\n", name)
	r.exit = exitStatus{code: 127}
}

// bashPPIf is a placeholder so the node has an owner from the start.
//
// The brace-form `if` is the one Day-1 site whose commit point is unbounded —
// only the absence of `then` after the MATCHING brace decides it — so it is
// gated behind that design question rather than behind implementation effort.
func (r *Runner) bashPPIf(ctx context.Context, i *syntax.BashPPIf) {
	r.errf("bash++: brace-form if is not implemented in this phase\n")
	r.exit = exitStatus{code: 2}
}

// bashPPUnsupported is the shared response for a recognized Go form belonging
// to a phase that has not landed.
//
// It exists so the Class rule has ONE implementation instead of being restated
// at each call site: a Class R form may be diagnosed, because bash rejects it
// anyway; a Class E form must never be, because a script relying on today's
// shell meaning would break. Returning false means "fall back to shell".
func (r *Runner) bashPPUnsupported(class syntax.SiteClass, form string) bool {
	if class == syntax.ClassE {
		return false
	}
	r.errf("bash++: %s is recognized but not supported in this phase\n", form)
	r.exit = exitStatus{code: 2}
	return true
}
