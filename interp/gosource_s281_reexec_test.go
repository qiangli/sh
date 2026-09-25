//go:build full

package interp_test

// Sprint: #281; Story: #811; Story-ID: aa5c046bb543

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	t.Helper()
	dir := t.TempDir()
	program, err := gosource.Parse(strings.NewReader(s281ReexecSource), "reexec.go", gosource.Options{RunMain: true, Importer: lower.NewModuleImporter(dir)})
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
