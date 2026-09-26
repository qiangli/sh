//go:build full

package interp_test

// Sprint: #281; Story: #811; Story-ID: aa5c046bb543

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/lower"
	"mvdan.cc/sh/v3/syntax"
)

const s281ReexecChild = "BASHPP_S281_REEXEC_CHILD"
const s281ReplacementCompile = "BASHPP_S281_REPLACEMENT_COMPILE"
const s281ReplacementMarker = "BASHPP_S281_REPLACEMENT_MARKER"

const s281ReexecSource = `package main
import (
	"fmt"
	"os"
	"os/exec"
)
func main() {
	if os.Getenv("BASHPP_S281_REEXEC_CHILD") == "1" {
		fmt.Println("interpreted-child", os.Args[1])
		fmt.Println("child-goroot", os.Getenv("GOROOT"))
		return
	}
	executable, err := os.Executable()
	if err != nil { panic(err) }
	if replacement := os.Getenv("BASHPP_S281_REPLACEMENT_COMPILE"); replacement != "" {
		script := "#!/bin/sh\nprintf invoked > \"$BASHPP_S281_REPLACEMENT_MARKER\"\necho replacement-compiler-invoked >&2\nexit 86\n"
		if err := os.WriteFile(replacement, []byte(script), 0755); err != nil { panic(err) }
	}
	os.Unsetenv("GOSH_PROG")
	cmd := exec.Command(executable, "payload")
	cmd.Env = append(os.Environ(), "BASHPP_S281_REEXEC_CHILD=1")
	out, err := cmd.CombinedOutput()
	if err != nil { panic(fmt.Sprintf("child: %v: %s", err, out)) }
	fmt.Print(string(out))
}
`

const s281ReexecVersionFanoutSource = `package main
import (
	"os"
	"os/exec"
)
func main() {
	executable, err := os.Executable()
	if err != nil { panic(err) }
	os.Unsetenv("GOSH_PROG")
	cmd := exec.Command(executable, "__fanout__")
	cmd.Env = append(os.Environ(), "BASHPP_S281_VERSION_PROBE_LAUNCHER=" + executable)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil { panic(err) }
}
`

const s281ReexecArgv0Source = `package main
import (
	"fmt"
	"os"
	"path/filepath"
)
func main() {
	name := filepath.Base(os.Args[0])
	if len(os.Args) == 2 && os.Args[1] == "payload" {
		fmt.Println("replayed-argv0", name)
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "-V=full" {
		fmt.Println(name, "version go1.27.1")
		return
	}
	launcher, err := os.Executable()
	if err != nil { panic(err) }
	renamed := os.Getenv("BASHPP_S281_RENAMED_TOOL")
	data, err := os.ReadFile(launcher)
	if err != nil { panic(err) }
	if err := os.WriteFile(renamed, data, 0755); err != nil { panic(err) }
	fmt.Println("copied-launcher", renamed)
}
`

const s281Argv0Source = `package main
import (
	"fmt"
	"os"
)
func main() { fmt.Println(os.Args[0]) }
`

func TestGoSourceS281SelfReexecLauncher(t *testing.T) {
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) >= 2 && args[1] == "payload" {
		if got := os.Getenv(s281ReexecChild); got != "1" {
			t.Fatalf("reexec child environment %s=%q, want 1", s281ReexecChild, got)
		}
		runS281ReexecProgram(t, os.Stdout, args[1:], nil)
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got := runS281ReexecProgram(t, nil, nil, []string{self, "-test.run=^TestGoSourceS281SelfReexecLauncher$", "--"})
	if !strings.Contains(got, "interpreted-child payload\n") {
		t.Fatalf("reexec output = %q, want interpreted child marker", got)
	}
}

