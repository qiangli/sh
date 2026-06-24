// Copyright (c) 2026, the sh authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func TestExpandTailBuiltinFidelity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "builtin-bracket__007",
			in:   "test -n x ]\necho status=$?\ntest -n x y\necho status=$?",
			want: "./s: line 1: test: x: binary operator expected\n" +
				"status=2\n" +
				"./s: line 3: test: x: binary operator expected\n" +
				"status=2\n",
		},
		{
			name: "builtin-printf__004",
			in:   "printf -v 'a[' %s 'foo'\necho status=$?",
			want: "./s: line 1: printf: `a[': not a valid identifier\n" +
				"status=2\n",
		},
		{
			name: "quote__029",
			in:   "echo $'\\uZ' $'\\u{03bc' $'\\z'",
			want: "\\uZ \\u{03bc \\z\n",
		},
		{
			// builtin-vars__016: a readonly-variable assignment error
			// inside `eval` discards only the eval'd command list — bash
			// contains the DISCARD within eval, which returns status 1,
			// so the enclosing function keeps running (the `status=1`
			// echo and the later global read both execute). (bash reports
			// the error at the eval-call line 5; our eval re-parses the
			// payload with fresh positions and reports line 1.)
			name: "builtin-vars__016",
			in: "f() {\n\tlocal x=local\n\treadonly x\n\techo $x\n" +
				"\teval 'x=bar'\n\techo status=$?\n}\nx=global\nf\necho $x",
			want: "local\n" +
				"./s: line 1: x: readonly variable\n" +
				"status=1\n" +
				"global\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
			file, err := parser.Parse(strings.NewReader(tt.in), "./s")
			if err != nil {
				t.Fatal(err)
			}

			var buf lockedBuffer
			r, err := New(
				Dir(t.TempDir()),
				StdIO(nil, &buf, &buf),
				WithBashCompatErrors(true),
				WithBashSource([]byte(tt.in)),
			)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := r.Run(ctx, file); err != nil {
				buf.Write([]byte(err.Error()))
			}
			if got := buf.String(); got != tt.want {
				t.Fatalf("wrong output:\nwant: %q\ngot:  %q", tt.want, got)
			}
		})
	}
}
