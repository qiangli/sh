package interp

import (
	"fmt"
	"strings"
)

// validateLocalTransport refuses dependency mutation of interpreter-owned
// references. Formatting is read-only; its original methods use authenticated
// receiver identities and execute against the interpreter's own storage.
func validateLocalTransport(req bashPPEvalRequest, q bashPPBridgeRequest) error {
	if q.Op != "call" {
		return nil
	}
	alias, name, selected := strings.Cut(q.Selector, ".")
	nativeWriterFormat := selected && req.Imports[alias] == "fmt" && (name == "Fprint" || name == "Fprintln" || name == "Fprintf")
	if nativeWriterFormat && !bashPPDependencyOwnedWriter(q.Args) {
		return fmt.Errorf("gosource: fmt.%s requires a dependency-owned writer; original Write callbacks are unsupported", name)
	}
	local := map[string]bashPPLocalType{}
	for _, typ := range req.LocalTypes {
		local[typ.Name] = typ
	}
	functionCallbacks := false
	// identity reports that this value is reachable through an original pointer
	// the dependency can name, so a method callback on it binds to the original
	// interpreter storage rather than to a rebuilt copy. It propagates from a
	// pointer to its pointee and from a struct to its fields, which is exactly
	// the addressable chain `&x.f.g` names in Go.
	var inspect func(bashPPBridgeValue, bool) (bool, bool, error)
	inspect = func(v bashPPBridgeValue, identity bool) (bool, bool, error) {
		if v.Kind == "callback" {
			functionCallbacks = true
			return false, false, nil
		}
		if v.Kind == "handle" {
			return false, false, nil
		}
		// A typed-nil pointer of a method-bearing local type is admitted: a
		// mirrored method raised on it runs the original body with a nil
		// receiver, which is exactly how native Go invokes it.
		name := strings.TrimPrefix(v.Type, "main.")
		typ, isLocal := local[name]
		if len(typ.OmittedMethods) > 0 {
			alias, _, _ := strings.Cut(q.Selector, ".")
			for _, method := range typ.OmittedMethods {
				if req.Imports[alias] != "fmt" || method == "Format" || method == "GoString" {
					return false, false, fmt.Errorf("gosource: original method %s.%s is not supported by dependency transport", name, method)
				}
			}
		}
		reference := v.Kind == "pointer" || v.Kind == "slice" || v.Kind == "map"
		identity = identity || v.Origin != 0
		nested := identity && (v.Kind == "pointer" || v.Kind == "struct")
		nestedRef := false
		for _, child := range v.Elements {
			l, ref, err := inspect(child, nested)
			if err != nil {
				return false, false, err
			}
			isLocal = isLocal || l
			nestedRef = nestedRef || ref
		}
		for _, child := range v.Fields {
			l, ref, err := inspect(child, nested)
			if err != nil {
				return false, false, err
			}
			isLocal = isLocal || l
			nestedRef = nestedRef || ref
		}
		for _, entry := range v.Entries {
			l, ref, err := inspect(entry.Value, false)
			if err != nil {
				return false, false, err
			}
			isLocal = isLocal || l
			nestedRef = nestedRef || ref
		}
		// A value-receiver method on copied maps/slices/reference-bearing
		// structs used to be refused outright here. It is now admitted: the
		// callback runs against the rebuilt copy and bashPPNativeCallback
		// compares the shared storage before and after the body, so an effect
		// on a shared referent is reported instead of silently lost. A pointer
		// method still requires original identity, which it already has
		// wherever the value is addressable.
		if len(typ.Methods) > 0 && (reference || nestedRef) && !identity {
			for _, m := range typ.Methods {
				if m.Pointer {
					return false, false, fmt.Errorf("gosource: pointer-receiver callback on reference-bearing value %s requires original reference identity", v.Type)
				}
			}
		}
		return isLocal, reference || nestedRef, nil
	}
	unsafe := false
	for _, arg := range q.Args {
		local, ref, err := inspect(arg, false)
		if err != nil {
			return err
		}
		unsafe = unsafe || (local && ref) || arg.Kind == "pointer"
	}
	// Plain-data pointer writeback reconciles field mutations the dependency
	// performs through an origin-bearing pointer; it never accounts for the
	// dependency invoking the pointee's ORIGINAL methods. A pointer to a local
	// method-bearing type is a callback interface value (e.g. an image.Image):
	// handing it to an arbitrary dependency lets that dependency retain it and
	// raise its original callbacks later, asynchronously, which this transport
	// cannot service. Such values may only cross to a reviewed synchronous
	// consumer (png.Encode, pic.ShowImage, the reader helpers) below; the
	// writeback admission is restricted to callback-free requests so an
	// unreviewed retainer like dep.Retain(image.Image) is refused here.
	if !functionCallbacks && !requestHasCallbacks(req, q) && nativePointerWritebackAllowed(req, q) {
		return nil
	}
	// A read-only structural emitter (json/xml Marshal, base64/hex encode, the
	// fmt formatters) walks the transported value tree and allocates its own
	// output; it never retains or mutates the interpreter-owned slices nested
	// inside a local struct. An original mirror method may ride along and is
	// executed synchronously on the parked Runner, exactly as it is for the fmt
	// entries in the same set. A general function callback still is not
	// admitted here: those go through synchronousFunctionCallback below.
	if callable := nativeSliceCallable(req, q); nativeSliceReadOnly(callable) && !functionCallbacks {
		return nil
	}
	// testing.Main owns the scheduler while this request remains parked. Its
	// descriptor slices are read-only copies and every original test callback
	// returns before Main does, so neither original storage nor callback
	// lifetime escapes this request.
	if functionCallbacks && nativeSliceCallable(req, q) == "testing.Main" {
		return nil
	}
	if synchronousReaderCallback(req, q) || synchronousImageCallback(req, q) || !functionCallbacks && (synchronousUnwrapCallback(req, q) || synchronousErrorsAsType(req, q)) {
		return nil
	}
	if functionCallbacks && !synchronousFunctionCallback(req, q) && !retainedFunctionCallback(req, q) {
		return fmt.Errorf("gosource: asynchronous or retained original function callbacks are unsupported for %s", q.Selector)
	}
	if !unsafe && (synchronousFunctionCallback(req, q) || retainedFunctionCallback(req, q)) {
		return nil
	}
	if !unsafe && !requestHasCallbacks(req, q) {
		return nil
	}
	if q.Receiver != nil && q.Receiver.Callbacks && (q.Selector == "Error" || q.Selector == "String") && len(q.Args) == 0 {
		return nil
	}
	if nativeWriterFormat {
		return nil
	}
	alias, name, ok := strings.Cut(q.Selector, ".")
	if ok && req.Imports[alias] == "fmt" {
		switch name {
		case "Print", "Println", "Printf", "Sprint", "Sprintln", "Sprintf", "Errorf":
			return nil
		}
	}
	return fmt.Errorf("gosource: dependency mutation of interpreter-owned references is unsupported for %s", q.Selector)
}

