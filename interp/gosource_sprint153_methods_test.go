package interp_test

// Sprint: #153; Story: S153.2; Story-ID: 7f74c9ff55b9
//
// Full method mirror for materialised local types: every method with an
// expressible non-variadic signature — exported or unexported, any result
// arity — is mirrored by the generalised protocol stub, so the dependency's
// reflect view presents the original method set and can invoke any mirrored
// method through the callback. Reproducers live under
// testdata/sprint153/method-mirror/; differSprint153 is in
// gosource_sprint153_bridge_test.go and refuseSprint153 in
// gosource_sprint153_reflect_test.go.

import "testing"

// TestGoSourceBridgeMethodMirror covers the mirrored method set: reflect
// enumeration over a type whose methods were previously omitted, a
// dependency-invoked mirror with typed results (json.Marshaler), and the
// already-supported String mirror as the positive control.
func TestGoSourceBridgeMethodMirror(t *testing.T) {
	differSprint153(t, "method-mirror")
}

// TestGoSourceBridgeMethodMirrorRefusal proves the remaining class is refused,
// not hung: a variadic method signature is outside the mirror and its owner
// type keeps the omitted-method refusal for non-fmt consumers.
func TestGoSourceBridgeMethodMirrorRefusal(t *testing.T) {
	refuseSprint153(t, "method-mirror", "variadic_method_refused.go.txt",
		"original method V.Join is not supported by dependency transport")
}
