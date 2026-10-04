//go:build full

package interp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/syntax"
)

func TestS374FastStdlibEligibility(t *testing.T) {
	req := bashPPEvalRequest{Imports: map[string]string{"conv": "strconv", "rng": "math/rand"}}
	q := bashPPBridgeRequest{Op: "call", Selector: "conv.Itoa", Args: []bashPPBridgeValue{{Kind: "int", Type: "int", Text: "41"}}}
	got, handled, err := goSourceFastStdlibCall(context.Background(), req, q)
	if err != nil || !handled || len(got) != 1 || got[0].stringText() != "41" {
		t.Fatalf("Itoa direct call: %#v, %v, %v", got, handled, err)
	}
	q.Args[0].Origin = 1
	if _, handled, _ := goSourceFastStdlibCall(context.Background(), req, q); handled {
		t.Fatal("value with interpreter identity took direct path")
	}
	for _, source := range []string{
		`package main; import "math/rand"; func main(){ _=rand.Read }`,
		`package main; import "math/rand"; func main(){ var b [4]byte; rand.Read(b[:]) }`,
		`package main; import "math/rand"; func main(){ _=rand.Intn(2); rand:=struct{Intn func(int)int}{}; _=rand.Intn }`,
	} {
		path := filepath.Join(t.TempDir(), "main.go")
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		if fastRandSourceProof(bashPPEvalRequest{SourceFile: path, Imports: map[string]string{"rand": "math/rand"}}, "rand") {
			t.Fatal("uncovered random operation claimed interpreter ownership")
		}
	}
}

