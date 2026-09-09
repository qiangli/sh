package interp

// Sprint: #118; Story: #51; Story-ID: 825f8083451e
import (
	"errors"
	"fmt"
	"go/constant"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func (r *Runner) bashPPGoSendChannel(send *syntax.BashPPSend) (*bashPPChannel, bool) {
	return r.goSourceChannelOperand(send.ChanExpr, send.Chan, "send")
}
func (r *Runner) bashPPGoReceiveChannel(recv *syntax.BashPPReceive) (*bashPPChannel, bool) {
	return r.goSourceChannelOperand(recv.ChanExpr, recv.Chan, "receive")
}
func (r *Runner) goSourceChannelOperand(expr syntax.BashPPExpr, word *syntax.Word, operation string) (*bashPPChannel, bool) {
	if !r.bashPPGoSource || expr == nil {
		return r.bashPPChannelOperation(word, operation)
	}
	for {
		paren, ok := expr.(*syntax.BashPPParenExpr)
		if !ok {
			break
		}
		expr = paren.X
	}
	var cell *bashPPCell
	var err error
	if id, ok := expr.(*syntax.BashPPIdent); ok {
		cell = r.bashPPScope.lookup(id.Name.Value)
	} else if r.bashPPNativeExpr(expr) {
		var value bashPPBridgeValue
		value, err = r.bashPPBridgeExpr(expr)
		if err == nil {
			cell = goSourceNativeValueCell(value)
		}
	} else {
		cell, err = r.goSourceValueCell(expr)
	}
	if err != nil {
		r.bashPPGoSendError(expr, err)
		return nil, false
	}
	if cell == nil {
		r.bashPPGoSendError(expr, fmt.Errorf("Go channel operand has no value"))
		return nil, false
	}
	typ, _ := cell.declType.(*syntax.BashPPChanType)
	if typ != nil && (((operation == "send" || operation == "close") && typ.Direction == "recv") || (operation == "receive" && typ.Direction == "send")) {
		r.bashPPGoSendError(expr, fmt.Errorf("cannot %s on %s-only channel", operation, typ.Direction))
		return nil, false
	}
	if r.bashPPChanBoundary {
		r.bashPPGoSendError(expr, fmt.Errorf("channel cannot cross a shell-copy boundary"))
		return nil, false
	}
	if native, ok := r.goSourceNativeChannel(cell); ok {
		return &bashPPChannel{native: native}, true
	}
	if cell.channel != nil {
		if cell.channelOwner != r.bashPPConcurrent {
			r.bashPPGoSendError(expr, fmt.Errorf("channel belongs to another task group"))
			return nil, false
		}
		return cell.channel, true
	}
	if typ != nil && cell.vr.Kind == expand.String && cell.vr.Str == "" {
		if r.goSourceNativeChannelElement(typ.Element, map[string]bool{}) {
			value := bashPPBridgeValue{Kind: "nil", Type: goSourceNativeChannelTypeText(typ)}
			return &bashPPChannel{native: &value}, true
		}
		// nil has no ready communication or close notification.
		nilChannel := newBashPPChannel(bashPPTypeText(typ.Element), 0)
		nilChannel.ch = nil
		nilChannel.closing = nil
		nilChannel.element = typ.Element
		return nilChannel, true
	}
	r.bashPPGoSendError(expr, fmt.Errorf("Go operand is not an interpreted channel"))
	return nil, false
}
func (r *Runner) bashPPGoSendError(expr syntax.BashPPExpr, err error) {
	if errors.Is(err, errBashPPScalarInterrupted) || r.bashPPPanicking() || r.exit.exiting || r.exit.fatalExit {
		return
	}
	var positioned *goSourceError
	if errors.As(err, &positioned) {
		r.errf("%v\n", err)
	} else {
		r.errf("%s%v\n", r.bashErrPrefix(expr.Pos()), err)
	}
	r.exit = exitStatus{code: 2}
}
func (r *Runner) bashPPGoSendPayload(channel *bashPPChannel, send *syntax.BashPPSend) (any, bool) {
	if !r.bashPPGoSource || send.ValueExpr == nil {
		value := r.bashPPGoSendValue(send.Value)
		if !r.bashPPValueFits(channel.elem, value) {
			r.errf("bash++: cannot send %q as %s channel value\n", value, channel.elem)
			r.exit.code = 2
			return nil, false
		}
		return value, true
	}
	if channel.element != nil {
		expected := channel.element
		if channel.elem == "any" {
			expected = &syntax.BashPPInterfaceType{}
		}
		if _, iface := r.bashPPInterfaceType(expected); iface {
			value, meta, err := r.bashPPEvalTypedValue(send.ValueExpr, expected)
			if err != nil {
				r.bashPPGoSendError(send.ValueExpr, err)
				return nil, false
			}
			cell := &bashPPCell{declType: channel.element}
			bashPPStoreCellValue(cell, value, meta)
			return bashPPCopyAssignmentCell(cell), true
		}
	}
	cell, err := r.goSourceValueCell(send.ValueExpr)
	if err != nil {
		r.bashPPGoSendError(send.ValueExpr, err)
		return nil, false
	}
	if r.bashPPPanicking() || r.exit.exiting || r.exit.fatalExit {
		return nil, false
	}
	if channel.element != nil {
		base, _ := r.bashPPChanElemBase(channel.elem)
		if bashPPScalarType(base) || base == "complex64" || base == "complex128" {
			scalar := r.bashPPScalarFromCell(cell)
			if scalar.value == nil || scalar.value.Kind() == constant.Unknown {
				r.bashPPGoSendError(send.ValueExpr, fmt.Errorf("channel scalar value missing"))
				return nil, false
			}
			converted, err := r.bashPPConvertScalar(base, scalar)
			if err != nil {
				r.bashPPGoSendError(send.ValueExpr, err)
				return nil, false
			}
			cell = r.bashPPChanValueCell(bashPPScalarString(converted.value), channel.elem)
			cell.scalarKind = converted.value.Kind()
			cell.declType = channel.element
		} else if _, iface := r.bashPPInterfaceType(channel.element); iface {
			dynamic := cell.declType
			if dynamic == nil {
				dynamic = &syntax.BashPPNamedType{Name: &syntax.Lit{Value: bashPPDefaultScalarTypeName(cell.scalarKind)}}
			}
			cell = &bashPPCell{vr: cell.vr, declType: channel.element, interfaceValue: &bashPPInterfaceValue{dynamic: dynamic, cell: bashPPCopyAssignmentCell(cell)}}
		} else if cell.declType != nil && !r.bashPPTypeAssignable(cell.declType, channel.element) {
			r.bashPPGoSendError(send.ValueExpr, fmt.Errorf("cannot send %s as %s", bashPPTypeText(cell.declType), channel.elem))
			return nil, false
		}
	}
	return bashPPCopyAssignmentCell(cell), true
}
func (r *Runner) bashPPReceivedCell(channel *bashPPChannel, value any, open bool) *bashPPCell {
	if !r.bashPPGoSource {
		text, _ := value.(string)
		if !open {
			text = ""
		}
		_, named := r.bashPPChanElemBase(channel.elem)
		return &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String, Str: text}, typeName: named}
	}
	if !open {
		if _, channelType := channel.element.(*syntax.BashPPChanType); channelType {
			return &bashPPCell{vr: expand.Variable{Set: true, Kind: expand.String}, declType: channel.element}
		}
		if r.bashPPGoSource && channel.element != nil {
			base, _ := r.bashPPChanElemBase(channel.elem)
			if !bashPPScalarType(base) && base != "complex64" && base != "complex128" {
				expected := channel.element
				if channel.elem == "any" {
					expected = &syntax.BashPPInterfaceType{}
				}
				value, meta := r.bashPPZeroValue(expected)
				cell := &bashPPCell{declType: channel.element}
				bashPPStoreCellValue(cell, value, meta)
				return cell
			}
		}
		return r.bashPPChanValueCell(r.bashPPChanZeroText(channel.elem), channel.elem)
	}
	if cell, ok := value.(*bashPPCell); ok {
		return cell
	}
	text, _ := value.(string)
	return r.bashPPChanValueCell(text, channel.elem)
}
func (r *Runner) bashPPBindReceivedCell(name string, cell *bashPPCell) {
	if name == "_" {
		return
	}
	r.bashPPDeclareName(name, cell.vr)
	if target := r.bashPPScope.lookup(name); target != nil {
		*target = *cell
	}
}

