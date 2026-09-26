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
// Concurrent Go tool identity probes (-V=full) are single-flighted and their
// successful output is cached for the launcher's lifetime and relevant build
// configuration, rather than starting one interpreter per probing go command.
// The launcher also forwards os.Interrupt and SIGTERM to its replayed child and
// waits for it, so signalling the launcher process terminates the replayed
// program rather than leaving it running after the launcher exits.
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

// reexecArgv0Env carries the executable identity (argv[0]) a reexec launcher was
// invoked under, so a replayed program keeps that identity across interpreter
// re-entry instead of falling back to the reconstructed source name.
const reexecArgv0Env = "BASHPP_REEXEC_ARGV0"

// goSourceReexecArgv0 reports the argv[0] the Go-source program should observe.
// A reexec launcher supplies its invoked identity through the Runner's own
// environment; consulting the process environment here would leak one replay's
// identity into unrelated Runners hosted by the same process. An explicitly
// configured argv0 remains authoritative for ordinary runs, while the historic
// default remains the source filename.
func (r *Runner) goSourceReexecArgv0() string {
	if len(r.bashPPTools.reexecPlan) != 0 {
		if vr := r.lookupVar(reexecArgv0Env); vr.IsSet() && vr.String() != "" {
			return vr.String()
		}
	}
	if r.origArgv0 != "" {
		return r.origArgv0
	}
	return r.filename
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
	quotedVersionCache := strconv.Quote(filepath.Join(s.reexecDir, "tool-version"))
	source := `package main
import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)
func main() {
	plan := []string{` + strings.Join(quoted, ",") + `}
	if len(os.Args) == 2 && os.Args[1] == "-V=full" {
		os.Exit(versionProbe(plan, ` + quotedVersionCache + `))
	}
	os.Exit(run(plan, os.Stdin, os.Stdout))
}
func command(plan []string) *exec.Cmd {
	args := append(append([]string(nil), plan[1:]...), os.Args[1:]...)
	cmd := exec.Command(plan[0], args...)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "BASHPP_GO") && !strings.EqualFold(name, "BASHPP_REEXEC_ARGV0") { cmd.Env = append(cmd.Env, entry) }
	}
	cmd.Env = append(cmd.Env, "BASHPP_GO=" + ` + quotedBuildGo + `)
	// Propagate the launcher's own invoked argv[0] so the replayed program keeps
	// the executable identity it was invoked under (e.g. go tool compile), rather
	// than the interpreter's reconstructed source name. See goSourceReexecArgv0.
	cmd.Env = append(cmd.Env, "BASHPP_REEXEC_ARGV0=" + os.Args[0])
	return cmd
}
func run(plan []string, stdin *os.File, stdout *os.File) int {
	cmd := command(plan)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, os.Stderr
	return wait(plan, cmd)
}
// wait runs cmd while forwarding os.Interrupt and SIGTERM to it, then waits for
// it to exit. Signalling the launcher process alone (not its group) would
// otherwise leave the replayed child running after the launcher returned;
// forwarding the signal and waiting terminates the direct child with the
// launcher.
func wait(plan []string, cmd *exec.Cmd) int {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "gosource reexec %q: %v\n", plan, err)
		return 127
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-signals:
				_ = cmd.Process.Signal(s)
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)
	if err == nil { return 0 }
	if exit, ok := err.(*exec.ExitError); ok { return exit.ExitCode() }
	fmt.Fprintf(os.Stderr, "gosource reexec %q: %v\n", plan, err)
	return 127
}
func versionProbe(plan []string, cache string) int {
	identity := filepath.Base(os.Args[0]) + "\x00" + os.Getenv("GOOS") + "\x00" + os.Getenv("GOARCH") + "\x00" + os.Getenv("GOEXPERIMENT")
	cache += fmt.Sprintf("-%x", sha256.Sum256([]byte(identity)))
	lock := cache + ".lock"
	for {
		if output, err := os.ReadFile(cache); err == nil {
			_, _ = os.Stdout.Write(output)
			return 0
		}
		file, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_ = file.Close()
			return populateVersionCache(plan, cache, lock)
		}
		if !errors.Is(err, os.ErrExist) {
			return run(plan, os.Stdin, os.Stdout)
		}
		if info, statErr := os.Stat(lock); statErr == nil && time.Since(info.ModTime()) > 30*time.Second {
			_ = os.Remove(lock)
			continue
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func populateVersionCache(plan []string, cache, lock string) int {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				_ = os.Chtimes(lock, now, now)
			case <-done:
				return
			}
		}
	}()
	defer func() { close(done); <-stopped; _ = os.Remove(lock) }()
	var output bytes.Buffer
	cmd := command(plan)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, &output, os.Stderr
	if code := wait(plan, cmd); code != 0 {
		return code
	}
	if temp, err := os.CreateTemp(filepath.Dir(cache), ".tool-version-"); err == nil {
		name := temp.Name()
		if _, err = temp.Write(output.Bytes()); err == nil { err = temp.Close() } else { _ = temp.Close() }
		if err == nil { err = os.Rename(name, cache) }
		if err != nil { _ = os.Remove(name) }
	}
	_, _ = os.Stdout.Write(output.Bytes())
	return 0
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
