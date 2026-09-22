//go:build unix

package interp

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBashPPNativeControlListener(t *testing.T) {
	listener, network, cleanup, err := bashPPNativeControlListener()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	defer listener.Close()
	if network != "unix" && network != "tcp" {
		t.Fatalf("network = %q, want unix or loopback TCP fallback", network)
	}
	accepted := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			err = conn.Close()
		}
		accepted <- err
	}()
	conn, err := net.Dial(network, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-accepted; err != nil {
		t.Fatal(err)
	}
}

func TestBashPPNativeControlListenerLongTempFallback(t *testing.T) {
	root := t.TempDir()
	longTemp := root
	for len(filepath.Join(longTemp, "bashpp-control-1234567890", "s")) < 180 {
		longTemp = filepath.Join(longTemp, strings.Repeat("x", 30))
		if err := os.Mkdir(longTemp, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMPDIR", longTemp)
	listener, network, cleanup, err := bashPPNativeControlListener()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	defer listener.Close()
	if network != "tcp" {
		t.Fatalf("network = %q, want tcp fallback for long Unix socket path", network)
	}
	if host, _, err := net.SplitHostPort(listener.Addr().String()); err != nil || host != "127.0.0.1" {
		t.Fatalf("fallback address = %q: host=%q err=%v", listener.Addr(), host, err)
	}
	entries, err := os.ReadDir(longTemp)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed Unix listener left temporary state: %v", entries)
	}
}
