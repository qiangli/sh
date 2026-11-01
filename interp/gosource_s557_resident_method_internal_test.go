//go:build full

package interp

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestS557ResidentBoundMethod(t *testing.T) {
	r, err := New()
	if err != nil {
		t.Fatal(err)
	}
	state := new(goSourceResidentSync)
	receiver := bashPPBridgeValue{Kind: "handle", Type: "sync.Mutex", residentSync: state}
	request := func(ctx context.Context, op, selector string, recv *bashPPBridgeValue) ([]bashPPBridgeValue, error) {
		values, handled, err := r.goSourceResidentSyncRequest(ctx, bashPPBridgeRequest{Op: op, Selector: selector, Receiver: recv})
		if !handled {
			t.Fatal("resident request escaped to transport")
		}
		return values, err
	}
	ctx := context.Background()
	if _, err := request(ctx, "call", "Lock", &receiver); err != nil {
		t.Fatal(err)
	}
	values, err := request(ctx, "get", "Unlock", &receiver)
	if err != nil || len(values) != 1 {
		t.Fatalf("bind = %v, %v", values, err)
	}
	bound := values[0]
	if !state.locked || !bound.Function || residentSyncValue(bound) != state {
		t.Fatal("binding changed or lost receiver")
	}
	if _, err := request(ctx, "call", "Lock", &bound); err == nil {
		t.Fatal("bound selector could be replaced")
	}
	if _, err := request(ctx, "call", "", &bound); err != nil {
		t.Fatal(err)
	}
	if state.locked {
		t.Fatal("bound method did not unlock captured receiver")
	}
	if _, err := request(ctx, "get", "Absent", &receiver); err == nil {
		t.Fatal("unknown method was bound")
	}
	session := new(bashPPNativeSession)
	if _, err := session.request(ctx, bashPPEvalRequest{}, bashPPBridgeRequest{Op: "call", Selector: "fmt.Sprint", Args: []bashPPBridgeValue{bound}}); err == nil || !strings.Contains(err.Error(), "resident sync cannot enter native transport") {
		t.Fatalf("bound method crossed transport: %v", err)
	}
	values, err = request(ctx, "get", "Lock", &receiver)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := request(canceled, "call", "", &values[0]); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled bound call = %v", err)
	}
	if state.locked {
		t.Fatal("canceled bound call acquired mutex")
	}
}
