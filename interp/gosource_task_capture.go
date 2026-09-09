// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"fmt"
	"reflect"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// GoSource lexical capture identity across tasks.
//
// A Bash++ task snapshot deep copies every mutable value reachable from the
// shell environment (see [bashPPObjectCloner]); the parent keeps running
// concurrently, and sharing interpreter heap between the two would be a data
// race. That is the right default for classic Bash++, where `go f()` is a
// shell construct and a task is a private copy of the shell.
//
// It is the wrong default for an ORIGINAL Go program. Go closures capture
// their free variables BY REFERENCE, so a goroutine and its parent name the
// same variable:
//
//	var counter int
//	var mu sync.Mutex
//	for i := 0; i < 8; i++ {
//	    wg.Add(1)
//	    go func() {
//	        mu.Lock()
//	        counter++      // must increment the counter the parent prints
//	        mu.Unlock()
//	        wg.Done()
//	    }()
//	}
//	wg.Wait()
//	fmt.Println(counter)   // 8 in Go; 0 under a deep-copy snapshot
//
// bashpp_task.go already carves out imported native handles, so a
// `sync.Mutex` or `atomic.Int64` names one object across the boundary. But the
// plain interpreted cell the mutex PROTECTS was still deep copied, so a
// correctly synchronized original Go program printed the parent's untouched
// copy. This file closes that gap for GoSource programs only.
//
// # The rule
//
// Identity is granted to exactly the cells a task's closure captures
// lexically, and to nothing else:
//
//   - Only in GoSource mode. Classic Bash++ `go f()` keeps its deep-copy
//     snapshot unchanged; [Runner.bashPPGoSourceTaskCapture] returns nil there
//     and every hook below is a no-op on a nil set.
//   - Only names the body actually USES as variables, computed by the lexical
//     scope walker in this file. A name the body re-declares — a `:=`, a `var`,
//     a nested parameter, a range or select binding — is a different variable
//     from the outer one that happens to share a spelling, and the outer cell
//     is NOT shared. Text inside a string literal is not a variable use at all.
//   - Only PLAIN interpreted cells. Native handles keep the reviewed
//     descriptor-copy rule in bashpp_task.go, and channels keep their own
//     identity and ownership machinery; neither is re-decided here.
//
// # Why precision, not over-approximation
//
// An earlier revision collected every [syntax.Lit] the body mentioned and
// argued that over-approximating was "safe because a superset of the free
// variables still resolves". It is not safe. Sharing is not a no-op on a cell
// the task never touches: it removes the deep copy, so the PARENT's live cell
// is spliced into a concurrently running task's environment, where an alias, a
// pointer target or a captured closure scope can reach and mutate it — for a
// variable the program never asked to share. `fmt.Println("counter")` would
// have shared an unrelated outer `counter`, and a body whose own `x := …`
// shadows an outer `x` would have shared the outer one it can never name. The
// walker below therefore reports EXACTNESS, and an inexact analysis shares
// nothing:
//
//   - A construct the walker does not model returns exact=false, the capture
//     set is nil, and the task falls back to the classic deep-copy snapshot.
//     That direction loses Go's by-reference capture for that one launch (a
//     visible, testable wrong answer) rather than silently aliasing a cell the
//     program never named (an invisible one).
//
// # Race safety
//
// A shared cell is shared exactly as a Go variable is, which makes
// synchronization the program's responsibility, as in Go. That is not a
// loophole in practice: every native `mu.Lock()`, `wg.Wait()` and
// `atomic.Add` is a request that serializes on the dependency session's own
// Go mutexes (see [bashPPNativeSession.request]), so a program which
// synchronizes its shared variable the way Go requires also establishes the
// Go happens-before edges the race detector checks. A program which does NOT
// synchronize races here because it races in Go too — reproducing the
// original's meaning includes reproducing that.

// bashPPGoSourcePin pins the function value a launched task resolved to.
//
// `go f()` where f is a variable holding a closure must name ONE function: the
// value f held when the `go` statement ran. Go evaluates the function value in
// the launching goroutine, and so do we — once, in the parent, before the
// snapshot, so the capture analysis and the task itself agree on which body is
// running and a computed callee such as `go factory()()` is never evaluated a
// second time in the child.
//
// The pin carries the closure HANDLE rather than the parent's *bashPPFunc, so
// the child resolves it through its OWN cloned closure registry: same function,
// the child's copy of everything the snapshot legitimately copied.
type bashPPGoSourcePin struct {
	call   *syntax.BashPPCall
	handle string
}

