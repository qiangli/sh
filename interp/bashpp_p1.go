// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"bytes"
	"context"
	"strconv"
	"strings"

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
	if d.Site == syntax.StartTypeDecl {
		if r.bashPPTypes == nil {
			r.bashPPTypes = make(map[string]bashPPType)
		}
		if _, exists := r.bashPPTypes[name]; exists {
			r.errf("%stype %s redeclared in this session\n", r.bashErrPrefix(d.Pos()), name)
			r.exit = exitStatus{code: 2}
			return
		}
		if d.DeclType == nil {
			r.errf("%stype %s has no underlying type\n", r.bashErrPrefix(d.Pos()), name)
			r.exit = exitStatus{code: 2}
			return
		}
		if d.DeclType.Value == "enum" {
			seen := make(map[string]bool, len(d.EnumMembers))
			for _, member := range d.EnumMembers {
				if !syntax.ValidName(member.Value) {
					r.errf("BASHPP-EENUM-MEMBER: enum member %q must be an identifier\n", member.Value)
					r.exit = exitStatus{code: 2}
					return
				}
				if seen[member.Value] {
					r.errf("BASHPP-EENUM-DUPLICATE: enum %s declares member %q more than once\n", name, member.Value)
					r.exit = exitStatus{code: 2}
					return
				}
				seen[member.Value] = true
			}
		}
	}
	if d.Site == syntax.StartVar && d.DeclType != nil {
		base := strings.TrimPrefix(d.DeclType.Value, "*")
		if _, ok := r.bashPPTypes[base]; !ok && !bashPPBuiltinType(base) {
			r.errf("%sundefined type: %s\n", r.bashErrPrefix(d.Pos()), base)
			r.exit = exitStatus{code: 2}
			return
		}
	}

	// DeclType is also the in-process identity source for P3-C receiver values.
	// The visible scalar stays in the ordinary shell variable below; the named
	// type and pointer bits are attached to its lexical cell after declaration.
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
		return
	}
	if d.Site == syntax.StartTypeDecl {
		members := make([]string, len(d.EnumMembers))
		for i, member := range d.EnumMembers {
			members[i] = member.Value
		}
		r.bashPPTypes[name] = bashPPType{underlying: d.DeclType.Value, alias: d.Alias, members: members}
	}
	if d.Site == syntax.StartVar && d.DeclType != nil {
		spelling := d.DeclType.Value
		pointer := strings.HasPrefix(spelling, "*")
		base := strings.TrimPrefix(spelling, "*")
		if _, named := r.bashPPTypes[base]; named {
			cell := r.bashPPScope.lookup(name)
			cell.typeName, cell.pointer = base, pointer
			cell.nilPointer = pointer && len(d.Init) == 0
		}
	}
}

