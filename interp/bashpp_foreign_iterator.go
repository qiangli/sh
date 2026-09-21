package interp

import (
	"context"
	"errors"
	"fmt"
	"mvdan.cc/sh/v3/polyglot"
	"sync"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// A foreign iterator is lazy: merely assigning it cannot leak a child. Its
// first range owns the B14 line process and closes/reaps it on every exit.
type bashPPForeignIterator struct {
	mu      sync.Mutex
	module  *polyglot.Module
	name    string
	args    []any
	element string
	used    bool
}

func (r *Runner) bashPPRangeForeignIterator(ctx context.Context, rng *syntax.BashPPRange) bool {
	if rng.Chan == nil || r.bashPPScope == nil {
		return false
	}
	cell := r.bashPPScope.lookup(r.literal(rng.Chan))
	if cell == nil || cell.vr.Kind != expand.Object {
		return false
	}
	iterator, ok := cell.vr.Obj.(*bashPPForeignIterator)
	if !ok {
		return false
	}
	iterator.mu.Lock()
	used := iterator.used
	iterator.used = true
	iterator.mu.Unlock()
	if used {
		return true
	}
	if len(rng.Names) > 1 {
		r.errf("BASHPP-ERANGE-TYPE: iterator range permits one variable\n")
		r.exit.code = 2
		return true
	}
	if len(rng.Names) > 0 && !rng.Define.IsValid() {
		r.errf("BASHPP-ERANGE-TYPE: iterator range requires a declaration\n")
		r.exit.code = 2
		return true
	}
	stdout, stderr := r.bashPPWriter(r.stdout), r.bashPPWriter(r.stderr)
	process, err := StartForeignIterator(ctx, iterator.module, iterator.name, iterator.args, stdout, stderr)
	if err != nil {
		r.errf("foreign iterator: %v\n", err)
		r.exit.code = 1
		return true
	}
	defer func() {
		if err := process.Close(); err != nil && !errors.Is(err, context.Canceled) {
			r.errf("foreign iterator: %v\n", err)
			r.exit.code = 1
		}
		fmt.Fprint(stderr, process.Stderr())
	}()
	for line := range process.Lines() {
		frame, err := iterator.module.DecodeIteratorFrame(ctx, line, iterator.element)
		if err != nil {
			r.errf("foreign iterator: %v\n", err)
			r.exit.code = 1
			return true
		}
		fmt.Fprint(stdout, frame.Stdout)
		fmt.Fprint(stderr, frame.Stderr)
		value := frame.Value
		if !r.bashPPForeignRangeValue(ctx, rng, value, iterator.element) {
			return true
		}
	}
	status, err := process.Wait()
	if err != nil {
		r.errf("foreign iterator: %v\n", err)
		r.exit.code = 1
	} else if status != 0 {
		r.exit.code = uint8(status)
	}
	return true
}

// Foreign aggregate values retain their Object cell through the range binding;
// fmt.Sprint would discard byte/handle identity and make fields inaccessible.
func (r *Runner) bashPPForeignRangeValue(ctx context.Context, rng *syntax.BashPPRange, value any, element string) bool {
	switch value.(type) {
	case nil, []byte, []any, map[string]any, *polyglot.Handle:
		leave := r.bashPPPushScope()
		if len(rng.Names) > 0 && rng.Names[0].Value != "_" {
			name := rng.Names[0].Value
			r.bashPPDeclareName(name, expand.NewObject(value))
			cell := r.bashPPScope.lookup(name)
			cell.declType = bashPPRangeNamedType(foreignShellType(element))
		}
		r.cmd(r.bashPPTaskContext(ctx), rng.Body)
		leave()
		return r.bashPPRangeControl()
	default:
		return r.bashPPRangeIteration(ctx, rng, value, bashPPRangeNamedType(element), nil, nil, nil)
	}
}