func (r *Runner) goSourceChannelValueCell(expr syntax.BashPPExpr) (*bashPPCell, bool, error) {
	if !r.bashPPGoSource {
		return nil, false, nil
	}
	switch x := expr.(type) {
	case *syntax.BashPPIdent:
		if cell := r.bashPPScope.lookup(x.Name.Value); cell != nil {
			_, channelType := cell.declType.(*syntax.BashPPChanType)
			if cell.channel != nil || channelType {
				return bashPPCopyAssignmentCell(cell), true, nil
			}
		}
	case *syntax.BashPPParenExpr:
		return r.goSourceChannelValueCell(x.X)
	case *syntax.BashPPUnaryExpr:
		if x.Op == nil || x.Op.Value != "<-" {
			return nil, false, nil
		}
		cell, _ := r.bashPPReceiveCell(r.ectx, &syntax.BashPPReceive{Arrow: x.Pos(), ChanExpr: x.X}, nil)
		if cell == nil {
			return nil, true, errBashPPScalarInterrupted
		}
		return cell, true, nil
	}
	return nil, false, nil
}

// Go send panics unwind through the original interpreted defer stack. The
// established shell error remains unchanged for the ordinary Bash++ profile.
func (r *Runner) bashPPClosedSend() {
	if r.bashPPGoSource {
		r.bashPPRaise("send on closed channel")
		return
	}
	r.errf("bash++: send on closed channel\n")
	r.exit.code = 2
}
