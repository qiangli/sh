//go:build full

package interp

import (
	"context"
	"testing"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

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
		bashPPTools: bashPPToolchain{
			callbackDepth: 1,
			routedDepth:   1,
		},
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
	if child.bashPPTools.callbackDepth != 0 || child.bashPPTools.routedDepth != 0 {
		t.Fatalf("callback invocation depth leaked into child: callback=%d routed=%d", child.bashPPTools.callbackDepth, child.bashPPTools.routedDepth)
	}
	if !child.bashPPTools.callbackDescendant {
		t.Fatal("child lost callback descendant routing provenance")
	}
	if parent.bashPPCallbackReturnDepth != 1 {
		t.Fatalf("child clone changed parent callback marker: got depth %d", parent.bashPPCallbackReturnDepth)
	}
}

func TestBashPPTestingCallbackFrameOwnership(t *testing.T) {
	parent, err := New(Lang(syntax.LangBashPP), Env(expand.ListEnviron()))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.closeDirFile()
	parent.Reset()
	scope := newBashPPScope(nil)
	shared := &bashPPCell{}
	scope.entries["shared"] = shared
	launch := parseGoStmt(t, "func main() {\n\tgo func() { shared++ }()\n}\n")
	fn := &bashPPFunc{lit: launch.Call.FuncLit, scope: scope}
	parent.bashPPGoSource = true
	parent.bashPPScope = scope
	parent.bashPPFuncs = map[string]*bashPPFunc{"callback": fn}
	parent.bashPPConcurrent = newBashPPConcurrent(context.Background())
	defer parent.bashPPConcurrent.cancel()
	parent.bashPPTools.callbackDepth = 1
	parent.bashPPTools.routedDepth = 1

	group := new(bashPPTestingCallbackFrames)
	registered, err := parent.bashPPCallbackFunctionTemplate(fn)
	if err != nil {
		t.Fatal(err)
	}
	defer registered.template.closeDirFile()
	child, childFn, err := registered.bashPPTestingCallbackFrame(group)
	if err != nil {
		t.Fatal(err)
	}
	defer child.closeDirFile()

	if child == parent || child.bashPPScope == parent.bashPPScope {
		t.Fatal("testing callback reused its parent Runner frame")
	}
	if got := child.bashPPScope.lookup("shared"); got != shared {
		t.Fatalf("testing callback split shared Go cell: got %p want %p", got, shared)
	}
	if childFn == fn || childFn.scope.lookup("shared") != shared {
		t.Fatal("testing callback function did not come from the immutable template")
	}
	if child.bashPPTools.testingCallbackFrames != group || !child.bashPPTools.callbackDescendant {
		t.Fatal("testing callback lost scheduler ownership or routed lineage")
	}
	if child.bashPPTools.callbackDepth != 0 || child.bashPPTools.routedDepth != 0 {
		t.Fatalf("testing callback inherited active stack depths: callback=%d routed=%d", child.bashPPTools.callbackDepth, child.bashPPTools.routedDepth)
	}
	if !child.bashPPGoTask || child.bashPPChanBoundary || child.bashPPConcurrent == nil || child.bashPPConcurrent != parent.bashPPConcurrent {
		t.Fatal("testing callback lost its Go task-group capabilities")
	}
	if parent.bashPPTools.testingCallbackFrames != nil || parent.bashPPTools.callbackDepth != 1 || parent.bashPPTools.routedDepth != 1 {
		t.Fatal("testing callback changed parent frame ownership")
	}

	session := &bashPPNativeSession{functions: map[uint64]*bashPPFunc{7: fn}, functionOwners: map[uint64]*bashPPCallbackFunction{
		7: registered,
	}}
	if got := session.callbackFunction(7); got != registered || got.template == parent || !got.capture[shared] {
		t.Fatalf("callback handle lost registered template: %#v", got)
	}
	legacy := &bashPPNativeSession{functions: map[uint64]*bashPPFunc{7: fn}}
	if got := legacy.callbackFunction(7); got != nil {
		t.Fatalf("legacy function handle fabricated template ownership: %#v", got)
	}
	if got := legacy.originalCallbackFunction(7); got != fn {
		t.Fatalf("legacy function handle did not keep its callback path: %#v", got)
	}
	if _, _, scheduled, err := legacy.testingCallbackFrame(group, bashPPBridgeResponse{Receiver: &bashPPBridgeValue{Kind: "callback", Handle: 7}}); err != nil || scheduled {
		t.Fatalf("legacy callback unexpectedly entered template scheduler: scheduled=%v err=%v", scheduled, err)
	}
	answer := session.callbackAnswer(parent.ectx, parent, bashPPBridgeResponse{ID: 1, Receiver: &bashPPBridgeValue{Kind: "callback", Handle: 8}}, nil, nil)
	if answer.Error != "gosource: original callback handle expired" {
		t.Fatalf("unknown callback was not refused: %#v", answer)
	}
}
