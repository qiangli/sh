// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

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

// lockedBuffer is a goroutine-safe buffer for capturing a runner's
// combined stdout+stderr.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestDbracketFidelity covers bash 5.3 [[ ]] conditional-command
// fidelity gaps fixed in interp/test.go. Each case runs a script
// through a Runner and compares the combined stdout+stderr (with a
// trailing "exit status N" appended when Run returns a non-nil error,
// matching interp_test.go's TestRunnerRun convention).
func TestDbracketFidelity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		// dbracket__043.sh — tilde expansion of [[ ]] operands,
		// including a $HOME that is set but empty (which bash expands
		// to "", unlike a tilde left intact for an unset HOME).
		{
			name: "043_tilde_operands",
			in: "HOME=/root\n" +
				"[[ ~ ]]\n" +
				"echo status=$?\n" +
				"HOME=''\n" +
				"[[ ~ ]]\n" +
				"echo status=$?\n" +
				"[[ -n ~ ]]\n" +
				"echo unary=$?\n" +
				"[[ ~ == ~ ]]\n" +
				"echo status=$?\n" +
				"[[ $HOME == ~ ]]\n" +
				"echo fnmatch=$?\n" +
				"[[ ~ == $HOME ]]\n" +
				"echo fnmatch=$?\n",
			want: "status=0\nstatus=1\nunary=1\nstatus=0\nfnmatch=0\nfnmatch=0\n",
		},
		// dbracket__044.sh — a tilde-expanded =~ operand is matched
		// literally; the home directory's regex metacharacters (e.g. a
		// HOME of `^a$`) are quoted, while a $HOME on the rhs keeps its
		// regex metacharacters active.
		{
			name: "044_tilde_regex",
			in: "HOME=foo\n" +
				"[[ ~ =~ $HOME ]]\n" +
				"echo regex=$?\n" +
				"[[ $HOME =~ ~ ]]\n" +
				"echo regex=$?\n" +
				"HOME='^a$'\n" +
				"[[ ~ =~ $HOME ]]\n" +
				"echo regex=$?\n" +
				"[[ $HOME =~ ~ ]]\n" +
				"echo regex=$?\n",
			want: "regex=0\nregex=0\nregex=1\nregex=0\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			file, err := syntax.NewParser().Parse(strings.NewReader(tc.in), "")
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}

			var cb lockedBuffer
			r, err := New(StdIO(nil, &cb, &cb))
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := r.Run(ctx, file); err != nil {
				cb.Write([]byte(err.Error()))
			}

			if got := cb.String(); got != tc.want {
				t.Fatalf("wrong output in %q:\nwant: %q\ngot:  %q", tc.in, tc.want, got)
			}
		})
	}
}
