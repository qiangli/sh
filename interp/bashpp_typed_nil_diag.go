package interp

// Sprint: #281; Story: #809; Story-ID: fac7e14af4a8

import (
	"fmt"
	"os"
)

const bashPPTypedNilDiagLimit = 30

// bashPPTypedNilDiag is a bounded, opt-in trace for the native typed-nil
// transport. It deliberately writes to the host process descriptor rather
// than Runner's program output streams, and reports representation metadata
// only: it never formats a transported value or source expression.
func (r *Runner) bashPPTypedNilDiag(stage string, value any, meta *bashPPCollectionMeta, bridges ...bashPPBridgeValue) {
	if r == nil || os.Getenv("BASHPP_TYPED_NIL_DIAG") == "" {
		return
	}
	session := r.bashPPTools.bridge
	if session == nil {
		return
	}
	line := session.typedNilDiag.Add(1)
	if line > bashPPTypedNilDiagLimit {
		return
	}
	fmt.Fprintf(os.Stderr, "BASHPP_TYPED_NIL_DIAG line=%d stage=%s value=%s meta=%s", line, stage, bashPPTypedNilValueSummary(value), bashPPTypedNilMetaSummary(meta))
	for i := range bridges {
		fmt.Fprintf(os.Stderr, " bridge%d=%s", i, bashPPTypedNilBridgeSummary(bridges[i]))
	}
	fmt.Fprintln(os.Stderr)
}

func bashPPTypedNilValueSummary(value any) string {
	switch value.(type) {
	case nil:
		return "<nil>"
	case *bashPPBridgeValue:
		return "*bridge"
	case *bashPPInterfaceValue:
		return "*interface"
	case *bashPPPointer:
		return "*pointer"
	case map[string]any:
		return "struct-storage"
	case []any:
		return "sequence-storage"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func bashPPTypedNilMetaSummary(meta *bashPPCollectionMeta) string {
	if meta == nil {
		return "<nil>"
	}
	iface := meta.interfaceValue
	if iface == nil {
		return fmt.Sprintf("kind:%q type:%q interface:false", meta.kind, bashPPTypeText(meta.typ))
	}
	dynamic := ""
	cell := "nil"
	if iface.dynamic != nil {
		dynamic = bashPPTypeText(iface.dynamic)
	}
	if iface.cell != nil {
		cell = fmt.Sprintf("decl:%q carrier:%s pointer:%t", bashPPTypeText(iface.cell.declType), bashPPTypedNilValueSummary(iface.cell.vr.Obj), iface.cell.pointer)
	}
	return fmt.Sprintf("kind:%q type:%q interface:true nil_interface:%t dynamic:%q cell:{%s}", meta.kind, bashPPTypeText(meta.typ), iface.nilIface, dynamic, cell)
}

func bashPPTypedNilBridgeSummary(value bashPPBridgeValue) string {
	return fmt.Sprintf("kind:%q type:%q native_type:%q interface:%q session:%t", value.Kind, value.Type, value.NativeType, value.Interface, value.Session != "")
}

func (r *Runner) bashPPComparablePayloadDiag(value any, meta *bashPPCollectionMeta) any {
	switch {
	case meta == nil:
		r.bashPPTypedNilDiag("comparable-payload/no-meta", value, meta)
	case meta.kind != "interface":
		r.bashPPTypedNilDiag("comparable-payload/non-interface", value, meta)
	case meta.interfaceValue == nil:
		r.bashPPTypedNilDiag("comparable-payload/no-interface-value", value, meta)
	case bashPPTypedNilInterfacePayload(value):
		r.bashPPTypedNilDiag("comparable-payload/already-interface", value, meta)
	default:
		r.bashPPTypedNilDiag("comparable-payload/recover-interface", value, meta)
	}
	return bashPPComparablePayload(value, meta)
}

func bashPPTypedNilInterfacePayload(value any) bool {
	_, ok := value.(*bashPPInterfaceValue)
	return ok
}

func bashPPTypedNilBridgeCarrier(value any) bool {
	bridge, ok := value.(*bashPPBridgeValue)
	return ok && bridge != nil && bridge.Kind == "nil"
}
