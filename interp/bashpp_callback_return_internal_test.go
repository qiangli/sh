//go:build full

package interp

import "testing"

// TestBashPPCallbackReturnMarkerCloneBoundary guards the invocation boundary
// used by native callbacks. A shell copy created while a callback is active
// may later enter an unrelated helper at the same numeric function depth; the
// parent's marker must not make that helper's return a callback return.
func TestBashPPCallbackReturnMarkerCloneBoundary(t *testing.T) {
	parent := &Runner{
		didReset:                  true,
		bashPPGoSource:            true,
		bashPPFuncActive:          1,
		bashPPCallbackReturnDepth: 1,
	}
	child := parent.subshell(false)
	defer child.closeDirFile()

	// Model the unrelated helper frame whose depth happens to equal the
	// callback frame in the parent. Only provenance, not numeric equality in an
	// ancestor, may authorize deferred composite transport.
	child.bashPPFuncActive = parent.bashPPCallbackReturnDepth
	if child.bashPPCallbackReturnDepth != 0 {
		t.Fatalf("callback return marker leaked into child helper: got depth %d", child.bashPPCallbackReturnDepth)
	}
	if parent.bashPPCallbackReturnDepth != 1 {
		t.Fatalf("child clone changed parent callback marker: got depth %d", parent.bashPPCallbackReturnDepth)
	}
}
