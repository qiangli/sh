package interp_test

// Sprint: #153; Story: S153.2; Story-ID: 7f74c9ff55b9
//
// Embedded-field transport. A local struct with an embedded field used to be
// unmaterialisable (named types) or spelled with an <unsupported embedded
// field> placeholder (anonymous shapes). The helper now emits real embedding
// of the rendered element type, the host transports the embedded storage
// under its promoted name, and the generated codecs read and write that same
// storage. Reproducers live under testdata/sprint153/embedded-transport/.

import "testing"

// TestGoSourceBridgeEmbeddedTransport covers a named local struct with an
// embedded method-bearing type (promotion included), an anonymous struct
// embedding a function-local type, and a flat struct as positive control.
func TestGoSourceBridgeEmbeddedTransport(t *testing.T) {
	differSprint153(t, "embedded-transport")
}

// TestGoSourceBridgeEmbeddedRefusal proves the remaining class is refused,
// not hung: embedding an instantiated generic spelling stays outside the
// materialised set and the enclosing type keeps the unregistered refusal.
func TestGoSourceBridgeEmbeddedRefusal(t *testing.T) {
	refuseSprint153(t, "embedded-transport", "generic_embedded_refused.go.txt",
		`unregistered bridge type "W"`)
}
