package interp

// Sprint: #281; Story: #811; Story-ID: aa5c046bb543

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// GoSourceReexecPlan supplies the exact host argv prefix which reconstructs
// the current interpreted Go-source program. When that program calls
// os.Executable, the returned path names a temporary native launcher. Running
// the launcher executes plan followed by the launcher's arguments after
// argv[0], while inheriting the child's cwd, environment, and standard files.
//
// The host owns the plan's meaning. In particular, a CLI host should include
// every source, mapped package, identity, test-main fact, and companion input,
// and end the plan with its program-argument separator. This package neither
// discovers inputs nor falls back to executing a native version of the
// interpreted program. The launcher preserves the child's GOROOT and other
// program environment, but pins the private BASHPP_GO selector to the
// authenticated SDK used to build interpreter helpers. Thus a test may replace
// a tool in its GOROOT without making the replaying front end compile itself.
// Without this option, os.Executable retains its former dependency-bridge
// behavior.
func GoSourceReexecPlan(plan ...string) RunnerOption {
	copyPlan := append([]string(nil), plan...)
	return func(r *Runner) error {
		if len(copyPlan) == 0 {
			return fmt.Errorf("gosource: reexec plan requires an executable")
		}
		if !filepath.IsAbs(copyPlan[0]) {
			return fmt.Errorf("gosource: reexec plan executable must be absolute: %s", copyPlan[0])
		}
		r.bashPPTools.reexecPlan = append([]string(nil), copyPlan...)
		return nil
	}
}

func (r *Runner) goSourceExecutableCall(ctx context.Context, call *syntax.BashPPCall) ([]bashPPBridgeValue, bool, error) {
	if !r.bashPPGoSource || len(r.bashPPTools.reexecPlan) == 0 || call == nil || call.Ellipsis.IsValid() || len(call.Args) != 0 || len(call.ArgExprs) != 0 {
		return nil, false, nil
	}
	if selector, ok := r.goSourceImportedCallee(call); !ok || selector != "os.Executable" {
		return nil, false, nil
	}
	session := r.bashPPTools.bridge
	if session == nil {
		return nil, true, fmt.Errorf("gosource: reexec launcher requires an active dependency bridge")
	}
	session.mu.Lock()
	connected := session.conn != nil
	session.mu.Unlock()
	if !connected {
		return nil, true, fmt.Errorf("gosource: reexec launcher requires an active dependency bridge")
	}
	req, err := r.bashPPEvalRequest()
	if err != nil {
		return nil, true, err
	}
	launcher, err := session.goSourceReexecLauncher(ctx, req, r.bashPPTools.reexecPlan, r.tempDir)
	if err != nil {
		return nil, true, err
	}
	return []bashPPBridgeValue{
		{Kind: "string", Type: "string", NativeType: "string", Text: launcher},
		{Kind: "nil"},
	}, true, nil
}

func (s *bashPPNativeSession) goSourceReexecLauncher(ctx context.Context, req bashPPEvalRequest, plan []string, tempDir string) (string, error) {
	s.reexecMu.Lock()
	defer s.reexecMu.Unlock()
	if s.reexecLauncher != "" {
		return s.reexecLauncher, nil
	}
	if s.reexecDir == "" {
		dir, err := os.MkdirTemp(tempDir, ".bashpp-reexec-")
		if err != nil {
			return "", fmt.Errorf("gosource: create reexec launcher workspace: %w", err)
		}
		s.reexecDir = dir
	}
	var quoted []string
	for _, arg := range plan {
		quoted = append(quoted, strconv.Quote(arg))
	}
	quotedBuildGo := strconv.Quote(req.internalBuildGo())
	source := `package main
import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)
func main() {
	plan := []string{` + strings.Join(quoted, ",") + `}
	args := append(append([]string(nil), plan[1:]...), os.Args[1:]...)
	cmd := exec.Command(plan[0], args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "BASHPP_GO") { cmd.Env = append(cmd.Env, entry) }
	}
	cmd.Env = append(cmd.Env, "BASHPP_GO=" + ` + quotedBuildGo + `)
	err := cmd.Run()
	if err == nil { return }
	if exit, ok := err.(*exec.ExitError); ok { os.Exit(exit.ExitCode()) }
	fmt.Fprintf(os.Stderr, "gosource reexec %q: %v\n", plan, err)
	os.Exit(127)
}
`
	sourcePath := filepath.Join(s.reexecDir, "bashpp-reexec-launcher.go")
	if err := os.WriteFile(sourcePath, []byte(source), 0o600); err != nil {
		return "", fmt.Errorf("gosource: write reexec launcher: %w", err)
	}
	launcher := filepath.Join(s.reexecDir, "bashpp-reexec-launcher")
	if executableSuffix := goSourceExecutableSuffix(); executableSuffix != "" {
		launcher += executableSuffix
	}
	cmd := exec.CommandContext(ctx, req.internalBuildGo(), "build", "-p", "2", "-o", launcher, sourcePath)
	cmd.Dir = s.reexecDir
	cmd.Env = setEnvString(req.internalBuildEnv(), "CGO_ENABLED", "0")
	var diagnostics bytes.Buffer
	cmd.Stdout, cmd.Stderr = &diagnostics, &diagnostics
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gosource: build reexec launcher: %w: %s", err, diagnostics.String())
	}
	s.reexecLauncher = launcher
	return launcher, nil
}

func (r *Runner) goSourceImportedCallee(call *syntax.BashPPCall) (string, bool) {
	alias, name := "", ""
	if selector, ok := call.CalleeExpr.(*syntax.BashPPSelectorExpr); ok {
		id, ok := selector.X.(*syntax.BashPPIdent)
		if !ok {
			return "", false
		}
		alias, name = id.Name.Value, selector.Sel.Value
	} else if len(call.Fun) == 2 {
		alias, name = call.Fun[0].Value, call.Fun[1].Value
	} else {
		return "", false
	}
	if r.bashPPScope != nil && r.bashPPScope.lookup(alias) != nil {
		return "", false
	}
	path, ok := r.bashPPImports[alias]
	if !ok {
		return "", false
	}
	return path + "." + name, true
}
