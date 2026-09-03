package interp

import (
	"context"
	"errors"
	"io"
	"maps"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

type recordingBashPPEval struct {
	mu       sync.Mutex
	resolved map[string]string
	calls    []bashPPEvalRequest
	err      error
}

func (e *recordingBashPPEval) Resolve(ctx context.Context, req bashPPEvalRequest, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.resolved[path], e.err
}
func (e *recordingBashPPEval) Call(ctx context.Context, req bashPPEvalRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	req.Imports = maps.Clone(req.Imports)
	e.calls = append(e.calls, req)
	return e.err
}

func parseBashPPInternal(t *testing.T, src string) *syntax.File {
	t.Helper()
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "test.bpp")
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func newInjectedBashPPRunner(t *testing.T, eval bashPPEvaluator) *Runner {
	t.Helper()
	r, err := New(StdIO(nil, io.Discard, io.Discard), Lang(syntax.LangBashPP))
	if err != nil {
		t.Fatal(err)
	}
	r.bashPPTools = bashPPToolchain{goBinary: "/reviewed/go", eval: eval}
	return r
}

func TestBashPPImportRegistryContract(t *testing.T) {
	eval := &recordingBashPPEval{resolved: map[string]string{"fmt": "fmt", "log": "log"}}
	r := newInjectedBashPPRunner(t, eval)
	run := func(src string) error { return r.Run(context.Background(), parseBashPPInternal(t, src)) }
	if err := run("import \"fmt\"\nimport \"fmt\"\n"); err != nil {
		t.Fatal(err)
	}
	if got := r.bashPPImports; len(got) != 1 || got["fmt"] != "fmt" {
		t.Fatalf("imports %#v", got)
	}
	before := maps.Clone(r.bashPPImports)
	if err := run("import fmt \"log\"\n"); err == nil || !strings.Contains(err.Error(), "import name fmt") {
		t.Fatalf("alias collision: %v", err)
	}
	if !maps.Equal(before, r.bashPPImports) {
		t.Fatalf("alias collision mutated registry: %#v", r.bashPPImports)
	}
	if err := run("import f \"fmt\"\n"); err == nil || !strings.Contains(err.Error(), "import path") {
		t.Fatalf("path collision: %v", err)
	}
	if !maps.Equal(before, r.bashPPImports) {
		t.Fatalf("path collision mutated registry: %#v", r.bashPPImports)
	}
	r.Reset()
	if len(r.bashPPImports) != 0 {
		t.Fatalf("Reset retained imports: %#v", r.bashPPImports)
	}
	if r.bashPPTools.goBinary != "/reviewed/go" || r.bashPPTools.eval != eval {
		t.Fatal("Reset lost injected toolchain identity")
	}
}

func TestBashPPGroupedImportIsAtomicAndUsesGoAliases(t *testing.T) {
	eval := &recordingBashPPEval{resolved: map[string]string{"fmt": "fmt", "log": "log", "embed": "embed"}}
	r := newInjectedBashPPRunner(t, eval)
	run := func(src string) error { return r.Run(context.Background(), parseBashPPInternal(t, src)) }
	if err := run("import (\n f \"fmt\"\n _ \"embed\"\n . \"log\"\n)\n"); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"f": "fmt", "_:embed": "embed", ".:log": "log"}
	if !maps.Equal(r.bashPPImports, want) {
		t.Fatalf("imports %#v, want %#v", r.bashPPImports, want)
	}
	before := maps.Clone(r.bashPPImports)
	if err := run("import (\n j \"encoding/json\"\n f \"log\"\n)\n"); err == nil {
		t.Fatal("expected grouped alias collision")
	}
	if !maps.Equal(r.bashPPImports, before) {
		t.Fatalf("failed group mutated registry: %#v", r.bashPPImports)
	}
	r.Reset()
	if len(r.bashPPImports) != 0 {
		t.Fatalf("Reset retained group: %#v", r.bashPPImports)
	}
}

func TestBashPPImportUsesInterpretedPath(t *testing.T) {
	eval := &recordingBashPPEval{resolved: map[string]string{"fmt": "fmt"}}
	r := newInjectedBashPPRunner(t, eval)
	if err := r.Run(context.Background(), parseBashPPInternal(t, `import "\x66mt"`)); err != nil {
		t.Fatal(err)
	}
	if got := r.bashPPImports["fmt"]; got != "fmt" {
		t.Fatalf("decoded path = %q", got)
	}
}

