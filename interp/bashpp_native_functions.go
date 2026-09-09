package interp

import (
	"context"
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Function handles name interpreted closures in one dependency session. Only
// the typed trampoline is native; the body and captured cells stay here.
func (r *Runner) bashPPBridgeFunction(fn *bashPPFunc) (bashPPBridgeValue, error) {
	if fn.native != nil {
		return *fn.native, nil
	}
	for _, fields := range [][]*syntax.BashPPField{fn.params(), fn.results()} {
		for _, field := range fields {
			if field.Variadic() {
				return bashPPBridgeValue{}, fmt.Errorf("gosource: variadic original callbacks are unsupported")
			}
			typ := r.bashPPUnderlyingType(field.FieldTypeExpr)
			n, ok := typ.(*syntax.BashPPNamedType)
			if !ok || n.Name == nil || !(bashPPIntegerType(n.Name.Value) || n.Name.Value == "bool" || n.Name.Value == "string" || n.Name.Value == "float32" || n.Name.Value == "float64") {
				return bashPPBridgeValue{}, fmt.Errorf("gosource: original callback signature requires supported scalar types")
			}
		}
	}
	req, err := r.bashPPEvalRequest()
	if err != nil {
		return bashPPBridgeValue{}, err
	}
	s := req.Bridge
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.functions == nil {
		s.functions = map[uint64]*bashPPFunc{}
	}
	for id, existing := range s.functions {
		if existing == fn {
			return bashPPBridgeValue{Kind: "callback", Handle: id, Session: s.id, Callbacks: true}, nil
		}
	}
	s.functionNext++
	id := s.functionNext
	s.functions[id] = fn
	return bashPPBridgeValue{Kind: "callback", Handle: id, Session: s.id, Callbacks: true}, nil
}

func (r *Runner) bashPPNativeFunctionCallback(ctx context.Context, id uint64, args []bashPPBridgeValue) (values []bashPPBridgeValue, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			values = nil
			err = fmt.Errorf("gosource: original function callback interpreter failure: %v", failure)
		}
	}()
	s := r.bashPPTools.bridge
	s.mu.Lock()
	fn := s.functions[id]
	s.mu.Unlock()
	if fn == nil {
		return nil, fmt.Errorf("gosource: original callback handle expired")
	}
	params := bashppParams(fn.params())
	if len(args) != len(params) {
		return nil, fmt.Errorf("gosource: original callback argument count mismatch")
	}
	savedResults, savedCalls := r.bashPPResultCells, r.bashPPCallCells
	savedChannels, savedInterfaces := r.bashPPCallChannels, r.bashPPCallInterfaces
	savedExit, savedPanic, savedCtx := r.exit, r.bashPPPanic, r.ectx
	failure := r.bashPPShortFailureSeq
	defer func() {
		r.bashPPResultCells, r.bashPPCallCells = savedResults, savedCalls
		r.bashPPCallChannels, r.bashPPCallInterfaces = savedChannels, savedInterfaces
		r.ectx = savedCtx
	}()
	r.ectx = ctx
	r.bashPPCallChannels = nil
	r.bashPPCallInterfaces = nil
	texts := make([]string, len(args))
	cells := make([]*bashPPCell, len(args))
	for i, arg := range args {
		scalar, err := arg.scalar()
		if err != nil {
			return nil, fmt.Errorf("gosource: callback parameter %d: %w", i, err)
		}
		texts[i] = bashPPScalarString(scalar.value)
		cells[i] = goSourceNativeValueCell(arg)
		cells[i].typeName, cells[i].declType = params[i].declared, params[i].typ
	}
	r.bashPPCallCells = cells
	results := r.bashPPInvoke(ctx, fn, texts)
	if r.bashPPPanicking() && !r.exit.exiting {
		payload := r.bashPPPanic.value()
		r.bashPPPanic, r.exit = savedPanic, savedExit
		return []bashPPBridgeValue{{Kind: "panic", Text: payload, Type: "string"}}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.exit.err != nil {
		return nil, r.exit.err
	}
	if r.exit.exiting || r.exit.fatalExit || r.exit.code != 0 || r.bashPPShortFailureSeq != failure {
		return nil, fmt.Errorf("gosource: original function callback failed (status %d)", r.exit.code)
	}
	if len(results) != bashppResultCount(fn.results()) || len(results) != len(r.bashPPResultCells) {
		return nil, fmt.Errorf("gosource: original callback result count mismatch")
	}
	for _, cell := range r.bashPPResultCells {
		v, err := r.bashPPBridgeCell(cell)
		if err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, nil
}

func synchronousFunctionCallback(req bashPPEvalRequest, q bashPPBridgeRequest) bool {
	path := ""
	if q.Receiver != nil && q.Receiver.Kind == "handle" {
		path = strings.TrimPrefix(q.Receiver.Callable, "*")
		if path == "sync.Once.Do" {
			return true
		}
		if q.Selector == "Do" && (q.Receiver.NativeType == "sync.Once" || q.Receiver.NativeType == "*sync.Once" || (q.Receiver.NativeType == "" && q.Receiver.Type == "sync.Once")) {
			return true
		}
	}
	if path == "" {
		alias, name, ok := strings.Cut(q.Selector, ".")
		if !ok {
			return false
		}
		path = req.Imports[alias] + "." + name
	}
	pkg, name, ok := strings.Cut(path, ".")
	if !ok {
		return false
	}
	if pkg == "strings" || pkg == "bytes" {
		switch name {
		case "Map", "FieldsFunc", "ContainsFunc", "IndexFunc", "LastIndexFunc", "TrimFunc", "TrimLeftFunc", "TrimRightFunc":
			return true
		}
	}
	return false
}
