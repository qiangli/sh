package interp

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunCompiledGoFile(t *testing.T) {
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain unavailable")
	}
	t.Setenv("BASHPP_GO", goBinary)

	dir := t.TempDir()
	mainFile := filepath.Join(dir, "main.go")
	mainSource := `package main
import (
	"bufio"
	"fmt"
	"os"
)
func main() {
	in, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	fmt.Fprintf(os.Stdout, "stdout:%s:%s", os.Args[1], in)
	fmt.Fprintln(os.Stderr, "stderr")
	os.Exit(23)
}
`
	if err := os.WriteFile(mainFile, []byte(mainSource), 0o600); err != nil {
		t.Fatal(err)
	}

	previousCommand := runCompiledGoCommand
	var commands []*exec.Cmd
	runCompiledGoCommand = func(name string, args ...string) *exec.Cmd {
		cmd := exec.Command(name, args...)
		commands = append(commands, cmd)
		return cmd
	}
	t.Cleanup(func() { runCompiledGoCommand = previousCommand })

	stdin := strings.NewReader("input\n")
	var stdout, stderr bytes.Buffer
	status, err := RunCompiledGoFile(mainFile, []string{"arg"}, stdin, &stdout, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if status != 23 || stdout.String() != "stdout:arg:input\n" || stderr.String() != "stderr\n" {
		t.Fatalf("status=%d stdout=%q stderr=%q", status, stdout.String(), stderr.String())
	}
	if len(commands) != 2 {
		t.Fatalf("commands=%d, want build and run", len(commands))
	}
	if commands[0].Stdin != nil {
		t.Fatalf("go build stdin = %T, want nil", commands[0].Stdin)
	}
	if commands[1].Stdin != stdin {
		t.Fatalf("program stdin = %T, want caller stdin", commands[1].Stdin)
	}

	libraryFile := filepath.Join(dir, "library.go")
	if err := os.WriteFile(libraryFile, []byte("// heading\npackage library\n\nfunc Exported() int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	commands = nil
	status, err = RunCompiledGoFile(libraryFile, nil, nil, &stdout, &stderr)
	if status != 2 || err == nil {
		t.Fatalf("status=%d err=%v, want positioned refusal", status, err)
	}
	message := err.Error()
	position := libraryFile + ":2:1:"
	if runtime.GOOS == "windows" {
		position = libraryFile + ":2:1:"
	}
	if !strings.Contains(message, position) || !strings.Contains(message, "~~~go") || !strings.Contains(message, "exported") {
		t.Fatalf("refusal %q lacks position, fence workaround, or export guidance", message)
	}
	if len(commands) != 0 {
		t.Fatalf("non-main package ran %d commands", len(commands))
	}
}
