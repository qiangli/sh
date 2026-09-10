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
	if nativeWriterFormat && (len(q.Args) == 0 || q.Args[0].Kind != "handle") {
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
		name := strings.TrimPrefix(v.Type, "main.")
		if v.Kind == "nil" && strings.HasPrefix(name, "*") {
			if t, ok := local[strings.TrimPrefix(name, "*")]; ok && len(t.Methods) > 0 {
				return false, false, fmt.Errorf("gosource: nil pointer callback requires original reference identity")
			}
		}
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
	if !functionCallbacks && nativePointerWritebackAllowed(req, q) {
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
		local[typ.Name] = len(typ.Methods) > 0
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