// bashPPGoSourceTaskCapture is the set of cells a launched GoSource task must
// share with its parent rather than copy, plus the callee pin to install on the
// child. A nil set means "copy everything", which is the classic Bash++
// behavior.
func (r *Runner) bashPPGoSourceTaskCapture(call *syntax.BashPPCall) (map[*bashPPCell]bool, *bashPPGoSourcePin) {
	if r == nil || !r.bashPPGoSource || call == nil {
		return nil, nil
	}
	body, params, env, pin := r.bashPPGoSourceTaskBody(call)
	if r.exit.code != 0 || r.exit.err != nil {
		return nil, pin
	}
	if body == nil || env == nil {
		r.bashPPGoSourceCaptureUnsupported(call, "the launched callee does not resolve to an original function body")
		return nil, pin
	}
	free, exact := bashPPGoSourceFreeNames(body, params)
	if !exact {
		r.bashPPGoSourceCaptureUnsupported(call, "the body contains a construct the lexical capture analysis does not model")
		return nil, pin
	}
	if len(free) == 0 {
		// Exact, and the body captures nothing. There is nothing to share and
		// nothing unanswered; the deep-copy snapshot is the right answer.
		return nil, pin
	}
	shared := make(map[*bashPPCell]bool, len(free))
	for name := range free {
		if _, imported := r.bashPPImports[name]; imported {
			// A package name is not a variable, so `fmt` in `fmt.Println`
			// never nominates a cell even if one shares the spelling.
			continue
		}
		cell := env.lookup(name)
		if cell != nil && r.bashPPGoSourceSharable(cell) {
			shared[cell] = true
		}
	}
	if len(shared) == 0 {
		return nil, pin
	}
	return shared, pin
}

// bashPPGoSourceCaptureUnsupported refuses a launch the capture analysis
// cannot answer exactly.
//
// The alternative — quietly returning a nil capture set — is the failure this
// review exists to remove. A nil set means the classic deep-copy snapshot, and
// for an ORIGINAL Go program that is not a conservative fallback but a
// different program: the goroutine gets its own copy of a variable Go says it
// shares, so a correctly synchronized original prints the parent's untouched
// value and looks like it merely computed the wrong number. A refusal with a
// diagnostic is a visible, reportable gap; a silent deep copy is a wrong answer
// wearing a green test.
//
// Classic Bash++ is untouched: this is only ever reached in GoSource mode.
func (r *Runner) bashPPGoSourceCaptureUnsupported(call *syntax.BashPPCall, reason string) {
	if r.bashPPPanicking() {
		return
	}
	r.exit.fatal(fmt.Errorf("%sgosource: unsupported task capture: %s; an original Go closure captures its free variables by reference and this launch cannot be given that meaning",
		r.bashErrPrefix(call.Pos()), reason))
}

// bashPPGoSourceTaskBody reports the launched body, its parameter names, the
// lexical environment the body's free variables resolve against, and the pin
// that keeps the child on the same resolved function.
//
// An immediately invoked literal — which is what `go func(){…}()` is — closes
// over the launching scope. A named callee closes over the environment it was
// declared in, which is what its [bashPPFunc] already carries. A closure held
// in a variable, or produced by a computed callee, is RESOLVED here, once.
func (r *Runner) bashPPGoSourceTaskBody(call *syntax.BashPPCall) (*syntax.Block, map[string]bool, *bashPPScope, *bashPPGoSourcePin) {
	if lit := call.FuncLit; lit != nil {
		body, params := bashPPGoSourceFuncBody(lit, nil)
		return body, params, r.bashPPScope, nil
	}
	fn, pin := r.bashPPGoSourceTaskFunc(call)
	if fn == nil {
		return nil, nil, nil, pin
	}
	env := fn.scope
	if env == nil {
		env = r.bashPPScope
	}
	if lit := fn.lit; lit != nil {
		body, params := bashPPGoSourceFuncBody(lit, nil)
		return body, params, env, pin
	}
	if decl := fn.decl; decl != nil {
		body, params := bashPPGoSourceFuncBody(nil, decl)
		return body, params, env, pin
	}
	return nil, nil, nil, pin
}

