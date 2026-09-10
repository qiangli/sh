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
	iteratorYield, _ := r.goSourceIteratorYield(fn)
	for group, fields := range [][]*syntax.BashPPField{fn.params(), fn.results()} {
		for _, field := range fields {
			if field.Variadic() {
				return bashPPBridgeValue{}, fmt.Errorf("gosource: variadic original callbacks are unsupported")
			}
			// Only reviewed synchronous consumers accept these aggregate results.
			// Callback arguments remain scalar: no copied reference aliasing.
			if group == 1 && r.bashPPTourCallbackResult(field.FieldTypeExpr) {
				continue
			}
			if group == 0 && iteratorYield != nil {
				if nested, ok := field.FieldTypeExpr.(*syntax.BashPPFuncType); ok &&
					bashPPFieldsSignature(nested.Params) == bashPPFieldsSignature(iteratorYield.Params) &&
					bashPPFieldsSignature(nested.Results) == bashPPFieldsSignature(iteratorYield.Results) {
					continue
				}
			}
			// A value-semantics aggregate — a struct or array built only from
			// scalars — is copied by Go itself at the call boundary, so the
			// copied transport observes what native Go observes. A parameter
			// naming an imported type is not copied at all: it stays the
			// dependency's own value behind a session handle.
			if r.bashPPCallbackValueType(field.FieldTypeExpr) || r.bashPPCallbackNativeType(field.FieldTypeExpr) {
				continue
			}
			if group == 0 && r.bashPPNativeType(field.FieldTypeExpr) {
				continue
			}
			// Interface parameters and results can carry nil, native handles, or
			// interpreter-owned dynamic values through the existing interface
			// side channel without flattening them to strings.
			if _, ok := r.bashPPInterfaceType(field.FieldTypeExpr); ok {
				continue
			}
			return bashPPBridgeValue{}, fmt.Errorf("gosource: original callback signature requires value-semantics parameters and supported results")
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
		if signature, ok := params[i].typ.(*syntax.BashPPFuncType); ok {
			if arg.Kind != "handle" || (!strings.HasPrefix(arg.Type, "func(") && !strings.HasPrefix(arg.NativeType, "func(")) {
				return nil, fmt.Errorf("gosource: callback parameter %d requires a native function", i)
			}
			native := arg
			callback := &bashPPFunc{native: &native, lit: &syntax.BashPPFuncLit{Params: signature.Params, Results: signature.Results}}
			vr := r.bashPPStoreFunc(callback)
			texts[i] = vr.Str
			cells[i] = &bashPPCell{vr: vr, declType: signature}
			continue
		}
		cell, text, err := r.goSourceCallbackCell(arg, params[i])
		if err != nil {
			if arg.Kind != "handle" && arg.Kind != "nil" {
				return nil, fmt.Errorf("gosource: callback parameter %d: %w", i, err)
			}
			cells[i] = goSourceNativeValueCell(arg)
			cells[i].declType = params[i].typ
			continue
		}
		cells[i], texts[i] = cell, text
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
		if q.Receiver.Callable == "range-iterator" {
			return true
		}
		path = strings.TrimPrefix(q.Receiver.Callable, "*")
		if path == "sync.Once.Do" {
			return true
		}
		if q.Selector == "Do" && (q.Receiver.NativeType == "sync.Once" || q.Receiver.NativeType == "*sync.Once" || (q.Receiver.NativeType == "" && q.Receiver.Type == "sync.Once")) {
			return true
		}
		// T.Run does not return until a non-parallel subtest callback completes.
		// This is the path used by the reviewed Go-by-Example table test.
		if q.Selector == "Run" && (q.Receiver.NativeType == "testing.T" || q.Receiver.NativeType == "*testing.T") {
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
	if path == "golang.org/x/tour/wc.Test" || path == "golang.org/x/tour/pic.Show" || path == "path/filepath.WalkDir" || path == "testing.Main" {
		return true
	}
	if pkg, name, ok := strings.Cut(path, "."); ok {
		if pkg == "slices" {
			switch name {
			case "AppendSeq", "Collect", "Sorted", "SortedFunc", "SortedStableFunc":
				return true
			}
		}
		if pkg == "maps" && (name == "Collect" || name == "Insert") {
			return true
		}
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

// retainedFunctionCallback reports a registration API that keeps an original
// function past the call that hands it over and invokes it later, possibly from
// the dependency's own goroutines. Admitting one switches the whole session to
// callback-capable dispatch: every later request serves callbacks on its own
// parked goroutine, so a retained handler runs on the Runner that owns it and
// never concurrently with another interpreted frame.
func retainedFunctionCallback(req bashPPEvalRequest, q bashPPBridgeRequest) bool {
	path := ""
	if q.Receiver != nil && q.Receiver.Kind == "handle" {
		path = strings.TrimPrefix(q.Receiver.Callable, "*")
		if path == "" && q.Receiver.NativeType != "" {
			path = strings.TrimPrefix(q.Receiver.NativeType, "*") + "." + q.Selector
		}
	}
	if path == "" {
		alias, name, ok := strings.Cut(q.Selector, ".")
		if !ok {
			return false
		}
		path = req.Imports[alias] + "." + name
	}
	switch path {
	case "net/http.HandleFunc", "net/http.Handle",
		"net/http.ServeMux.HandleFunc", "net/http.ServeMux.Handle":
		return true
	}
	return false
}

// These result shapes are consumed without mutation or retention by the reviewed
// Tour helpers. Arbitrary aggregate parameters/results require shared-reference
// transport and remain unsupported.
func (r *Runner) bashPPTourCallbackResult(typ syntax.BashPPTypeExpr) bool {
	shape, ok := r.bashPPUnderlyingType(typ).(*syntax.BashPPCollectionType)
	if !ok {
		return false
	}
	if shape.Kind == "map" {
		return bashPPTypeText(r.bashPPUnderlyingType(shape.Key)) == "string" && bashPPTypeText(r.bashPPUnderlyingType(shape.Element)) == "int"
	}
	if shape.Kind != "slice" {
		return false
	}
	inner, ok := r.bashPPUnderlyingType(shape.Element).(*syntax.BashPPCollectionType)
	if !ok || inner.Kind != "slice" {
		return false
	}
	element := bashPPTypeText(r.bashPPUnderlyingType(inner.Element))
	return element == "uint8" || element == "byte"
}
