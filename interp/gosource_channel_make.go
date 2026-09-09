package interp

// Sprint: #118; Story: #51; Story-ID: 825f8083451e
import (
	"context"
	"fmt"
	"go/constant"
	"mvdan.cc/sh/v3/syntax"
	"strconv"
	"strings"
)

// Keep original reference-bearing values in their interpreter-owned storage.
// Scalar channels and native-owned element types can use actual Go channels
// in the same dependency session without copying original reference identity.
func (r *Runner) goSourceNativeChannelElement(typ syntax.BashPPTypeExpr, seen map[string]bool) bool {
	if typ == nil {
		return false
	}
	if r.bashPPNativeType(typ) {
		return true
	}
	switch t := typ.(type) {
	case *syntax.BashPPStructType:
		return len(t.Fields) == 0
	case *syntax.BashPPNamedType:
		name := t.Name.Value
		for _, local := range r.bashPPLocalTypeDescriptors() {
			if local.Name == name && (len(local.Methods) > 0 || len(local.OmittedMethods) > 0) {
				return false
			}
		}
		switch name {
		case "bool", "string", "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "byte", "rune", "float32", "float64", "complex64", "complex128":
			return true
		}
		if seen[name] {
			return false
		}
		seen[name] = true
		if decl, ok := r.bashPPTypes[name]; ok {
			return r.goSourceNativeChannelElement(decl.typeExpr, seen)
		}
	}
	return false
}
func (r *Runner) goSourceChannelCapacity(expr syntax.BashPPExpr, word *syntax.Word) (int, error) {
	if expr == nil {
		if word == nil {
			return 0, nil
		}
		n, err := strconv.Atoi(r.literal(word))
		if err != nil {
			return 0, fmt.Errorf("gosource: channel capacity requires an integer")
		}
		if n < 0 {
			r.bashPPRaise("makechan: size out of range")
			return 0, errBashPPScalarInterrupted
		}
		return n, nil
	}
	value, err := r.bashPPEvalScalarExpr(expr)
	if err != nil {
		return 0, err
	}
	n, ok := constant.Int64Val(constant.ToInt(value.value))
	if !ok || n < 0 || int64(int(n)) != n {
		r.bashPPRaise("makechan: size out of range")
		return 0, errBashPPScalarInterrupted
	}
	return int(n), nil
}
func (r *Runner) goSourceMakeNativeChannel(typ *syntax.BashPPChanType, expr syntax.BashPPExpr, word *syntax.Word) (*bashPPCell, bool, error) {
	if !r.bashPPGoSource || typ == nil || !r.goSourceNativeChannelElement(typ.Element, map[string]bool{}) {
		return nil, false, nil
	}
	capacity, err := r.goSourceChannelCapacity(expr, word)
	if err != nil {
		return nil, true, err
	}
	req, err := r.bashPPEvalRequest()
	if err != nil {
		return nil, true, err
	}
	values, err := r.bashPPNativeRequest(r.bashPPTaskContext(r.ectx), req, bashPPBridgeRequest{Op: "channel-make", Selector: goSourceNativeChannelTypeText(typ), Args: []bashPPBridgeValue{{Kind: "int", Type: "int", Text: strconv.Itoa(capacity)}}})
	if err != nil {
		return nil, true, err
	}
	if len(values) != 1 {
		return nil, true, fmt.Errorf("gosource: channel make returned no value")
	}
	cell := goSourceNativeValueCell(values[0])
	cell.declType = typ
	return cell, true, nil
}
func (r *Runner) goSourceCloseChannel(expr syntax.BashPPExpr) {
	channel, ok := r.goSourceChannelOperand(expr, nil, "close")
	if !ok {
		return
	}
	if channel.native != nil {
		req, err := r.bashPPEvalRequest()
		if err == nil {
			_, err = r.bashPPNativeRequest(r.bashPPTaskContext(r.ectx), req, bashPPBridgeRequest{Op: "channel-close", Receiver: channel.native})
		}
		if err != nil {
			message := strings.TrimPrefix(err.Error(), "native dependency panic: ")
			if message == "close of nil channel" || message == "close of closed channel" {
				r.bashPPRaise(message)
				return
			}
			r.goSourceNativeChannelError(err)
		}
		return
	}
	if channel.ch == nil {
		r.bashPPRaise("close of nil channel")
		return
	}
	if !channel.close() {
		r.bashPPRaise("close of closed channel")
	}
}

