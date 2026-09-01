// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Evaluation of the Bash++ P1 ("Day-1") nodes.
//
// Like its counterpart in sh/syntax, this file is deliberately separate from
// runner.go: the evaluation can be written, reviewed and merged without
// touching a line the certification workstream owns. What remains owed to
// runner.go is four `case` arms in the existing command type switch, each a
// single call into this file.
//
// Nothing here runs yet. The nodes cannot appear in a tree until the parser
// dispatch is wired, so these functions are unreachable by construction, and
// their unit tests build the nodes directly rather than parsing source.
//
// ONE VALUE MODEL, NOT TWO. `:=` binds through the existing expand.Object
// machinery whenever the value is structured. Introducing a second
// representation for "a Go value in the shell" is the most likely way for this
// phase to do lasting damage: the object model already answers how a value
// crosses execve (as JSON), how it interpolates, and what happens under
// `set -o posix`, and a parallel model would have to answer all three again
// and would inevitably answer at least one differently.

// Every function below takes a ctx it may not use. That is deliberate: these
// are the bodies of the four `case` arms owed to runner.go's command type
// switch, which is passed a ctx, and a uniform signature keeps each arm a
// single call. P3 onwards, where a call actually executes a body, will use it.
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
		// Unreachable while dispatch is unwired, and a fail-safe once it is:
		// a Bash++ node in a runner that has extensions off is a bug in the
		// caller, not an input error, so it must be loud rather than silent.
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

	vr := r.bashPPValue(ctx, d.Init)
	r.setVar(name, vr)

	if d.Site == syntax.StartConst {
		prev := r.lookupVar(name)
		prev.ReadOnly = true
		r.setVar(name, prev)
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
		r.setVar(name, r.bashPPValue(ctx, d.Rhs))
		return
	}
	for i, lhs := range d.Lhs {
		if !syntax.ValidName(lhs.Value) {
			r.errf("invalid variable name: %q\n", lhs.Value)
			r.exit = exitStatus{code: 2}
			return
		}
		r.setVar(lhs.Value, r.bashPPValue(ctx, d.Rhs[i:i+1]))
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
		// A bare declaration: `var x int`. The zero value is the empty string,
		// which is also what the shell gives an unset-but-declared variable,
		// so nothing downstream has to special-case it.
		return expand.Variable{Kind: expand.String, Str: ""}
	case 1:
		return expand.Variable{Kind: expand.String, Str: r.literal(words[0])}
	default:
		strs := make([]string, len(words))
		for i, w := range words {
			strs[i] = r.literal(w)
		}
		return expand.Variable{Kind: expand.Indexed, List: strs}
	}
}

// bashPPCall evaluates a Go call in command position.
//
// P1 recognizes the SHAPE but implements no call semantics: user functions
// arrive in P3 and the builtin set is not settled. Reporting that honestly is
// the whole job here. Every shape reaching this node is Class R — already a
// bash syntax error — so a diagnostic takes nothing away from any working
// script, which is exactly why a diagnostic is permitted here and forbidden on
// a Class E shape.
func (r *Runner) bashPPCall(ctx context.Context, c *syntax.BashPPCall) {
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
