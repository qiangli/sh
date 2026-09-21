package interp

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// A foreign iterator is lazy: merely assigning it cannot leak a child. Its
// first range owns the B14 line process and closes/reaps it on every exit.
type bashPPForeignIterator struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
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
	process, err := bashPPStartCmd(ctx, iterator.cmd, bashPPDefaultLineBuffer)
	if err != nil {
		r.errf("foreign iterator: %v\n", err)
		r.exit.code = 1
		return true
	}
	defer func() { _ = process.Close(); fmt.Fprint(r.stderr, process.Stderr()) }()
	for line := range process.Lines() {
		var value any
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			r.errf("foreign iterator: invalid NDJSON: %v\n", err)
			r.exit.code = 1
			return true
		}
		if number, ok := value.(json.Number); ok {
			if iterator.element == "int" {
				value, err = number.Int64()
			} else {
				value, err = number.Float64()
			}
			if err != nil {
				r.errf("foreign iterator: %v\n", err)
				r.exit.code = 1
				return true
			}
		}
		if !r.bashPPRangeIteration(ctx, rng, value, bashPPRangeNamedType(iterator.element), nil, nil, nil) {
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