// The range expression was evaluated once by the native range dispatcher.
func (r *Runner) goSourceRangeNativeChannel(ctx context.Context, rng *syntax.BashPPRange, value *bashPPBridgeValue) {
	if len(rng.Names) > 1 {
		r.bashPPRangeError(rng, "channel range permits at most one iteration variable")
		return
	}
	for {
		cell, open := r.goSourceNativeReceive(ctx, &bashPPChannel{native: value}, nil)
		if cell == nil || !open {
			return
		}
		leave := r.bashPPPushScope()
		if len(rng.Names) == 1 {
			r.bashPPBindReceivedCell(rng.Names[0].Value, cell)
		}
		r.cmd(r.bashPPTaskContext(ctx), rng.Body)
		leave()
		if r.exit.exiting || r.exit.returning || r.exit.fatalExit || r.loopControlPending() {
			return
		}
		switch r.bashPPBranch {
		case bashPPBranchBreak:
			r.bashPPBranch = bashPPBranchNone
			r.exit.clear()
			return
		case bashPPBranchContinue:
			r.bashPPBranch = bashPPBranchNone
			r.exit.clear()
		}
	}
}

func goSourceNativeChannelTypeText(typ *syntax.BashPPChanType) string {
	prefix := "chan "
	if typ.Direction == "recv" {
		prefix = "<-chan "
	}
	if typ.Direction == "send" {
		prefix = "chan<- "
	}
	return prefix + bashPPBridgeTypeText(typ.Element)
}
func (r *Runner) goSourceReceiveAssign(assign *syntax.BashPPAssign) bool {
	if !r.bashPPGoSource || len(assign.Names) != 2 || len(assign.ValueExprs) != 1 {
		return false
	}
	receive, ok := assign.ValueExprs[0].(*syntax.BashPPUnaryExpr)
	if !ok || receive.Op == nil || receive.Op.Value != "<-" {
		return false
	}
	for _, name := range assign.Names {
		if name.Value == "_" {
			continue
		}
		cell := r.bashPPScope.lookup(name.Value)
		if cell == nil || cell.constant || cell.vr.ReadOnly {
			r.exit.fatal(fmt.Errorf("gosource: receive assignment target %s is not mutable", name.Value))
			return true
		}
	}
	cell, open := r.bashPPReceiveCell(r.ectx, &syntax.BashPPReceive{Arrow: receive.Pos(), ChanExpr: receive.X}, nil)
	if cell == nil {
		return true
	}
	r.bashPPCommitTupleAssign(assign, []*bashPPCell{cell, goSourceNativeValueCell(bashPPBridgeValue{Kind: "bool", Type: "bool", Text: strconv.FormatBool(open)})})
	return true
}

// time.Sleep is a known blocking imported operation. Its callee and argument
// are already captured; release the Go task launch handshake before waiting.
func (r *Runner) goSourceNativeSleepBoundary(ctx context.Context, req bashPPEvalRequest, q bashPPBridgeRequest) bool {
	if !r.bashPPGoSource || !r.bashPPGoTask || q.Op != "call" || len(q.Args) != 1 {
		return true
	}
	sleep := false
	alias, name, ok := strings.Cut(q.Selector, ".")
	if ok && req.Imports[alias] == "time" && name == "Sleep" {
		sleep = true
	}
	if q.Receiver != nil && q.Receiver.Callable == "time.Sleep" {
		sleep = true
	}
	if !sleep {
		return true
	}
	n, err := strconv.ParseInt(q.Args[0].Text, 10, 64)
	if err != nil || n <= 0 {
		return true
	}
	return r.bashPPArmBeforeBlock(ctx)
}
