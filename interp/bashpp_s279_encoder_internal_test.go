// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

// Sprint: #279; Story: #757; Story-ID: 609c89bfa598

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/syntax"
)

// TestBashPPS279SessionEncoder covers the hot imported-call representation:
// one live connection owns one encoder while many direct calls use it.  The
// writer captures the session before Run's cleanup closes it, so the test also
// proves the encoder is installed at the same connection boundary as the
// worker handshake.
func TestBashPPS279SessionEncoder(t *testing.T) {
	const source = `package main
import "strings"
func main() {
	n := 0
	for i := 0; i < 100; i++ {
		if strings.HasPrefix("interpreter", "inter") { n++ }
	}
	println(n)
}
`
	program, err := gosource.Parse(strings.NewReader(source), "s279_encoder.go", gosource.Options{RunMain: true})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	var runner *Runner
	var session *bashPPNativeSession
	probe := callbackProbeWriter(func(p []byte) (int, error) {
		stderr.Write(p)
		if strings.Contains(string(p), "100") && runner != nil {
			session = runner.bashPPTools.bridge
		}
		return len(p), nil
	})
	runner, err = New(Lang(syntax.LangBashPP), Dir(t.TempDir()), StdIO(nil, &stdout, probe))
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), program.File); err != nil {
		t.Fatalf("Run: %v; stderr=%q", err, stderr.String())
	}
	if got := strings.TrimSpace(stderr.String()); got != "100" {
		t.Fatalf("output = %q, want 100", got)
	}
	if session == nil {
		t.Fatal("native session was not live while imported calls ran")
	}
	session.write.Lock()
	encoder := session.encoder
	session.write.Unlock()
	if encoder == nil {
		t.Fatal("live native session has no connection-owned encoder")
	}
}