// TestGoSourceS281SelfReexecLauncherArgv0 copies the generated launcher to the
// name of a replacement tool. Both a normal child replay and the Go command's
// -V=full probe must observe that invoked name, not the reconstructed source
// filename or the host test binary.
func TestGoSourceS281SelfReexecLauncherArgv0(t *testing.T) {
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) == 2 && (args[1] == "payload" || args[1] == "-V=full") {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		plan := []string{self, "-test.run=^TestGoSourceS281SelfReexecLauncherArgv0$", "--"}
		runS281ReexecSourceProgram(t, s281ReexecArgv0Source, os.Stdout, args[1:], plan)
		return
	}
	name := "renamed-compile-tool"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	renamed := filepath.Join(t.TempDir(), name)
	t.Setenv("BASHPP_S281_RENAMED_TOOL", renamed)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	launcherOutput := runS281ReexecSourceProgram(t, s281ReexecArgv0Source, nil, nil,
		[]string{self, "-test.run=^TestGoSourceS281SelfReexecLauncherArgv0$", "--"})
	if want := "copied-launcher " + renamed + "\n"; launcherOutput != want {
		t.Fatalf("launcher output = %q, want %q", launcherOutput, want)
	}
	command := exec.Command(renamed, "payload")
	command.Env = s281WithoutEnv(os.Environ(), "GOSH_PROG")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("replay: %v: %s", err, output)
	}
	if want := "replayed-argv0 " + name + "\n"; !strings.Contains(string(output), want) {
		t.Fatalf("reexec output = %q, want %q", output, want)
	}
	command = exec.Command(renamed, "-V=full")
	command.Env = s281WithoutEnv(os.Environ(), "GOSH_PROG")
	output, err = command.CombinedOutput()
	if err != nil {
		t.Fatalf("version probe: %v: %s", err, output)
	}
	if want := name + " version go1.27.1\n"; !strings.Contains(string(output), want) {
		t.Fatalf("version probe output = %q, want %q", output, want)
	}
}

func s281WithoutEnv(env []string, name string) []string {
	prefix := name + "="
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return out
}

func TestGoSourceS281ReexecArgv0Selection(t *testing.T) {
	t.Setenv("BASHPP_REEXEC_ARGV0", "process-global-replay")
	withoutReplay := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "BASHPP_REEXEC_ARGV0=") {
			withoutReplay = append(withoutReplay, entry)
		}
	}
	withReplay := append(append([]string(nil), withoutReplay...), "BASHPP_REEXEC_ARGV0=runner-replay")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	plan := []string{self, "-test.run=^$", "--"}
	for _, tc := range []struct {
		name string
		plan []string
		opts []interp.RunnerOption
		want string
	}{
		{name: "default source filename", want: "reexec.go"},
		{name: "explicit argv0", opts: []interp.RunnerOption{interp.WithArgv0("configured-tool")}, want: "configured-tool"},
		{name: "process environment does not leak", plan: plan, opts: []interp.RunnerOption{interp.Env(expand.ListEnviron(withoutReplay...)), interp.WithArgv0("configured-tool")}, want: "configured-tool"},
		{name: "runner replay overrides configured argv0", plan: plan, opts: []interp.RunnerOption{interp.Env(expand.ListEnviron(withReplay...)), interp.WithArgv0("configured-tool")}, want: "runner-replay"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runS281ReexecSourceProgram(t, s281Argv0Source, nil, nil, tc.plan, tc.opts...)
			if got != tc.want+"\n" {
				t.Fatalf("os.Args[0] = %q, want %q", strings.TrimSpace(got), tc.want)
			}
		})
	}
}