// bashPPGoSourceTaskFunc resolves a non-literal callee to the exact function
// the task will run, without evaluating anything twice.
//
// A declared `func` and a closure held in a variable are pure lookups. A
// computed callee is the only form that must be EVALUATED, and evaluating it
// here is what Go does — the function value is computed in the launching
// goroutine — so the returned pin, not a second evaluation in the child, is
// what the task calls.
func (r *Runner) bashPPGoSourceTaskFunc(call *syntax.BashPPCall) (*bashPPFunc, *bashPPGoSourcePin) {
	if call.CalleeExpr != nil {
		cell, err := r.goSourceValueCell(call.CalleeExpr)
		if err != nil {
			r.exit.fatal(err)
			return nil, nil
		}
		if cell == nil || cell.vr.Kind != expand.String {
			return nil, nil
		}
		fn, ok := r.bashPPClosure(cell.vr.Str)
		if !ok {
			return nil, nil
		}
		return fn, &bashPPGoSourcePin{call: call, handle: cell.vr.Str}
	}
	if len(call.Fun) != 1 {
		// A selector callee is a method; its receiver binding is resolved by
		// call dispatch, which this analysis does not duplicate.
		return nil, nil
	}
	name := call.Fun[0].Value
	// A lexical binding is considered BEFORE a package-level `func` of the
	// same name, because that is what Go's scoping says: an inner
	//
	//	f := func() { … }
	//
	// shadows a file-scope `func f()` for the rest of its block, and `go f()`
	// there launches the local closure. Consulting r.bashPPFuncs first made
	// the analysis walk the global body while the pin — and therefore the
	// task — ran the local one, so the capture set was computed from a
	// function that was never launched.
	if cell := r.bashPPScope.lookup(name); cell != nil {
		if cell.vr.Kind != expand.String {
			// The name is bound here to something that is not a function
			// value. It still shadows the declared func, so there is no
			// original body to launch through this analysis.
			return nil, nil
		}
		fn, ok := r.bashPPClosure(cell.vr.Str)
		if !ok {
			return nil, nil
		}
		return fn, &bashPPGoSourcePin{call: call, handle: cell.vr.Str}
	}
	// A closure held in a shell variable: the cell's value is the handle, so
	// the exact function is resolvable without running anything.
	if vr := r.lookupVar(name); vr.Kind == expand.String {
		if fn, ok := r.bashPPClosure(vr.Str); ok {
			return fn, &bashPPGoSourcePin{call: call, handle: vr.Str}
		}
	}
	if fn, ok := r.bashPPFuncs[name]; ok {
		return fn, nil
	}
	return nil, nil
}

// bashPPGoSourceFuncBody reports a function's body and the names its signature
// binds. Parameters and named results are both bindings of the function's own
// scope: they shadow an outer variable of the same spelling, and a task's
// `go func(n int){…}(i)` therefore copies `i` by value the way Go does.
func bashPPGoSourceFuncBody(lit *syntax.BashPPFuncLit, decl *syntax.BashPPFuncDecl) (*syntax.Block, map[string]bool) {
	names := make(map[string]bool)
	switch {
	case lit != nil:
		bashPPGoSourceAddFieldNames(names, lit.Params)
		bashPPGoSourceAddFieldNames(names, lit.Results)
		return lit.Body, names
	case decl != nil:
		bashPPGoSourceAddFieldNames(names, decl.Params)
		bashPPGoSourceAddFieldNames(names, decl.Results)
		if recv := decl.Receiver; recv != nil && recv.Name != nil {
			names[recv.Name.Value] = true
		}
		return decl.Body, names
	}
	return nil, names
}

func bashPPGoSourceAddFieldNames(names map[string]bool, fields []*syntax.BashPPField) {
	for _, field := range fields {
		if field == nil {
			continue
		}
		for _, name := range field.Names {
			if name != nil {
				names[name.Value] = true
			}
		}
	}
}

// bashPPGoSourceSharable decides, ONCE per cell, whether identity may be
// granted to it, and remembers the answer.
//
// The decision must be made before the cell can be shared with any task,
// because after that the parent is no longer the only goroutine touching it:
// re-inspecting the payload on a later launch would read a value a running task
// is concurrently writing, which is a data race in the interpreter itself
// rather than in the program it runs. (`go test -race` on a loop that launches
// the same closure repeatedly reports exactly that.)
//
// # Why a nested task never inspects a shared payload
//
// The memo below is the ownership record, and it is the SUPERSET of everything
// ever shared: a cell is shared only after this function has written an entry
// for it, and [Runner.subshell] hands each task its own clone of the memo (see
// api.go). So inside a task:
//
//   - a cell the parent shared is present in the inherited memo under the SAME
//     pointer, and is answered from the memo without reading the payload;
//   - a cell that is absent from the memo was deep copied into this task, or
//     was declared inside it, and is therefore private to this runner — the
//     only goroutine that can read or write it is this one.
//
// That is the race proof. It holds for arbitrarily nested launches because
// each level clones the level above's memo before its task can run.
//
// The classification itself prefers the cell's DECLARED TYPE, which Go fixes at
// the declaration and which no concurrent writer can change. Payload inspection
// is the fallback for an inferred binding only, and it is reached only on a
// cell this runner privately owns.
func (r *Runner) bashPPGoSourceSharable(cell *bashPPCell) bool {
	if cell == nil {
		return false
	}
	if decided, ok := r.bashPPGoSourceSharableCells[cell]; ok {
		return decided
	}
	decided := r.bashPPGoSourceSharableCell(cell)
	if r.bashPPGoSourceSharableCells == nil {
		r.bashPPGoSourceSharableCells = make(map[*bashPPCell]bool)
	}
	r.bashPPGoSourceSharableCells[cell] = decided
	return decided
}

