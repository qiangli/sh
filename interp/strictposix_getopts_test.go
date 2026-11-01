package interp

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

// POSIX XBD 8.1 requires a built-in which rejects a readonly OPTARG/OPTIND
// modification to fail. getopts uses >1 for errors and 1 for end-of-options.
// The default profile intentionally preserves Bash's diagnostic-plus-success.
func TestStrictPosixGetoptsReadonly(t *testing.T) {
	cases := []struct {
		name, setup, command, diagnostic string
		strict, compat                   int
	}{
		{"argument", "readonly OPTARG", "getopts a: opt -a value", "OPTARG: readonly variable", 2, 0},
		{"empty_argument", "readonly OPTARG", "getopts a: opt -a ''", "OPTARG: readonly variable", 2, 0},
		{"argument_existing", "readonly OPTARG=keep", "getopts a: opt -a value", "OPTARG: readonly variable", 2, 0},
		{"index", "readonly OPTIND=1", "getopts a opt -a", "OPTIND: readonly variable", 2, 0},
		{"result", "readonly opt=keep", "getopts a opt -a", "opt: readonly variable", 2, 0},
		{"silent_unknown", "readonly OPTARG", "getopts :a opt -z", "OPTARG: readonly variable", 2, 0},
		{"silent_missing", "readonly OPTARG", "getopts :a: opt -a", "OPTARG: readonly variable", 2, 0},
		{"clear_argument", "readonly OPTARG=keep", "getopts a opt -a", "OPTARG: readonly variable", 2, 0},
		{"end_clear_argument", "readonly OPTARG=keep", "getopts a opt operand", "OPTARG: readonly variable", 2, 1},
		{"unused_readonly", "readonly OPTARG", "getopts a opt -a", "", 0, 0},
		{"ordinary_end", ":", "getopts a opt operand", "", 1, 1},
		{"previous_failure", "false", "getopts a opt -a", "", 0, 0},
		{"verbose_unknown", ":", "getopts a opt -z", "illegal option", 0, 0},
	}
	for _, tc := range cases {
		for _, strict := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/strict=%t", tc.name, strict), func(t *testing.T) {
				src := tc.setup + "\n" + tc.command + "\nprintf 'status=%s\\n' \"$?\"\n"
				file, err := syntax.NewParser().Parse(strings.NewReader(src), "")
				if err != nil {
					t.Fatal(err)
				}
				var out, diag bytes.Buffer
				r, err := New(WithPosixMode(true), WithStrictPosix(strict), StdIO(nil, &out, &diag))
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				if err := r.Run(ctx, file); err != nil {
					t.Fatalf("run: %v; %s", err, diag.String())
				}
				want := tc.compat
				if strict {
					want = tc.strict
				}
				if got := out.String(); got != fmt.Sprintf("status=%d\n", want) {
					t.Fatalf("stdout=%q want status=%d; stderr=%q", got, want, diag.String())
				}
				// Clearing readonly OPTARG is intentionally silent in Bash compatibility.
				diagnostic := tc.diagnostic
				if !strict && (tc.name == "clear_argument" || tc.name == "end_clear_argument" || tc.name == "empty_argument") {
					diagnostic = ""
				}
				if diagnostic == "" && diag.Len() != 0 || diagnostic != "" && !strings.Contains(diag.String(), diagnostic) {
					t.Fatalf("stderr=%q want diagnostic %q", diag.String(), diagnostic)
				}
				if tc.setup == "readonly OPTARG=keep" && r.lookupVar("OPTARG").String() != "keep" {
					t.Fatal("readonly OPTARG changed")
				}
			})
		}
	}
}