// TestGoSourceS281SelfReexecReplacementToolIsolation recreates TestMain's
// replacement-tool ordering without running cmd/compile's large script suite:
// the parent first obtains its replay launcher, then overwrites compile in the
// selected GOROOT, and only then starts the interpreted child. The child must
// build its dependency helper with the independent authenticated SDK, while
// retaining the selected GOROOT in the interpreted process environment.
func TestGoSourceS281SelfReexecReplacementToolIsolation(t *testing.T) {
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) >= 2 && args[1] == "payload" {
		if got := os.Getenv(s281ReexecChild); got != "1" {
			t.Fatalf("reexec child environment %s=%q, want 1", s281ReexecChild, got)
		}
		runS281ReexecProgram(t, os.Stdout, args[1:], nil)
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("the focused replacement is a POSIX executable script")
	}
	goBinary, compileTool := s281ReplacementGOROOT(t)
	fakeRoot := filepath.Dir(filepath.Dir(goBinary))
	marker := filepath.Join(t.TempDir(), "replacement-invoked")
	t.Setenv("BASHPP_GO", goBinary)
	t.Setenv("GOROOT", fakeRoot)
	t.Setenv(s281ReplacementCompile, compileTool)
	t.Setenv(s281ReplacementMarker, marker)

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got := runS281ReexecProgram(t, nil, nil, []string{self, "-test.run=^TestGoSourceS281SelfReexecReplacementToolIsolation$", "--"})
	if !strings.Contains(got, "interpreted-child payload\n") {
		t.Fatalf("reexec output = %q, want interpreted child marker", got)
	}
	if !strings.Contains(got, "child-goroot "+fakeRoot+"\n") {
		t.Fatalf("reexec output = %q, want child GOROOT %q", got, fakeRoot)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("replacement compiler was invoked; marker stat: %v", err)
	}
}

// TestGoSourceS281SelfReexecVersionProbeSingleflight models the Go command's
// toolID probe. Each Go command caches -V=full only in-process, so parallel
// script tests otherwise replay the entire interpreted replacement compiler
// once per command. The replay launcher must obtain the real replay result,
// but share that stable tool identity across its own processes.
func TestGoSourceS281SelfReexecVersionProbeSingleflight(t *testing.T) {
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) == 2 && args[1] == "-V=full" {
		marker := os.Getenv("BASHPP_S281_VERSION_PROBE_MARKER")
		file, err := os.OpenFile(marker, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("x"); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		time.Sleep(250 * time.Millisecond)
		version := "compile version go1.27.1"
		if experiment := os.Getenv("GOEXPERIMENT"); experiment != "" {
			version += " X:" + experiment
		}
		_, _ = io.WriteString(os.Stdout, version+"\n")
		return
	}
	if len(args) == 2 && args[1] == "__fanout__" {
		launcher := os.Getenv("BASHPP_S281_VERSION_PROBE_LAUNCHER")
		if err := os.Unsetenv("GOSH_PROG"); err != nil {
			t.Fatal(err)
		}
		const processes = 16
		outputs := make(chan []byte, processes)
		errs := make(chan error, processes)
		var group sync.WaitGroup
		for range processes {
			group.Add(1)
			go func() {
				defer group.Done()
				output, err := exec.Command(launcher, "-V=full").CombinedOutput()
				if err != nil {
					errs <- fmt.Errorf("version probe: %w: %s", err, output)
					return
				}
				outputs <- output
			}()
		}
		group.Wait()
		close(outputs)
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		for output := range outputs {
			_, _ = os.Stdout.Write(output)
		}
		if err := os.Setenv("GOEXPERIMENT", "cachekey"); err != nil {
			t.Fatal(err)
		}
		output, err := exec.Command(launcher, "-V=full").CombinedOutput()
		if err != nil {
			t.Fatalf("experiment version probe: %v: %s", err, output)
		}
		_, _ = os.Stdout.Write(output)
		return
	}

	marker := filepath.Join(t.TempDir(), "version-probes")
	t.Setenv("BASHPP_S281_VERSION_PROBE_MARKER", marker)
	t.Setenv("GOEXPERIMENT", "")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	output := runS281ReexecSourceProgram(t, s281ReexecVersionFanoutSource, nil, nil,
		[]string{self, "-test.run=^TestGoSourceS281SelfReexecVersionProbeSingleflight$", "--"})
	if got, want := strings.Count(output, "compile version go1.27.1\n"), 16; got != want {
		t.Errorf("version output count = %d, want %d; output=%q", got, want, output)
	}
	if got, want := strings.Count(output, "compile version go1.27.1 X:cachekey\n"), 1; got != want {
		t.Errorf("experiment version output count = %d, want %d; output=%q", got, want, output)
	}
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(data); got != 2 {
		t.Fatalf("replayed version probes = %d, want 2 configurations", got)
	}
}