// bashPPGoSourceSharableCell restricts identity to a plain interpreted cell.
//
// A native handle is deliberately excluded: bashpp_task.go already preserves
// the object it names, by copying the descriptor and carrying Session/Handle
// across, and that rule stays the one authority on native identity. A channel
// is excluded for the same reason — it owns its own cross-task identity.
func (r *Runner) bashPPGoSourceSharableCell(cell *bashPPCell) bool {
	if cell == nil || cell.channel != nil || cell.constant {
		return false
	}
	if plain, decided := r.bashPPGoSourceSharableType(cell.declType, 0); decided {
		return plain
	}
	// An inferred binding has no declared type to answer from. This runner
	// privately owns the cell (see the memo argument above), so reading its
	// payload here races with nobody.
	return !bashPPCellHoldsNative(cell)
}

// bashPPGoSourceSharableType answers from the declared type alone, reporting
// whether it could answer at all.
//
// A type expression is immutable AST fixed at the declaration, so this answer
// is stable for the cell's whole life — which is what makes it usable while
// other goroutines hold the variable. A package-qualified name is a dependency
// type (`sync.Mutex`, `atomic.Int64`, `os.File`), and identity for those stays
// bashpp_task.go's to decide. Anything this function does not model reports
// "undecided" rather than guessing.
func (r *Runner) bashPPGoSourceSharableType(typ syntax.BashPPTypeExpr, depth int) (plain, decided bool) {
	if typ == nil {
		return false, false
	}
	if depth > 32 {
		// A type expression this deep is not one this function models. Report
		// undecided rather than granting: an unmodelled shape must never fall
		// through to "plain" merely by running out of budget.
		return false, false
	}
	switch typ := typ.(type) {
	case *syntax.BashPPNamedType:
		if typ.Name == nil {
			return false, false
		}
		if strings.Contains(typ.Name.Value, ".") {
			// Qualified by a package: a dependency type.
			return false, true
		}
		if _, imported := r.bashPPImports[typ.Name.Value]; imported {
			return false, true
		}
		for _, arg := range typ.TypeArgs {
			if arg == nil {
				return false, false
			}
			argPlain, argDecided := r.bashPPGoSourceSharableType(arg.ArgType, depth+1)
			if !argDecided {
				return false, false
			}
			if !argPlain {
				return false, true
			}
		}
		return true, true
	case *syntax.BashPPPointerType:
		// A pointer to a dependency value names that dependency object.
		return r.bashPPGoSourceSharableType(typ.Element, depth+1)
	case *syntax.BashPPCollectionType:
		// [N]T, []T and map[K]V are plain exactly when everything they can
		// hold is plain.
		if typ.Key != nil {
			keyPlain, keyDecided := r.bashPPGoSourceSharableType(typ.Key, depth+1)
			if !keyDecided {
				return false, false
			}
			if !keyPlain {
				return false, true
			}
		}
		return r.bashPPGoSourceSharableType(typ.Element, depth+1)
	}
	return false, false
}

// bashPPCellHoldsNative reports whether a cell's payload is, or contains, an
// imported native handle.
//
// It is total rather than depth-limited. An earlier revision stopped at depth 8
// and answered "no native here", which GRANTS identity to whatever sits below —
// a native handle nested ten containers deep would have been shared as if it
// were a plain value. Recursion is bounded by a visited set over the container
// pointers instead, so a cyclic payload terminates without a depth cap, and any
// payload shape this function does not model is reported as native-holding so
// that an unmodelled value is copied rather than aliased.
func bashPPCellHoldsNative(cell *bashPPCell) bool {
	if cell == nil {
		return false
	}
	seen := make(map[any]bool)
	var holds func(any) bool
	holds = func(value any) bool {
		switch value := value.(type) {
		case nil:
			return false
		case *bashPPBridgeValue:
			return true
		case bashPPBridgeValue:
			return true
		case map[string]any:
			key := reflect.ValueOf(value).Pointer()
			if seen[key] {
				return false
			}
			seen[key] = true
			for _, item := range value {
				if holds(item) {
					return true
				}
			}
			return false
		case []any:
			if len(value) == 0 {
				return false
			}
			key := reflect.ValueOf(value).Pointer()
			if seen[key] {
				return false
			}
			seen[key] = true
			for _, item := range value {
				if holds(item) {
					return true
				}
			}
			return false
		case string, bool, int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64, float32, float64:
			return false
		}
		// Not a shape this function models. Fail closed: report it as native so
		// the cell is copied under the reviewed bashpp_task.go rule instead of
		// being aliased on an assumption.
		return true
	}
	return holds(cell.vr.Obj)
}
