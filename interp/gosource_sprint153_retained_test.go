package interp_test

// Sprint: #153; Story: S153.2; Story-ID: 7f74c9ff55b9
//
// Retained function callbacks with pointer parameters (runtime.SetFinalizer)
// stay refused by design. The observable of every SetFinalizer root is the
// memory runtime's own behaviour over the program's heap — which allocations
// combine, when they become unreachable, whether a finalizer runs at all —
// and a bridge mirror cannot reproduce that liveness: mirrored child
// allocations are pinned by the session's handle tables or die out of sync
// with interpreter cell lifetime. The bounded deliverable is promptness of
// the refusal; the two-part design a real close would need is recorded in
// testdata/sprint153/bridge/FINDINGS-S153.2.md.

import "testing"

// TestGoSourceBridgeRetainedFinalizerRefusal proves runtime.SetFinalizer with
// an original pointer-parameter callback is refused promptly, never hung.
func TestGoSourceBridgeRetainedFinalizerRefusal(t *testing.T) {
	refuseSprint153(t, "retained-callback", "setfinalizer_refused.go.txt",
		"original callback signature requires value-semantics parameters")
}
