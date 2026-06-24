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

		// assign-extended: eval -- "$(declare -p arr)" must restore a
		// sparse indexed array, including explicitly set empty elements.
		{
			`arr=(1 2 3); eval -- "$(arr=(); arr[3]=; arr[4]=foo; declare -p arr)"; for i in {0..4}; do echo "arr[$i]: ${arr[$i]+set ... [}${arr[$i]-unset}${arr[$i]+]}"; done`,
			"arr[0]: unset\narr[1]: unset\narr[2]: unset\narr[3]: set ... []\narr[4]: set ... [foo]\n",
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

		// assign-extended__012: query-mode words with `=` are looked up
		// literally for diagnostics; they are not declarations.
		{
			"declare -p foo=bar; echo status=$?; a=b; declare -p a foo=bar; echo status=$?",
			"declare: foo=bar: not found\nstatus=1\ndeclare -- a=\"b\"\ndeclare: foo=bar: not found\nstatus=1\n",
		},
		// assign-extended__013: explicit `declare -p` names are filtered
		// by attribute flags like bash 5.3.
		{
			"test_var1=111; readonly test_var2=222; export test_var3=333; declare -n test_var4=test_var1; echo '[declare -pn]'; declare -pn test_var{0..5}; echo '[declare -pr]'; declare -pr test_var{0..5}; echo '[declare -px]'; declare -px test_var{0..5}",
			"[declare -pn]\ndeclare -n test_var4=\"test_var1\"\n[declare -pr]\ndeclare -r test_var2=\"222\"\n[declare -px]\ndeclare -x test_var3=\"333\"\n",
		},
		// assign__045: readonly -a/-A on a fresh name sets readonly only;
		// it must not create an empty array. Existing arrays are preserved.
		{
			"declare -a arr1; readonly -a arr2; declare -A dict1; readonly -A dict2; declare -p arr1 arr2 dict1 dict2",
			"declare -a arr1\ndeclare -r arr2\ndeclare -A dict1\ndeclare -r dict2\n",
		},
		// assign-extended__035: `typeset +r r=value` on a readonly var
		// reports the failed readonly mutation once and leaves it unchanged.
		{
			"readonly r=r1; echo r=$r; typeset +r r=r2; echo r=$r",
			"r=r1\ntypeset: r: readonly variable\nr=r1\n",
		},
		// assign-extended__007/__008: no-arg declare/export/readonly
		// list variables instead of falling through to an empty result.
		{
			"zz_assign_plain=111; readonly zz_assign_ro=222; export zz_assign_ex=333; declare | grep '^zz_assign_'; readonly | grep '^declare -r zz_assign_ro='; export | grep '^declare -x zz_assign_ex='",
			"zz_assign_ex=333\nzz_assign_plain=111\nzz_assign_ro=222\ndeclare -r zz_assign_ro=\"222\"\ndeclare -x zz_assign_ex=\"333\"\n",
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
