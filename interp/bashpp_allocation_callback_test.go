package interp

import "testing"

func TestAllocationObserverRejectsSourceLifetimeProof(t *testing.T) {
	req := bashPPEvalRequest{Imports: map[string]string{"probe": "testing"}}
	for _, request := range []bashPPBridgeRequest{
		{Op: "call", Selector: "probe.AllocsPerRun", sourceSynchronousCallback: true},
		{Op: "call", Receiver: &bashPPBridgeValue{Kind: "handle", Function: true, Callable: "testing.AllocsPerRun"}, sourceSynchronousCallback: true},
	} {
		if synchronousFunctionCallback(req, request) {
			t.Fatal("allocation observer admitted solely on callback lifetime")
		}
	}
	if !synchronousFunctionCallback(req, bashPPBridgeRequest{sourceSynchronousCallback: true, Selector: "other.Call"}) {
		t.Fatal("unrelated proven synchronous consumer was rejected")
	}
}
