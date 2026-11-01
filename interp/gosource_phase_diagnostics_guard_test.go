//go:build full

package interp_test

import (
	"testing"
	"time"
)

func TestPhaseDiagnosticCanceledBeforeSnapshot(t *testing.T) {
	stop := startPhaseDiagnostic(time.Hour, func() string { t.Error("snapshot after cancellation"); return "unexpected" })
	if got := stop(); got != "" {
		t.Fatalf("capture=%q", got)
	}
	if got := stop(); got != "" {
		t.Fatalf("repeated stop capture=%q", got)
	}
}

func TestPhaseDiagnosticJoinsSnapshot(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	stop := startPhaseDiagnostic(0, func() string { close(entered); <-release; return "captured" })
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("snapshot never started")
	}
	joined := make(chan string, 1)
	go func() { joined <- stop() }()
	select {
	case got := <-joined:
		t.Fatalf("stop returned before snapshot: %q", got)
	default:
	}
	close(release)
	select {
	case got := <-joined:
		if got != "captured" {
			t.Fatalf("capture=%q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot not joined")
	}
	if got := stop(); got != "captured" {
		t.Fatalf("repeated capture=%q", got)
	}
}
