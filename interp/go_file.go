package interp

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// RunCompiledGoFile runs a whole Go program with the toolchain selected for
// Bash# Go fences. Building first preserves the program's exit status (go run
// itself returns status 1 for any nonzero program exit).
func RunCompiledGoFile(path string, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	identity, err := bashPPGoIdentity()
	if err != nil {
		return 2, err
	}
	root, err := os.MkdirTemp("", "bashsharp-go-*")
	if err != nil {
		return 2, err
	}
	defer os.RemoveAll(root)
	source, err := filepath.Abs(path)
	if err != nil {
		return 2, err
	}
	if strings.ToLower(filepath.Ext(source)) != ".go" {
		original, openErr := os.Open(source)
		if openErr != nil {
			return 2, openErr
		}
		defer original.Close()
		source = filepath.Join(root, "source.go")
		copyFile, createErr := os.OpenFile(source, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
		if createErr != nil {
			return 2, createErr
		}
		if _, err = io.Copy(copyFile, original); err != nil {
			copyFile.Close()
			return 2, err
		}
		if err = copyFile.Close(); err != nil {
			return 2, err
		}
	}
	program := filepath.Join(root, "program")
	build := exec.Command(identity.Binary, "build", "-o", program, source)
	build.Stdin, build.Stdout, build.Stderr = stdin, stdout, stderr
	if err = build.Run(); err != nil {
		return processStatus(err)
	}
	run := exec.Command(program, args...)
	run.Stdin, run.Stdout, run.Stderr = stdin, stdout, stderr
	if err = run.Run(); err != nil {
		return processStatus(err)
	}
	return 0, nil
}

func processStatus(err error) (int, error) {
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), nil
	}
	return 2, fmt.Errorf("run Go program: %w", err)
}