// bashPPReflectTypeOnly rewrites original function arguments of reflect.TypeOf
// into bare type descriptors. TypeOf inspects the argument's type and never
// invokes the value, so no callback trampoline is wired and the value is not
// marked callback-bearing; the worker resolves the rendered signature against
// its registered type table and hands reflect a zero value of that type.
func bashPPReflectTypeOnly(req bashPPEvalRequest, q *bashPPBridgeRequest) {
	if q.Op != "call" || q.Receiver != nil {
		return
	}
	alias, name, ok := strings.Cut(q.Selector, ".")
	if !ok || req.Imports[alias] != "reflect" || name != "TypeOf" {
		return
	}
	s := req.Bridge
	if s == nil {
		return
	}
	for i, arg := range q.Args {
		if arg.Kind != "callback" || arg.Session != s.id {
			continue
		}
		s.mu.Lock()
		fn := s.functions[arg.Handle]
		s.mu.Unlock()
		if fn == nil {
			continue
		}
		if text, ok := bashPPFunctionTypeText(fn); ok {
			q.Args[i] = bashPPBridgeValue{Kind: "nil", Type: text}
		}
	}
}

// bashPPTypeDescriptorResult reports a call whose results are pure type
// descriptors. reflect.TypeOf retains neither its argument nor the argument's
// mirrored callbacks, so its result handle must not be marked callback-bearing
// — later method reads on the descriptor are plain dependency operations.
func bashPPTypeDescriptorResult(req bashPPEvalRequest, q bashPPBridgeRequest) bool {
	if q.Op != "call" || q.Receiver != nil {
		return false
	}
	alias, name, ok := strings.Cut(q.Selector, ".")
	return ok && req.Imports[alias] == "reflect" && name == "TypeOf"
}

