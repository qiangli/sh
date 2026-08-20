// Copyright (c) 2017, Daniel Martí
// See LICENSE for licensing information

//go:build linux

package interp_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func TestDefaultExecHandlerResolvesRelativeProgramsFromRunnerDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	working := filepath.Join(root, "working")
	if err := os.Mkdir(working, 0o755); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(root, "relative-program")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf 'ran-relative\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plainProgram := filepath.Join(root, "plain-program")
	if err := os.WriteFile(plainProgram, []byte("printf 'ran-plain\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	binaryProgram := filepath.Join(root, "binary-program")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, binaryProgram); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		src  string
		want string
	}{
		{name: "explicit-path", src: "command ../relative-program"},
		{name: "relative-PATH-entry", src: "PATH=.. command relative-program"},
		{name: "no-shebang", src: "GOSH_PROG=1 GOSH_CMD=relative_exec_script command ../plain-program"},
		{
			name: "binary-argv0-and-underscore",
			src:  "GOSH_PROG=1 GOSH_CMD=relative_exec_identity command ../binary-program",
			want: "argv0=../binary-program\nunderscore=../binary-program\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			runner, err := interp.New(
				interp.Dir(working),
				interp.StdIO(nil, &output, &output),
				interp.Params("-o", "posix"),
			)
			if err != nil {
				t.Fatal(err)
			}
			file, err := syntax.NewParser().Parse(strings.NewReader(tc.src), tc.name)
			if err != nil {
				t.Fatal(err)
			}
			if err := runner.Run(context.Background(), file); err != nil {
				t.Fatalf("run: %v; output=%q", err, output.String())
			}
			want := tc.want
			if want == "" {
				if tc.name == "no-shebang" {
					want = "ran-plain\n"
				} else {
					want = "ran-relative\n"
				}
			}
			if got := output.String(); got != want {
				t.Fatalf("output=%q, want %q", got, want)
			}
		})
	}
}