func TestS374FastStdlibWorkerParity(t *testing.T) {
	t.Setenv("GODEBUG", "randautoseed=0,randseednop=0")
	source := `package main
import "strconv"
import "math/rand"
func main(){println("capture"); _=strconv.Itoa(1); _=strconv.FormatInt(1,16); _=strconv.Quote("x"); rand.Seed(17); _=rand.Intn(10); _=rand.Int63()}`
	var runner *Runner
	var probeErr error
	called := false
	writer := callbackProbeWriter(func(p []byte) (int, error) {
		if !strings.Contains(string(p), "capture") || called {
			return len(p), nil
		}
		called = true
		req, err := runner.bashPPEvalRequest()
		if err != nil {
			probeErr = err
			return len(p), nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, q := range []bashPPBridgeRequest{
			{Op: "call", Selector: "strconv.Itoa", Args: []bashPPBridgeValue{{Kind: "int", Type: "int", Text: "-73"}}},
			{Op: "call", Selector: "strconv.FormatInt", Args: []bashPPBridgeValue{{Kind: "int", Type: "int64", Text: "-73"}, {Kind: "int", Type: "int", Text: "16"}}},
			{Op: "call", Selector: "strconv.Quote", Args: []bashPPBridgeValue{{Kind: "string", Type: "string", Text: "a\n"}}},
		} {
			fast, handled, err := goSourceFastStdlibCall(ctx, req, q)
			if err != nil || !handled {
				probeErr = fmt.Errorf("fast %s: handled=%v err=%v", q.Selector, handled, err)
				return len(p), nil
			}
			worker, err := req.Bridge.request(ctx, req, q)
			if err != nil {
				probeErr = err
				return len(p), nil
			}
			if len(fast) != len(worker) || fast[0].Kind != worker[0].Kind || fast[0].Type != worker[0].Type || fast[0].Text != worker[0].Text || fast[0].stringText() != worker[0].stringText() {
				probeErr = fmt.Errorf("%s: fast=%#v worker=%#v", q.Selector, fast, worker)
				return len(p), nil
			}
		}
		seed := bashPPBridgeRequest{Op: "call", Selector: "rand.Seed", Args: []bashPPBridgeValue{{Kind: "int", Type: "int64", Text: "23"}}}
		for _, q := range []bashPPBridgeRequest{{Op: "call", Selector: "rand.Intn", Args: []bashPPBridgeValue{{Kind: "int", Type: "int", Text: "1000000"}}}, seed, {Op: "call", Selector: "rand.Intn", Args: []bashPPBridgeValue{{Kind: "int", Type: "int", Text: "1000000"}}}, {Op: "call", Selector: "rand.Int63"}} {
			fast, handled, err := goSourceFastStdlibCall(ctx, req, q)
			if err != nil || !handled {
				probeErr = fmt.Errorf("fast %s: handled=%v err=%v", q.Selector, handled, err)
				return len(p), nil
			}
			worker, err := req.Bridge.request(ctx, req, q)
			if err != nil {
				probeErr = err
				return len(p), nil
			}
			if len(fast) != len(worker) || len(fast) > 0 && (fast[0].Kind != worker[0].Kind || fast[0].Type != worker[0].Type || fast[0].Text != worker[0].Text) {
				probeErr = fmt.Errorf("%s: fast=%#v worker=%#v", q.Selector, fast, worker)
				return len(p), nil
			}
		}
		bad := bashPPBridgeRequest{Op: "call", Selector: "rand.Intn", Args: []bashPPBridgeValue{{Kind: "int", Type: "int", Text: "-1"}}}
		_, handled, directErr := goSourceFastStdlibCall(ctx, req, bad)
		_, workerErr := req.Bridge.request(ctx, req, bad)
		if handled || directErr != nil || workerErr == nil {
			probeErr = fmt.Errorf("panic parity: handled=%v direct=%v worker=%v", handled, directErr, workerErr)
		}
		return len(p), nil
	})
	var err error
	runner, err = New(Lang(syntax.LangBashPP), Dir(t.TempDir()), StdIO(nil, io.Discard, writer))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runner.Dir, "main.go")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	program, err := gosource.Parse(strings.NewReader(source), path, gosource.Options{RunMain: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runner.Run(ctx, program.File); err != nil {
		t.Fatal(err)
	}
	if !called || probeErr != nil {
		t.Fatalf("probe called=%v error=%v", called, probeErr)
	}
}

func BenchmarkS374StdlibCall(b *testing.B) {
	for _, symbol := range []string{"strconv.Itoa", "math/rand.Intn"} {
		for _, mode := range []string{"worker", "direct"} {
			b.Run(symbol+"/"+mode, func(b *testing.B) {
				b.StopTimer()
				source := `package main
import "strconv"
func main(){println("capture"); _=strconv.Itoa(1)}`
				q := bashPPBridgeRequest{Op: "call", Selector: "strconv.Itoa", Args: []bashPPBridgeValue{{Kind: "int", Type: "int", Text: "41"}}}
				if symbol == "math/rand.Intn" {
					source = `package main
import "math/rand"
func main(){println("capture"); _=rand.Intn(1000000)}`
					q = bashPPBridgeRequest{Op: "call", Selector: "rand.Intn", Args: []bashPPBridgeValue{{Kind: "int", Type: "int", Text: "1000000"}}}
				}
				var runner *Runner
				var probeErr error
				called := false
				writer := callbackProbeWriter(func(p []byte) (int, error) {
					if !strings.Contains(string(p), "capture") || called {
						return len(p), nil
					}
					called = true
					req, err := runner.bashPPEvalRequest()
					if err != nil {
						probeErr = err
						return len(p), nil
					}
					req.disableFastStdlib = mode == "worker"
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					if _, err := runner.bashPPNativeRequest(ctx, req, q); err != nil {
						probeErr = err
						return len(p), nil
					}
					b.ReportAllocs()
					b.ResetTimer()
					b.StartTimer()
					for i := 0; i < b.N; i++ {
						values, err := runner.bashPPNativeRequest(ctx, req, q)
						valid := len(values) == 1
						if valid && symbol == "strconv.Itoa" {
							valid = values[0].stringText() == "41"
						}
						if valid && symbol == "math/rand.Intn" {
							n, parseErr := strconv.Atoi(values[0].Text)
							valid = parseErr == nil && n >= 0 && n < 1000000
						}
						if err != nil || !valid {
							probeErr = fmt.Errorf("call %d: %v, %#v", i, err, values)
							break
						}
					}
					b.StopTimer()
					return len(p), nil
				})
				var err error
				runner, err = New(Lang(syntax.LangBashPP), Dir(b.TempDir()), StdIO(nil, io.Discard, writer))
				if err != nil {
					b.Fatal(err)
				}
				path := filepath.Join(runner.Dir, "main.go")
				if err := os.WriteFile(path, []byte(source), 0600); err != nil {
					b.Fatal(err)
				}
				program, err := gosource.Parse(strings.NewReader(source), path, gosource.Options{RunMain: true})
				if err != nil {
					b.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()
				if err := runner.Run(ctx, program.File); err != nil {
					b.Fatal(err)
				}
				if !called || probeErr != nil {
					b.Fatalf("probe called=%v error=%v", called, probeErr)
				}
			})
		}
	}
}