// bashPPDependencyOwnedWriter reports a writer argument the dependency itself
// stores: a native handle, or an original pointer whose pointee is one. The
// worker binds such a pointer to the handle's own storage, so formatted writes
// land in the value later reads observe. Anything else — an original type's
// own Write method, a rebuilt copy — stays refused.
func bashPPDependencyOwnedWriter(args []bashPPBridgeValue) bool {
	if len(args) == 0 {
		return false
	}
	w := args[0]
	if w.Kind == "handle" {
		return true
	}
	return w.Kind == "pointer" && len(w.Elements) == 1 && w.Elements[0].Kind == "handle"
}

// nativePointerWritebackAllowed reports a call whose pointer arguments all
// carry original identity. Every write the dependency performs through such a
// pointer — during this call, or during a later one if the dependency retained
// it — is compared against the value it was given and transported back into
// the original storage, so no native write is silently dropped. A pointer
// without identity is still refused: there would be nothing to write back to.
func nativePointerWritebackAllowed(req bashPPEvalRequest, q bashPPBridgeRequest) bool {
	var inspect func(bashPPBridgeValue) (pointers int, valid bool)
	inspect = func(v bashPPBridgeValue) (int, bool) {
		pointers := 0
		if v.Kind == "pointer" {
			if v.Origin == 0 {
				return 0, false
			}
			pointers++
		}
		for _, child := range v.Elements {
			n, ok := inspect(child)
			if !ok {
				return 0, false
			}
			pointers += n
		}
		for _, child := range v.Fields {
			n, ok := inspect(child)
			if !ok {
				return 0, false
			}
			pointers += n
		}
		for _, entry := range v.Entries {
			for _, child := range []bashPPBridgeValue{entry.Key, entry.Value} {
				n, ok := inspect(child)
				if !ok {
					return 0, false
				}
				pointers += n
			}
		}
		return pointers, true
	}
	pointers := 0
	for _, arg := range q.Args {
		n, ok := inspect(arg)
		if !ok {
			return false
		}
		pointers += n
	}
	return pointers > 0
}

func nativeRetainedPointerMutator(name string) bool {
	switch name {
	case "flag.Parse", "*flag.FlagSet.Parse":
		return true
	}
	return false
}

func synchronousErrorsAsType(req bashPPEvalRequest, q bashPPBridgeRequest) bool {
	if q.Receiver != nil {
		return false
	}
	alias, name, ok := strings.Cut(q.Selector, ".")
	return ok && req.Imports[alias] == "errors" && name == "AsType" && len(q.Args) == 1
}

// requestCallbackCapable reports a request that must be able to service an
// original callback while it is in flight. That is either a request carrying
// one, or any request on a session that has already been handed a RETAINED
// callback: the dependency may raise that one at any later moment, and the
// request parked at that moment is the only frame able to run it.
func requestCallbackCapable(req bashPPEvalRequest, q bashPPBridgeRequest) bool {
	if requestHasCallbacks(req, q) {
		return true
	}
	return req.Bridge != nil && req.Bridge.retainedCallbacks()
}

// Only requests carrying a local interface callback acquire the gate. Native
// blocking synchronization must remain available to other interpreted tasks.
func requestHasCallbacks(req bashPPEvalRequest, q bashPPBridgeRequest) bool {
	local := map[string]bool{}
	for _, typ := range req.LocalTypes {
		if len(typ.Methods) == 0 {
			continue
		}
		local[typ.Name] = true
		// An instantiated generic type is materialised under a generated name
		// but transported under its instantiation spelling; recognise both, so
		// a value carrying its mirrored method is still seen as a callback.
		if typ.WireType != "" {
			local[typ.WireType] = true
		}
	}
	var check func(bashPPBridgeValue) bool
	check = func(v bashPPBridgeValue) bool {
		if v.Callbacks || local[strings.TrimPrefix(strings.TrimPrefix(v.Type, "*"), "main.")] {
			return true
		}
		for _, c := range v.Elements {
			if check(c) {
				return true
			}
		}
		for _, c := range v.Fields {
			if check(c) {
				return true
			}
		}
		for _, e := range v.Entries {
			if check(e.Key) || check(e.Value) {
				return true
			}
		}
		return false
	}
	for _, v := range q.Args {
		if check(v) {
			return true
		}
	}
	return q.Receiver != nil && check(*q.Receiver)
}