// s281ReexecForwardSource copies its generated launcher to a caller-named path
// so a native test can drive the launcher directly and observe how it treats
// its replayed child when the launcher process is signalled.
const s281ReexecForwardSource = `package main
import (
	"os"
)
func main() {
	launcher, err := os.Executable()
	if err != nil { panic(err) }
	data, err := os.ReadFile(launcher)
	if err != nil { panic(err) }
	renamed := os.Getenv("BASHPP_S281_RENAMED_TOOL")
	if err := os.WriteFile(renamed, data, 0755); err != nil { panic(err) }
}
`

// TestGoSourceS281SelfReexecForwardsInterrupt drives the generated launcher
// directly and measures its lifecycle toward its direct replayed child. The
// launcher installs a signal handler and waits for the child, so a SIGINT or
// SIGTERM delivered to the launcher process alone (not its process group) is
// forwarded to the child, which exits rather than continuing to run after the
// launcher returns. Both the ordinary replay path and the -V=full version-probe
// path route through the same wait helper, so both are exercised.
//
// This asserts only the launcher's direct-child behavior; it establishes
// nothing about any further interpreter-helper grandchildren. The baseline
// launcher (no forwarding) exits on the signal and leaves the child running,
// which the interrupted-marker wait detects as a bounded failure.
func TestGoSourceS281SelfReexecForwardsInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the focused child is a POSIX shell that traps INT/TERM")
	}
	for _, tc := range []struct {
		name   string
		args   []string
		signal syscall.Signal
	}{
		{name: "run/SIGINT", signal: syscall.SIGINT},
		{name: "run/SIGTERM", signal: syscall.SIGTERM},
		{name: "version-probe/SIGINT", args: []string{"-V=full"}, signal: syscall.SIGINT},
		{name: "version-probe/SIGTERM", args: []string{"-V=full"}, signal: syscall.SIGTERM},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s281AssertLauncherForwards(t, tc.args, tc.signal)
		})
	}
}

func s281AssertLauncherForwards(t *testing.T, args []string, sig syscall.Signal) {
	t.Helper()
	dir := t.TempDir()
	renamed := filepath.Join(dir, "reexec-launcher")
	t.Setenv("BASHPP_S281_RENAMED_TOOL", renamed)

	started := filepath.Join(dir, "child-started")
	interrupted := filepath.Join(dir, "child-interrupted")
	// The child installs its trap, records that it started, then loops sleeping.
	// On INT/TERM it records the signal and exits 0. Without forwarding the child
	// never receives the signal and the interrupted marker never appears.
	script := `trap 'printf x > "$MARK_INT"; exit 0' INT TERM
printf x > "$MARK_STARTED"
n=0
while [ "$n" -lt 300 ]; do sleep 0.1; n=$((n+1)); done`
	plan := []string{"/bin/sh", "-c", script}

	if got := runS281ReexecSourceProgram(t, s281ReexecForwardSource, nil, nil, plan); got != "" {
		t.Fatalf("launcher build output = %q, want empty", got)
	}

	cmd := exec.Command(renamed, args...)
	cmd.Env = append(s281WithoutEnv(os.Environ(), "GOSH_PROG"),
		"MARK_STARTED="+started, "MARK_INT="+interrupted)
	// Put the launcher in its own process group so cleanup can terminate the
	// entire fixture — the launcher, the replayed shell child and its sleeps —
	// even in the baseline where nothing is forwarded and the child would
	// otherwise be orphaned and outlive the test.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start launcher: %v", err)
	}
	pgid := cmd.Process.Pid
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	t.Cleanup(func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })

	// Signal only once the child is running: by then wait() has installed the
	// launcher's handler and the child has installed its trap. Signalling earlier
	// would hit the launcher's default signal disposition instead of forwarding.
	s281WaitForFile(t, started, 30*time.Second)
	if err := cmd.Process.Signal(sig); err != nil {
		t.Fatalf("signal launcher: %v", err)
	}
	// The launcher must exit within a bounded window after being signalled.
	select {
	case <-waited:
	case <-time.After(30 * time.Second):
		t.Fatalf("launcher did not exit within 30s after %v", sig)
	}
	// Distinguishing assertion: with forwarding the child observes the signal and
	// writes the marker; the baseline launcher exits without forwarding and this
	// bounded wait fails.
	s281WaitForFile(t, interrupted, 10*time.Second)
}

