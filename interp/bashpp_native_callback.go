package interp

// Sprint: #118; Story: #54; Story-ID: c3a60493cde9
//
// The interpreter side of a dependency callback. A materialised local type's
// reviewed method is a generated stub in the helper whose whole body
// asks for this: the original body is never compiled, it is executed here by
// the interpreter that owns it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// enterCallbacks serializes callback-capable outer requests. Nested imports from
// the active callback may reenter; an unrelated Runner never borrows its state.
func (s *bashPPNativeSession) enterCallbacks(ctx context.Context, req bashPPEvalRequest, q bashPPBridgeRequest) (func(), chan bashPPBridgeResponse, error) {
	if !requestCallbackCapable(req, q) {
		return func() {}, nil, nil
	}
	s.mu.Lock()
	if s.callbackGate == nil {
		s.callbackGate = make(chan struct{}, 1)
		s.callbackGate <- struct{}{}
	}
	gate := s.callbackGate
	nested := req.CallbackDepth > 0 && req.CallbackOwner == s.callbackOwner
	foreign := req.CallbackDepth > 0 && req.CallbackOwner != s.callbackOwner
	s.mu.Unlock()
	if foreign {
		return nil, nil, fmt.Errorf("gosource: concurrent task cannot reenter an active original method callback")
	}
	if !nested {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	s.mu.Lock()
	previous, owner := s.activeCallbacks, s.callbackOwner
	callbacks := make(chan bashPPBridgeResponse, 1)
	s.activeCallbacks, s.callbackOwner = callbacks, req.CallbackOwner
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		s.activeCallbacks, s.callbackOwner = previous, owner
		s.mu.Unlock()
		if !nested {
			gate <- struct{}{}
		}
	}, callbacks, nil
}

// serveCallback executes synchronously in the parked request's goroutine. The
// socket reader stays free, and nested imports can service their own callbacks.
func (s *bashPPNativeSession) serveCallback(ctx context.Context, owner *Runner, q bashPPBridgeResponse) {
	answer := bashPPBridgeRequest{ID: q.ID, Op: "callback-reply"}
	if owner == nil || q.Receiver == nil {
		answer.Error = "gosource: callback has no original owner or receiver"
	} else {
		s.bashPPAuthenticateCallbackValue(q.Receiver)
		owner.bashPPTools.callbackDepth++
		var values []bashPPBridgeValue
		var err error
		if q.Receiver.Kind == "callback" {
			values, err = owner.bashPPNativeFunctionCallback(ctx, q.Receiver.Handle, q.Receiver.Elements)
		} else {
			values, err = owner.bashPPNativeCallback(ctx, q.Selector, *q.Receiver)
		}
		owner.bashPPTools.callbackDepth--
		if err != nil {
			// Interruption is an internal unwind token after the shared program
			// outcome is already known. Never serialize its text as an ordinary
			// callback failure and destroy that identity across the bridge.
			if !errors.Is(err, errBashPPScalarInterrupted) {
				answer.Error = err.Error()
			}
			if answer.Error != "" && !owner.exit.exiting {
				owner.exit.fatal(err)
			}
		} else {
			answer.Values = values
		}
		if q.Receiver.Origin != 0 {
			s.mu.Lock()
			ptr := s.origins[q.Receiver.Origin]
			s.mu.Unlock()
			if ptr != nil {
				v, err := owner.bashPPBridgePointerValue(ptr)
				if err != nil {
					answer.Error = err.Error()
				} else {
					answer.Receiver = &v
				}
			}
		}
	}
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn != nil {
		s.write.Lock()
		_ = json.NewEncoder(conn).Encode(answer)
		s.write.Unlock()
	}
}

// bashPPLocalTypeName maps a dependency-reported type identity back to the
// original program's own type name. The helper is package main, exactly as the
// original program is, so a materialised local type reports itself as
// "main.Vertex" — the same identity Go's %T prints.
func bashPPLocalTypeName(identity string) string {
	return strings.TrimPrefix(identity, "main.")
}

