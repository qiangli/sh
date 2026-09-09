package interp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/syntax"
)

type callbackProbeWriter func([]byte) (int, error)

func (w callbackProbeWriter) Write(p []byte) (int, error) { return w(p) }

// A callback ID from a completed run must not resolve to the new run's closure
// with the same numeric ID. Probe two real initialized dependency sessions.
func TestGoSourceFunctionCallbackStaleSession(t *testing.T) {
	var runner *Runner
	var old bashPPBridgeValue
	var probeErr error
	round := 0
	writer := callbackProbeWriter(func(p []byte) (int, error) {
		if !bytes.Contains(p, []byte("capture")) {
			return len(p), nil
		}
		req, err := runner.bashPPEvalRequest()
		if err != nil {
			probeErr = err
			return len(p), nil
		}
		if round == 0 {
			req.Bridge.mu.Lock()
			for id := range req.Bridge.functions {
				old = bashPPBridgeValue{Kind: "callback", Callbacks: true, Session: req.Bridge.id, Handle: id}
				break
			}
			req.Bridge.mu.Unlock()
		} else {
			alias := ""
			for name, path := range req.Imports {
				if path == "strings" {
					alias = name
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, probeErr = runner.bashPPNativeRequest(ctx, req, bashPPBridgeRequest{Op: "call", Selector: alias + ".Map", Args: []bashPPBridgeValue{old, {Kind: "string", Type: "string", Text: "a"}}})
		}
		return len(p), nil
	})
	var err error
	runner, err = New(Lang(syntax.LangBashPP), Dir(t.TempDir()), StdIO(nil, io.Discard, writer))
	if err != nil {
		t.Fatal(err)
	}
	const source = `package main
import "strings"
func main(){strings.Map(func(r rune)rune{println("capture");return r},"a")}`
	for round = 0; round < 2; round++ {
		if round > 0 {
			runner.Reset()
		}
		p, err := gosource.Parse(strings.NewReader(source), filepath.Join(runner.Dir, fmt.Sprintf("round%d.go", round)), gosource.Options{RunMain: true})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err = runner.Run(ctx, p.File)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
	if old.Session == "" || old.Handle == 0 {
		t.Fatal("first original callback was not registered")
	}
	if probeErr == nil || !strings.Contains(probeErr.Error(), "another dependency session") {
		t.Fatalf("stale callback accepted: %v", probeErr)
	}
}
