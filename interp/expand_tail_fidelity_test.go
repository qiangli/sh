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