// bashPPNativeCallback executes one original method body for the dependency.
// Reviewed signatures include String/Error, synchronous Read, and image.Image.
// Arguments and results preserve their declared types across the callback.
func (r *Runner) bashPPNativeCallback(ctx context.Context, selector string, recv bashPPBridgeValue) (values []bashPPBridgeValue, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			values = nil
			err = fmt.Errorf("gosource: original callback interpreter failure: %v", failure)
		}
	}()
	typeName, method, ok := strings.Cut(selector, ".")
	if !ok {
		return nil, fmt.Errorf("gosource: malformed original method selector %q", selector)
	}
	typeName = bashPPLocalTypeName(typeName)
	var copied *bashPPCell
	if r.bashPPMethods[typeName][method] == nil {
		return nil, fmt.Errorf("gosource: original type %s has no method %s", typeName, method)
	}
	named := &syntax.BashPPNamedType{Name: &syntax.Lit{Value: typeName}}
	var cell *bashPPCell
	if recv.Origin != 0 {
		session := r.bashPPTools.bridge
		session.mu.Lock()
		ptr := session.origins[recv.Origin]
		session.mu.Unlock()
		if ptr == nil {
			return nil, fmt.Errorf("gosource: callback receiver identity expired")
		}
		cell = bashPPPointerCell(ptr)
	} else {
		if r.bashPPMethods[typeName][method].decl.Receiver.Pointer {
			return nil, fmt.Errorf("gosource: pointer callback requires original receiver identity")
		}
		value, meta, err := r.bashPPBridgeContents(recv, named)
		if err != nil {
			return nil, fmt.Errorf("gosource: %s receiver: %w", selector, err)
		}
		cell = &bashPPCell{declType: named, typeName: typeName}
		bashPPStoreCellValue(cell, value, meta)
		// The receiver is a copy of storage the caller still shares. A write
		// through one of its references would be invisible to the caller here,
		// so it is detected after the body rather than dropped.
		copied = cell
	}
	before := r.goSourceCopiedReceiverDigest(copied)

	// The callback borrows the runner while its caller is parked inside the
	// imported call, so the caller's in-flight argument state is restored
	// rather than consumed by this nested invocation.
	savedResults, savedChannels := r.bashPPResultCells, r.bashPPCallChannels
	savedInterfaces, savedCells := r.bashPPCallInterfaces, r.bashPPCallCells
	savedExit, savedPanic, failure := r.exit, r.bashPPPanic, r.bashPPShortFailureSeq
	savedCtx := r.ectx
	r.ectx = ctx
	defer func() {
		r.bashPPResultCells, r.bashPPCallChannels = savedResults, savedChannels
		r.bashPPCallInterfaces, r.bashPPCallCells = savedInterfaces, savedCells
		r.ectx = savedCtx
	}()
	r.bashPPCallChannels, r.bashPPCallInterfaces, r.bashPPCallCells = nil, nil, nil

	// A pointer receiver binds to the original interpreter storage.
	bound, ok := r.bashPPBindMethod(cell, method, true)
	if !ok {
		return nil, fmt.Errorf("gosource: cannot bind original method %s", selector)
	}
	var arguments []string
	var readerBuffer []any
	if method == "Read" {
		if (len(recv.CallArgs) != 1 && len(recv.CallArgs) != 2) || recv.CallArgs[0].Kind != "handle" || (recv.CallArgs[0].Type != "[]uint8" && recv.CallArgs[0].Type != "[]byte") {
			return nil, fmt.Errorf("gosource: original Read requires an authenticated native byte buffer")
		}
		argument := goSourceNativeValueCell(recv.CallArgs[0])
		argument.declType = bound.params()[0].FieldTypeExpr
		argument.typeName = bashPPTypeText(argument.declType)
		if len(recv.CallArgs) == 2 && r.bashPPReaderLocalBufferAllowed(bound) {
			snapshot := recv.CallArgs[1]
			if snapshot.Kind != "reader-buffer" || snapshot.ReaderLength < 0 || snapshot.ReaderLength > len(snapshot.ReaderBuffer) {
				return nil, fmt.Errorf("gosource: invalid Read buffer snapshot")
			}
			readerBuffer = make([]any, len(snapshot.ReaderBuffer))
			for i, b := range snapshot.ReaderBuffer {
				readerBuffer[i] = int(b)
			}
			bashPPStoreCellValue(argument, readerBuffer[:snapshot.ReaderLength], &bashPPCollectionMeta{kind: "slice", typ: argument.declType, sequence: make([]*bashPPCollectionMeta, snapshot.ReaderLength, len(readerBuffer))})
		}

		r.bashPPCallCells = []*bashPPCell{argument}
		arguments = []string{""}
	} else {
		// A generalised mirrored signature — image.Image's At(x, y int) — sends
		// its arguments with the call. Each binds at the original parameter's
		// own declared type, so the body sees exactly what it declared.
		params := bashppParams(bound.params())
		if len(recv.CallArgs) != len(params) {
			return nil, fmt.Errorf("gosource: original %s got %d argument(s), want %d", selector, len(recv.CallArgs), len(params))
		}
		cells := make([]*bashPPCell, len(params))
		for i, arg := range recv.CallArgs {
			cell := goSourceNativeValueCell(arg)
			cell.declType = params[i].typ
			cell.typeName = bashPPTypeText(params[i].typ)
			cells[i] = cell
			text := ""
			if scalar, err := arg.scalar(); err == nil {
				text = bashPPScalarString(scalar.value)
			}
			arguments = append(arguments, text)
		}
		r.bashPPCallCells = cells
	}
	results := r.bashPPInvoke(ctx, bound, arguments)
	if r.bashPPPanicking() && !r.exit.exiting {
		payload := r.bashPPPanic.value()
		r.bashPPPanic, r.exit = savedPanic, savedExit
		return []bashPPBridgeValue{{Kind: "panic", Type: "string", Text: payload}}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.exit.err != nil {
		return nil, r.exit.err
	}
	if r.exit.exiting || r.exit.fatalExit || r.exit.code != 0 || r.bashPPShortFailureSeq != failure {
		return nil, fmt.Errorf("gosource: original %s failed (status %d)", selector, r.exit.code)
	}
	if copied != nil && r.goSourceCopiedReceiverDigest(copied) != before {
		return nil, fmt.Errorf("gosource: original %s wrote through reference storage of a copied receiver; that effect cannot cross back", selector)
	}
	// Error and String answer with the one string the original body returns.
	// Every other mirrored signature — Read, and the image.Image method set —
	// crosses back as typed values, so a native handle stays an authenticated
	// handle rather than being flattened into text.
	if want := bashppParams(bound.results()); !bashPPStringResult(want) {
		if len(results) != len(want) || len(r.bashPPResultCells) != len(want) {
			return nil, fmt.Errorf("gosource: original %s returned %d values, want %d", selector, len(results), len(want))
		}
		for _, cell := range r.bashPPResultCells {
			value, err := r.bashPPBridgeCell(cell)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		if readerBuffer != nil {
			data := make([]byte, len(readerBuffer))
			for i, item := range readerBuffer {
				n, ok := item.(int)
				if !ok || n < 0 || n > 255 {
					return nil, fmt.Errorf("gosource: invalid Read byte writeback")
				}
				data[i] = byte(n)
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			values = append(values, bashPPBridgeValue{Kind: "reader-buffer", ReaderBuffer: data})
		}
		return values, nil
	}
	if len(results) != 1 {
		return nil, fmt.Errorf("gosource: original %s returned %d values, want 1", selector, len(results))
	}
	return []bashPPBridgeValue{{Kind: "string", Type: "string", Text: results[0]}}, nil
}

// bashPPStringResult reports the fixed String/Error protocol shape: exactly one
// result, of type string. Those mirrors answer with text; everything else the
// helper mirrors answers with typed values.
func bashPPStringResult(results []bashPPParam) bool {
	return len(results) == 1 && bashPPTypeText(results[0].typ) == "string"
}

// bashPPBridgeContents rebuilds an interpreter value at its original declared
// type. Native fields retain their authenticated session handles; their private
// SDK storage is never reflected into an interpreter-owned imitation.
func (r *Runner) bashPPBridgeContents(v bashPPBridgeValue, typ syntax.BashPPTypeExpr) (any, *bashPPCollectionMeta, error) {
	if _, ok := r.bashPPInterfaceType(typ); ok {
		if v.Kind == "nil" && (v.Type == "" || v.Type == v.Interface) {
			return "", &bashPPCollectionMeta{kind: "interface", typ: typ, interfaceValue: &bashPPInterfaceValue{nilIface: true}}, nil
		}
		dynamic := bashPPBridgeDynamicType(v.Type)
		if _, ok := r.bashPPInterfaceType(dynamic); ok {
			return nil, nil, fmt.Errorf("dynamic type %s of an interface value is not materialised", v.Type)
		}
		if v.Kind == "nil" {
			if _, _, err := r.goSourceNativeAssignedValue(v, typ); err != nil {
				return nil, nil, err
			}
			// The native receiver contains a nonnil interface whose dynamic
			// value is typed nil. Keep that type; no handle needs rebuilding.
			payload := goSourceNativeValueCell(v)
			payload.declType = dynamic
			iv := &bashPPInterfaceValue{cell: payload, dynamic: dynamic}
			return payload.vr.Obj, &bashPPCollectionMeta{kind: "interface", typ: typ, interfaceValue: iv}, nil
		}
		inner, innerMeta, err := r.bashPPBridgeContents(v, dynamic)
		if err != nil {
			return nil, nil, err
		}
		payload := &bashPPCell{declType: dynamic}
		if named, ok := dynamic.(*syntax.BashPPNamedType); ok && named.Name != nil {
			payload.typeName = named.Name.Value
		}
		bashPPStoreCellValue(payload, inner, innerMeta)
		iv := &bashPPInterfaceValue{cell: payload, dynamic: dynamic}
		return inner, &bashPPCollectionMeta{kind: "interface", typ: typ, interfaceValue: iv}, nil
	}
	if v.Kind == "handle" {
		if r.bashPPTools.bridge == nil || v.Session != r.bashPPTools.bridge.id {
			return nil, nil, fmt.Errorf("gosource: native callback field belongs to another dependency session")
		}
		copy := v
		return &copy, &bashPPCollectionMeta{kind: "native", typ: typ}, nil
	}
	if pointer, ok := r.bashPPPointerType(typ); ok {
		if v.Kind == "nil" {
			return nil, bashPPPointerMeta(typ), nil
		}
		// The dependency sends the pointee by shape, so the original body
		// reads the value it would have read through the pointer. The
		// dependency keeps ownership of the address itself.
		return r.bashPPBridgeContents(v, pointer.Element)
	}
	if v.Kind == "nil" {
		value, meta := r.bashPPZeroValue(typ)
		return value, meta, nil
	}
	switch shape := r.bashPPUnderlyingType(typ).(type) {
	case *syntax.BashPPStructType:
		if v.Kind != "struct" {
			return nil, nil, fmt.Errorf("%s is a struct but the dependency sent %s", bashPPTypeText(typ), v.Kind)
		}
		out := map[string]any{}
		meta := &bashPPCollectionMeta{kind: "struct", typ: typ, mapping: map[string]*bashPPCollectionMeta{}}
		for _, field := range bashPPFlatFields(shape.Fields) {
			item, ok := v.Fields[field.name]
			if !ok {
				return nil, nil, fmt.Errorf("field %s did not cross the dependency boundary", field.name)
			}
			value, child, err := r.bashPPBridgeContents(item, field.typ)
			if err != nil {
				return nil, nil, err
			}
			out[field.name], meta.mapping[field.name] = value, child
		}
		return out, meta, nil
	case *syntax.BashPPCollectionType:
		if shape.Kind == "map" {
			if v.Kind != "map" {
				return nil, nil, fmt.Errorf("%s is a map but the dependency sent %s", bashPPTypeText(typ), v.Kind)
			}
			out := map[string]any{}
			meta := &bashPPCollectionMeta{kind: "map", typ: typ, mapping: map[string]*bashPPCollectionMeta{}}
			for _, item := range v.Entries {
				value, child, err := r.bashPPBridgeContents(item.Value, shape.Element)
				if err != nil {
					return nil, nil, err
				}
				out[item.Key.Text], meta.mapping[item.Key.Text] = value, child
			}
			return out, meta, nil
		}
		if v.Kind != "slice" && v.Kind != "array" {
			return nil, nil, fmt.Errorf("%s is a sequence but the dependency sent %s", bashPPTypeText(typ), v.Kind)
		}
		out := make([]any, 0, len(v.Elements))
		meta := &bashPPCollectionMeta{kind: shape.Kind, typ: typ}
		for _, item := range v.Elements {
			value, child, err := r.bashPPBridgeContents(item, shape.Element)
			if err != nil {
				return nil, nil, err
			}
			out = append(out, value)
			meta.sequence = append(meta.sequence, child)
		}
		return out, meta, nil
	}
	return bashPPBridgeScalarValue(v)
}

func bashPPBridgeDynamicType(name string) syntax.BashPPTypeExpr {
	name = strings.TrimSpace(name)
	switch name {
	case "interface {}", "interface{}":
		return &syntax.BashPPNamedType{Name: &syntax.Lit{Value: "any"}}
	}
	if strings.HasPrefix(name, "[]") {
		return &syntax.BashPPCollectionType{
			Kind:    "slice",
			Element: bashPPBridgeDynamicType(name[2:]),
		}
	}
	if strings.HasPrefix(name, "map[") {
		end := strings.IndexByte(name, ']')
		if end > len("map[") {
			return &syntax.BashPPCollectionType{
				Kind:    "map",
				Key:     bashPPBridgeDynamicType(name[len("map["):end]),
				Element: bashPPBridgeDynamicType(name[end+1:]),
			}
		}
	}
	return &syntax.BashPPNamedType{Name: &syntax.Lit{Value: bashPPLocalTypeName(name)}}
}

// bashPPBridgeScalarValue converts one transported scalar into the interpreter
// representation the collection layer stores.
func bashPPBridgeScalarValue(v bashPPBridgeValue) (any, *bashPPCollectionMeta, error) {
	switch v.Kind {
	case "string":
		return v.Text, nil, nil
	case "bool":
		return v.Text == "true", nil, nil
	case "int", "uint":
		n, err := strconv.Atoi(v.Text)
		if err != nil {
			return nil, nil, fmt.Errorf("integer %q from the dependency: %w", v.Text, err)
		}
		return n, nil, nil
	case "float":
		n, err := strconv.ParseFloat(v.Text, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("float %q from the dependency: %w", v.Text, err)
		}
		return n, nil, nil
	}
	return nil, nil, fmt.Errorf("%s (%s) has no interpreter representation", v.Kind, v.Type)
}

// Only values arriving on this session's authenticated callback connection get
// its identity. Stored handles keep that identity through nested field reads,
// and the normal request validator rejects them after Reset or in another runner.
func (s *bashPPNativeSession) bashPPAuthenticateCallbackValue(v *bashPPBridgeValue) {
	if v.Kind == "handle" || v.Kind == "callback" || v.Origin != 0 {
		v.Session = s.id
	}
	for i := range v.CallArgs {
		s.bashPPAuthenticateCallbackValue(&v.CallArgs[i])
	}
	for i := range v.Elements {
		s.bashPPAuthenticateCallbackValue(&v.Elements[i])
	}
	for name, field := range v.Fields {
		s.bashPPAuthenticateCallbackValue(&field)
		v.Fields[name] = field
	}
	for i := range v.Entries {
		s.bashPPAuthenticateCallbackValue(&v.Entries[i].Key)
		s.bashPPAuthenticateCallbackValue(&v.Entries[i].Value)
	}
}
