package interp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

// POSIX.1-2024 trap uses the environment after the last command. An async
// simple command's own redirections have ended by then. Bash compatibility
// deliberately retains its different /dev/null behavior for that case.
func TestStrictPosixAsyncExitTrapRedirection(t *testing.T) {
	cases := []struct{ name, script, strictOut, bashOut, fileOut string }{
		{"async-simple", `trap 'printf child' EXIT >NULL & wait $!`, "child", "", ""},
		{"foreground-simple", `trap 'printf final' EXIT >NULL`, "final", "final", ""},
		{"foreground-function", `f() { trap 'printf final' EXIT; }; f >NULL`, "final", "final", ""},
		{"subshell-outer-redirection", `(trap 'printf inner' EXIT) >NULL`, "", "", ""},
		{"async-subshell-outer-redirection", `(trap 'printf inner' EXIT) >NULL & wait $!`, "", "", ""},
		{"nested-trap", `trap 'v=$(trap "printf inner" EXIT); printf "<%s>" "$v"' EXIT >NULL & wait $!`, "<inner>", "", ""},
		{"inherited-reset", `trap 'printf parent' EXIT; trap - EXIT >NULL & wait $!`, "parent", "parent", ""},
		{"parent-output", `trap 'printf child' EXIT >NULL & wait $!; printf parent`, "childparent", "parent", ""},
		{"persistent-exec-redirection", `{ exec >out; trap 'printf file' EXIT; } & wait $!`, "", "", "file"},
	}
	for _, tc := range cases {
		for _, strict := range []bool{false, true} {
			profile := "bash-posix"
			if strict {
				profile = "strict-posix"
			}
			t.Run(tc.name+"/"+profile, func(t *testing.T) {
				src := "set -o posix\n" + strings.ReplaceAll(tc.script, "NULL", os.DevNull)
				f, err := syntax.NewParser().Parse(strings.NewReader(src), "async-exit")
				if err != nil {
					t.Fatal(err)
				}
				var stdout, stderr syncBuffer // background jobs and post-run snapshots use the same mutex
				dir := t.TempDir()
				r, err := New(Dir(dir), StdIO(nil, &stdout, &stderr), WithStrictPosix(strict))
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err = r.Run(ctx, f); err != nil {
					t.Fatalf("run:%v stderr:%s", err, &stderr)
				}
				want := tc.bashOut
				if strict {
					want = tc.strictOut
				}
				if stdout.String() != want || stderr.String() != "" {
					t.Fatalf("stdout=%q want=%q stderr=%q", stdout.String(), want, stderr.String())
				}
				if tc.fileOut != "" {
					data, err := os.ReadFile(filepath.Join(dir, "out"))
					if err != nil || string(data) != tc.fileOut {
						t.Fatalf("redirected output=%q want=%q error=%v", data, tc.fileOut, err)
					}
				}
			})
		}
	}
}