func TestBashPPImportSubshellIsolationAndCancellation(t *testing.T) {
	eval := &recordingBashPPEval{resolved: map[string]string{"fmt": "fmt"}}
	r := newInjectedBashPPRunner(t, eval)
	if err := r.Run(context.Background(), parseBashPPInternal(t, "import \"fmt\"\n")); err != nil {
		t.Fatal(err)
	}
	child := r.subshell(false)
	child.bashPPImports["log"] = "log"
	if _, ok := r.bashPPImports["log"]; ok {
		t.Fatal("subshell import leaked to parent session")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.Run(ctx, parseBashPPInternal(t, "fmt.Println(\"no\")\n"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestBashPPImportSubshellsRunWithoutSharedRegistry(t *testing.T) {
	eval := &recordingBashPPEval{resolved: map[string]string{"fmt": "fmt"}}
	parent := newInjectedBashPPRunner(t, eval)
	if err := parent.Run(context.Background(), parseBashPPInternal(t, "import \"fmt\"\n")); err != nil {
		t.Fatal(err)
	}
	children := make([]*Runner, 8)
	for i := range children {
		children[i] = parent.subshell(false)
	}
	call := parseBashPPInternal(t, "fmt.Println(\"x\")\n")
	var wg sync.WaitGroup
	for _, child := range children {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := child.Run(context.Background(), call); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(parent.bashPPImports) != 1 {
		t.Fatalf("parent registry mutated: %#v", parent.bashPPImports)
	}
}

func TestNativeBashPPEvaluatorRejectsArgumentInjection(t *testing.T) {
	e := nativeBashPPEvaluator{}
	err := e.Call(context.Background(), bashPPEvalRequest{
		Go: "/must/not/run", Imports: map[string]string{"fmt": "fmt"},
		Selector: []string{"fmt", "Println"}, Args: []string{`"ok"); panic("injected"`},
	})
	if err == nil || !strings.Contains(err.Error(), "invalid Go argument") {
		t.Fatalf("got %v", err)
	}
}

func TestBashPPGoIdentityIgnoresPATH(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	got, err := bashPPGoIdentity()
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(got.Root, "bin", "go"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ = filepath.Abs(want)
	if got.Binary != want {
		t.Fatalf("Go identity %q, want %q", got.Binary, want)
	}
	if got.Version != "go1.27.0" || got.GOOS != runtime.GOOS || got.GOARCH != runtime.GOARCH {
		t.Fatalf("Go identity = %#v", got)
	}
}

func TestBashPPGoIdentityMutationRejection(t *testing.T) {
	got, err := bashPPGoIdentity()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*bashPPGoIdentityInfo)
		match  string
	}{
		{"version", func(v *bashPPGoIdentityInfo) { v.Version = "go1.27.1" }, "not reviewed"},
		{"hash", func(v *bashPPGoIdentityInfo) { v.SHA256 = "0" + v.SHA256[1:] }, "checksum"},
		{"path", func(v *bashPPGoIdentityInfo) { v.Binary += "x" }, "binary path"},
	} {
		t.Run(test.name, func(t *testing.T) {
			mutant := got
			test.mutate(&mutant)
			if err := validateBashPPGoIdentity(mutant, bashPPGoReviews); err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("mutation accepted: %v", err)
			}
		})
	}
}

func TestBashPPEvalRequestPinsReviewedGoEnvironment(t *testing.T) {
	r, err := New(Env(expand.ListEnviron("GOROOT=/wrong", "GOTOOLCHAIN=go1.26.5")), Lang(syntax.LangBashPP))
	if err != nil {
		t.Fatal(err)
	}
	r.Reset()
	r.bashPPTools = bashPPToolchain{goBinary: "/reviewed/go", goRoot: "/reviewed", goVersion: "go1.27.0", eval: &recordingBashPPEval{}}
	req, err := r.bashPPEvalRequest()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"GOROOT=/reviewed": false, "GOTOOLCHAIN=go1.27.0": false}
	for _, entry := range req.Env {
		if _, ok := want[entry]; ok {
			want[entry] = true
		}
		if entry == "GOROOT=/wrong" || entry == "GOTOOLCHAIN=go1.26.5" {
			t.Fatalf("unreviewed environment retained: %q", entry)
		}
	}
	for entry, seen := range want {
		if !seen {
			t.Errorf("missing %q", entry)
		}
	}
}

func TestNativeBashPPSelectorExitStatusIsExact(t *testing.T) {
	r := newInjectedBashPPRunner(t, nativeBashPPEvaluator{})
	r.bashPPTools.goBinary = ""
	err := r.Run(context.Background(), parseBashPPInternal(t, "import \"os\"\nos.Exit(7)\n"))
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("error/status = %T %v", err, err)
	}
}