// s281WaitForFile blocks until path exists or the bounded timeout elapses,
// failing the test on timeout so a missing marker is a distinguishing failure
// rather than a silent pass.
func s281WaitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, path)
}

func s281ReplacementGOROOT(t *testing.T) (goBinary, compileTool string) {
	t.Helper()
	realRoot := runtime.GOROOT()
	fakeRoot := t.TempDir()
	for _, name := range []string{"api", "doc", "lib", "misc", "src", "test", "VERSION", "go.env"} {
		source := filepath.Join(realRoot, name)
		if _, err := os.Stat(source); err == nil {
			if err := os.Symlink(source, filepath.Join(fakeRoot, name)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(fakeRoot, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	goBinary = filepath.Join(fakeRoot, "bin", "go")
	s281CopyExecutable(t, filepath.Join(realRoot, "bin", "go"), goBinary)

	toolDir := filepath.Join("pkg", "tool", runtime.GOOS+"_"+runtime.GOARCH)
	if err := os.MkdirAll(filepath.Join(fakeRoot, toolDir), 0755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(realRoot, toolDir))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		source := filepath.Join(realRoot, toolDir, entry.Name())
		target := filepath.Join(fakeRoot, toolDir, entry.Name())
		if entry.Name() == "compile" {
			s281CopyExecutable(t, source, target)
			compileTool = target
		} else if err := os.Symlink(source, target); err != nil {
			t.Fatal(err)
		}
	}
	if compileTool == "" {
		t.Fatal("selected GOROOT has no compile tool")
	}
	return goBinary, compileTool
}

func s281CopyExecutable(t *testing.T, source, target string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0755); err != nil {
		t.Fatal(err)
	}
}

func runS281ReexecProgram(t *testing.T, stdout io.Writer, args, plan []string) string {
	return runS281ReexecSourceProgram(t, s281ReexecSource, stdout, args, plan)
}

func runS281ReexecSourceProgram(t *testing.T, source string, stdout io.Writer, args, plan []string, extra ...interp.RunnerOption) string {
	t.Helper()
	dir := t.TempDir()
	program, err := gosource.Parse(strings.NewReader(source), "reexec.go", gosource.Options{RunMain: true, Importer: lower.NewModuleImporter(dir)})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if stdout == nil {
		stdout = &output
	}
	options := []interp.RunnerOption{
		interp.Lang(syntax.LangBashPP),
		interp.Dir(dir),
		interp.Env(expand.ListEnviron(os.Environ()...)),
		interp.GoSourceEnv(os.Environ()),
		interp.StdIO(nil, stdout, stdout),
		interp.Params(append([]string{"--"}, args...)...),
	}
	if plan != nil {
		options = append(options, interp.GoSourceReexecPlan(plan...))
	}
	options = append(options, extra...)
	runner, err := interp.New(options...)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := runner.Run(ctx, program.File); err != nil {
		t.Fatalf("Run: %v; output=%q", err, output.String())
	}
	return output.String()
}
