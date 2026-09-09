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
	var inspect func(bashPPBridgeValue) (bool, bool, error)
	inspect = func(v bashPPBridgeValue) (bool, bool, error) {
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
		nestedRef := false
		for _, child := range v.Elements {
			l, ref, err := inspect(child)
			if err != nil {
				return false, false, err
			}
			isLocal = isLocal || l
			nestedRef = nestedRef || ref
		}
		for _, child := range v.Fields {
			l, ref, err := inspect(child)
			if err != nil {
				return false, false, err
			}
			isLocal = isLocal || l
			nestedRef = nestedRef || ref
		}
		for _, entry := range v.Entries {
			l, ref, err := inspect(entry.Value)
			if err != nil {
				return false, false, err
			}
			isLocal = isLocal || l
			nestedRef = nestedRef || ref
		}
		// Value-receiver methods on copied maps/slices/reference-bearing structs
		// cannot silently lose effects on their shared referents.
		if len(typ.Methods) > 0 && (reference || nestedRef) && v.Origin == 0 {
			for _, m := range typ.Methods {
				if !m.Pointer {
					return false, false, fmt.Errorf("gosource: callback on reference-bearing value %s requires original reference identity", v.Type)
				}
			}
		}
		return isLocal, reference || nestedRef, nil
	}
	unsafe := false
	for _, arg := range q.Args {
		local, ref, err := inspect(arg)
		if err != nil {
			return err
		}
		unsafe = unsafe || (local && ref) || arg.Kind == "pointer"
	}
	if synchronousReaderCallback(req, q) || synchronousImageCallback(req, q) {
		return nil
	}
	if functionCallbacks && !synchronousFunctionCallback(req, q) {
		return fmt.Errorf("gosource: asynchronous or retained original function callbacks are unsupported for %s", q.Selector)
	}
	if !unsafe && synchronousFunctionCallback(req, q) {
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
