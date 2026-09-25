//go:build full

package interp_test

// Sprint: #281; Story: #811; Story-ID: aa5c046bb543

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/gosource"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

const s281ReexecChild = "BASHPP_S281_REEXEC_CHILD"

const s281ReexecSource = `package main
import (
	"fmt"
	"os"
	"os/exec"
)
func main() {
	if os.Getenv("BASHPP_S281_REEXEC_CHILD") == "1" {
		fmt.Println("interpreted-child", os.Args[1])
		return
	}
	executable, err := os.Executable()
	if err != nil { panic(err) }
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

func runS281ReexecProgram(t *testing.T, stdout io.Writer, args, plan []string) string {
	t.Helper()
	program, err := gosource.Parse(strings.NewReader(s281ReexecSource), "reexec.go", gosource.Options{RunMain: true})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if stdout == nil {
		stdout = &output
	}
	options := []interp.RunnerOption{
		interp.Lang(syntax.LangBashPP),
		interp.Dir(t.TempDir()),
		interp.Env(nil),
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
