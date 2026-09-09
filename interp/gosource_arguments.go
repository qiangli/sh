package interp

// Sprint: #118; Story: #53; Story-ID: 99bd1de0093b
import (
	"fmt"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// goSourceCallArguments evaluates original expressions exactly once and retains
// each result cell. A computed struct, pointer, interface or channel cannot be
// reconstructed from the legacy argument word after its producer has returned.
func (r *Runner) goSourceCallArguments(call *syntax.BashPPCall, fn *bashPPFunc) ([]string, bool, error) {
	if len(call.ArgExprs) != len(call.Args) {
		return nil, false, fmt.Errorf("gosource: missing positioned call argument")
	}
	cells := make([]*bashPPCell, len(call.ArgExprs))
	for i, expr := range call.ArgExprs {
		cell, err := r.goSourceValueCell(expr)
		if err != nil {
			return nil, false, err
		}
		if cell.channel != nil && cell.channelOwner != r.bashPPConcurrent {
			return nil, false, fmt.Errorf("channel belongs to another task group")
		}
		cells[i] = bashPPCopyAssignmentCell(cell)
	}
	if fn.skipArgs > len(cells) {
		return nil, false, fmt.Errorf("gosource: missing method expression receiver")
	}
	cells = cells[fn.skipArgs:]
	args := make([]string, len(cells))
	channels := make([]*bashPPChannel, len(cells))
	interfaces := make([]*bashPPInterfaceValue, len(cells))
	for i, cell := range cells {
		// Typed aggregate arguments already carry their complete value cell.
		// Stringifying them would traverse referenced storage that another
		// goroutine may legally mutate under its own synchronization.
		if cell.vr.Kind != expand.Object {
			args[i] = cell.vr.String()
		}
		channels[i] = cell.channel
		interfaces[i] = cell.interfaceValue
	}
	r.bashPPCallCells, r.bashPPCallChannels, r.bashPPCallInterfaces = cells, channels, interfaces
	return args, true, nil
}

func (r *Runner) goSourceBuiltinResult(call *syntax.BashPPCall) (*bashPPCell, bool, error) {
	if !r.bashPPGoSource || call.CalleeExpr != nil {
		return nil, false, nil
	}
	name := bashPPPredeclaredCall(call)
	switch name {
	case "len", "cap", "append", "copy", "make", "new", "min", "max":
	default:
		return nil, false, nil
	}
	if r.bashPPFuncs[name] != nil || (r.bashPPScope != nil && r.bashPPScope.lookup(name) != nil) {
		return nil, false, nil
	}
	cell, ok := r.bashPPRunValueBuiltin(name, call)
	if !ok || cell == nil {
		return nil, true, errBashPPScalarInterrupted
	}
	return cell, true, nil
}
