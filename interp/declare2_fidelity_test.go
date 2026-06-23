// Copyright (c) 2024, the sh authors.
// See LICENSE for licensing information.

package interp

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

// syncBuffer is a goroutine-safe buffer collecting both stdout and stderr,
// so the relative order of a builtin's error output and a following echo is
// preserved the way the combined-stream spec cases expect.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestDeclare2Fidelity covers the bash-5.3 drop-in fidelity gaps closed in the
// declare/typeset/local/readonly/export/unset builtins. It mirrors the
// {input, want} table style of TestRunnerRun: `want` is the combined
// stdout+stderr, and a script whose final command exits non-zero appends the
// "exit status N" line that interp.Runner.Run reports as its error.
func TestDeclare2Fidelity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in, want string
	}{
		// builtin-vars__032: subscript-unset of a declared-but-unset scalar.
		// A non-zero subscript is the "not an array variable" error case, but
		// subscript `[0]` (and any index that evaluates to 0, such as the
		// unset name in `["key"]`) aliases the scalar itself, so bash returns
		// success and merely no-ops.
		{
			"declare undef; unset -v 'undef[1]'; echo status=$?",
			"unset: undef: not an array variable\nstatus=1\n",
		},
		{
			`declare undef; unset -v 'undef["key"]'; echo status=$?`,
			"status=0\n",
		},
		{
			"declare undef; unset -v 'undef[0]'; echo status=$?",
			"status=0\n",
		},
		// The associative-array sibling rows from the same spec case still
		// behave: a missing key is a successful no-op.
		{
			`declare -A A=(['key']=val); unset -v 'A[1]'; echo $?; unset -v 'A["key"]'; echo $?`,
			"0\n0\n",
		},

		// builtin-vars__026: a name that cannot even begin a variable
		// identifier (pure punctuation) is not a botched identifier; bare
		// `unset` silently falls through to the function namespace and exits 0.
		{
			"unset %; echo status=$?",
			"status=0\n",
		},
		{
			"unset @; echo status=$?",
			"status=0\n",
		},
		// Regression guard: digit/letter-led invalid names must still error.
		{
			"unset '1bad'; echo after=$?",
			"unset: `1bad': not a valid identifier\nafter=2\n",
		},
		// Regression guard: the set-scalar `[0]` whole-variable unset path.
		{
			"s=abc; unset 's[0]'; echo status:$?; echo ${s-unset}",
			"status:0\nunset\n",
		},
	}

	p := syntax.NewParser()
	for _, c := range cases {
		t.Run("", func(t *testing.T) {
			t.Logf("input: %q", c.in)
			file, err := p.Parse(strings.NewReader(c.in), "")
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			t.Parallel()

			var buf syncBuffer
			r, err := New(Dir(t.TempDir()), StdIO(nil, &buf, &buf))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if rerr := r.Run(ctx, file); rerr != nil {
				buf.Write([]byte(rerr.Error()))
			}
			if got := buf.String(); got != c.want {
				t.Fatalf("wrong output in %q:\nwant: %q\ngot:  %q", c.in, c.want, got)
			}
		})
	}
}