func bashPPBuiltinType(name string) bool {
	switch name {
	case "bool", "byte", "complex64", "complex128", "error", "float32", "float64",
		"int", "int8", "int16", "int32", "int64", "rune", "string",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr":
		return true
	}
	return false
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
	if d.MakeChan != nil {
		r.bashPPMakeChan(ctx, d)
		return
	}
	if d.Recv != nil {
		r.bashPPReceive(ctx, d.Recv, d.Lhs)
		return
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
	if len(d.MethodValue) > 0 {
		if len(d.Lhs) != 1 {
			r.errf("assignment mismatch: %d variable(s) but 1 value(s)\n", len(d.Lhs))
			r.exit = exitStatus{code: 2}
			return
		}
		call := &syntax.BashPPCall{Fun: d.MethodValue}
		fn, ok := r.bashPPLookupFunc(call)
		if !ok {
			if r.exit.code == 0 {
				r.errf("bash++: selector %s.%s is not a method value\n", d.MethodValue[0].Value, d.MethodValue[len(d.MethodValue)-1].Value)
				r.exit.code = 2
			}
			return
		}
		vr := r.bashPPStoreFunc(fn)
		fn.bound = d.Lhs[0].Value
		r.bashPPDeclareName(d.Lhs[0].Value, vr)
		return
	}
	// `x := f(1)` / `a, b := f()` binds a typed function's results. It is
	// handled before the tuple arity check below because a call's single text
	// Rhs never matches a multi-name left-hand side; the real arity check is
	// against the function's declared results, done inside.
	if d.Call != nil {
		if r.bashPPEnumConstruct(d) {
			return
		}
		if fn, ok := r.bashPPLookupFunc(d.Call); ok {
			r.bashPPShortDeclCall(ctx, d, fn)
			return
		}
		// `err := recover()` is the spelling a recovering defer is written
		// with, so the predeclared functions bind their results here exactly
		// as a declared function's do.
		if name := bashPPPredeclaredCall(d.Call); name != "" {
			r.bashPPShortDeclPredeclared(d, name)
			return
		}
		// The typed process boundary's inbound half: `r, err := run(...)`,
		// `out, err := capture(...)` (predeclared like panic/recover), then
		// the explicit `v, err := json.Decode(...)`, which must be answered
		// before the generic imported-selector delegation hands it to the
		// toolchain. See bashpp_capture.go.
		if r.bashPPShortDeclCapture(ctx, d) {
			return
		}
		if r.bashPPShortDeclDecode(ctx, d) {
			return
		}
		if r.bashPPShortDeclImported(ctx, d) {
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
		if len(d.Rhs) == 1 {
			if value, identity, ok := r.bashPPObjectExpr(bashPPWordSource(d.Rhs[0])); ok {
				vr := expand.NewObject(value)
				r.bashPPDeclareName(name, vr)
				cell := r.bashPPScope.lookup(name)
				if identity == nil {
					identity = &bashPPObjectIdentity{owner: name}
				}
				cell.object = identity
				return
			}
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

// bashPPEnumConstruct evaluates `v := Color(Member)`. Enum values keep their
// member spelling in the ordinary shell cell while the lexical cell carries
// the named type, matching the existing named-scalar representation.
func (r *Runner) bashPPEnumConstruct(d *syntax.BashPPShortDecl) bool {
	if d.Call == nil || len(d.Call.Fun) != 1 || len(d.Lhs) != 1 {
		return false
	}
	name := d.Call.Fun[0].Value
	typ, ok := r.bashPPTypes[name]
	if !ok || typ.underlying != "enum" {
		return false
	}
	args := r.bashPPCallArgValues(d.Call)
	value := ""
	if len(args) == 1 {
		value = args[0]
	}
	valid := false
	for _, member := range typ.members {
		if member == value {
			valid = true
			break
		}
	}
	if !valid {
		r.errf("BASHPP-EENUM-VALUE: %s is not a member of %s\n", value, name)
		r.exit = exitStatus{code: 2}
		return true
	}
	r.bashPPDeclareName(d.Lhs[0].Value, expand.Variable{Set: true, Kind: expand.String, Str: value})
	if cell := r.bashPPScope.lookup(d.Lhs[0].Value); cell != nil {
		cell.typeName = name
	}
	return true
}

func (r *Runner) bashPPSwitch(ctx context.Context, sw *syntax.BashPPSwitch) {
	if !r.objectsEnabled() {
		r.errf("bash++ switch evaluated with extensions disabled\n")
		r.exit = exitStatus{code: 2}
		return
	}
	value := r.bashPPExprValue(sw.Expr)
	var fallback *syntax.BashPPSwitchArm
	for _, arm := range sw.Arms {
		if arm.Member == nil {
			fallback = arm
			continue
		}
		if arm.Member.Value == value {
			fallback = arm
			break
		}
	}
	if fallback == nil {
		return
	}
	if r.bashPPScope != nil {
		defer r.bashPPPushScope()()
	}
	r.stmts(ctx, fallback.Stmts)
}

// bashPPShortDeclPredeclared binds the results of a predeclared call to the
// names on the left of `:=`, preserving the status the call set — which for
// `recover` is how a script tells "recovered the empty string" from "there was
// nothing to recover".
func (r *Runner) bashPPShortDeclPredeclared(d *syntax.BashPPShortDecl, name string) {
	// A call that produced no values leaves nothing to bind — `panic(v)`
	// abandons the declaration along with the rest of the statement, and a
	// diagnosed call has already reported itself.
	results, ok := r.bashPPPredeclared(name, d.Call, r.bashPPCallArgValues(d.Call))
	if !ok {
		return
	}
	if len(d.Lhs) != len(results) {
		r.errf("assignment mismatch: %d variable(s) but %d value(s)\n",
			len(d.Lhs), len(results))
		r.exit = exitStatus{code: 2}
		return
	}
	status := r.exit
	for i, lhs := range d.Lhs {
		if !syntax.ValidName(lhs.Value) {
			r.errf("invalid variable name: %q\n", lhs.Value)
			r.exit = exitStatus{code: 2}
			return
		}
		r.bashPPDeclareName(lhs.Value, expand.Variable{Set: true, Kind: expand.String, Str: results[i]})
	}
	r.exit = status
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
	if r.exit.code != 0 {
		return
	}
	// `panic` and `recover` are predeclared, so they answer only where the
	// session declared nothing of that name — the lookup above already had its
	// chance, exactly as a Go declaration shadows a predeclared identifier.
	// They are dialect state like every other extension, so POSIX mode and the
	// Classic dialect see the same "not implemented" diagnostic as any other
	// Go form, never a panic.
	if r.bashPPEnabled() && !r.PosixMode() {
		if name := bashPPPredeclaredCall(c); name != "" {
			r.bashPPPredeclared(name, c, r.bashPPCallArgValues(c))
			return
		}
		if r.bashPPCaptureCommandPosition(c) {
			return
		}
		if len(c.Fun) >= 1 {
			r.bashPPEvalSelector(ctx, c, nil)
			return
		}
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

func (r *Runner) bashPPCommandCall(ctx context.Context, c *syntax.BashPPCommandCall) {
	fn, ok := r.bashPPLookupFunc(c.Call)
	if !ok {
		if r.exit.code == 0 {
			r.errf("bash++: nested call is not a declared function\n")
			r.exit = exitStatus{code: 127}
		}
		return
	}
	args, ok := r.bashPPCallValues(c.Call, fn)
	if !ok {
		return
	}
	results := r.bashPPInvoke(ctx, fn, args)
	if r.exit.code != 0 || r.exit.exiting || r.exit.returning {
		return
	}
	words := append([]*syntax.Word(nil), c.Before...)
	for _, result := range results {
		words = append(words, &syntax.Word{Parts: []syntax.WordPart{&syntax.SglQuoted{Value: result}}})
	}
	r.cmd(ctx, &syntax.CallExpr{Args: words})
}

// bashPPEvalSelector dispatches a call through the import evaluator.
//
// values is nil for a direct call, whose arguments the evaluator receives as
// the Go source the script wrote. A DEFERRED call passes the values it captured
// when the defer ran instead, rendered back as Go literals: Go fixes a deferred
// call's arguments at the defer, so re-reading the script's words as the frame
// unwinds would hand the package whatever the variables hold by then.
func (r *Runner) bashPPEvalSelector(ctx context.Context, c *syntax.BashPPCall, values []string) {
	req, err := r.bashPPEvalRequest()
	if err == nil {
		req.Selector = make([]string, len(c.Fun))
		for i, lit := range c.Fun {
			req.Selector[i] = lit.Value
		}
		if values != nil {
			req.Args = make([]string, len(values))
			for i, value := range values {
				req.Args[i] = strconv.Quote(value)
			}
		} else {
			req.Args = make([]string, len(c.Args))
			for i, arg := range c.Args {
				var b bytes.Buffer
				_ = syntax.NewPrinter().Print(&b, arg)
				req.Args[i] = b.String()
			}
		}
		err = r.bashPPTools.eval.Call(ctx, req)
	}
	if err != nil {
		r.exit.fatal(err)
	}
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
