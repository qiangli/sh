// Copyright (c) 2026, the bashy fork
// See LICENSE for licensing information

package main

import (
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func TestBashDbracketParseError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "dynamic_operator",
			src:  "op='=='\n[[ a $op a ]] && echo true\n",
			want: "./s: line 2: unexpected token `$op', conditional binary operator expected\n" +
				"./s: line 2: syntax error near `$op'\n" +
				"./s: line 2: `[[ a $op a ]] && echo true'",
		},
		{
			name: "unary_redirection",
			src:  "[[ -f < ]] && echo should\n",
			want: "./s: line 1: unexpected argument `<' to conditional unary operator\n" +
				"./s: line 1: syntax error near `<'\n" +
				"./s: line 1: `[[ -f < ]] && echo should'",
		},
		{
			name: "missing_unary_arg",
			src:  "[[ -z ]]\necho status=$?\n",
			want: "./s: line 1: unexpected argument `]]' to conditional unary operator\n" +
				"./s: line 1: syntax error near `]]'\n" +
				"./s: line 1: `[[ -z ]]'",
		},
		{
			name: "extra_unary_arg",
			src:  "[[ -z '>' -- ]]\n",
			want: "./s: line 1: syntax error in conditional expression\n" +
				"./s: line 1: syntax error near `--'\n" +
				"./s: line 1: `[[ -z '>' -- ]]'",
		},
		{
			name: "empty",
			src:  "[[ ]]\n",
			want: "./s: line 1: syntax error near `]]'\n" +
				"./s: line 1: `[[ ]]'",
		},
		{
			name: "leading_and",
			src:  "[[ && ]]\n",
			want: "./s: line 1: unexpected token `&&' in conditional command\n" +
				"./s: line 1: syntax error near `&'\n" +
				"./s: line 1: `[[ && ]]'",
		},
		{
			name: "fd_redirection_operator",
			src:  "[[ a 3< b ]]\n",
			want: "./s: line 1: unexpected token `3<', conditional binary operator expected\n" +
				"./s: line 1: syntax error near `3<'\n" +
				"./s: line 1: `[[ a 3< b ]]'",
		},
		{
			name: "quoted_paren_literal",
			src:  "[[ '(' foo ]]\n",
			want: "./s: line 1: unexpected token `foo', conditional binary operator expected\n" +
				"./s: line 1: syntax error near `foo'\n" +
				"./s: line 1: `[[ '(' foo ]]'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := syntax.NewParser().Parse(strings.NewReader(tt.src), "./s")
			if err == nil {
				t.Fatal("expected parse error")
			}
			got, ok := bashDbracketParseError(err, []byte(tt.src), "./s")
			if !ok {
				t.Fatal("error was not formatted as a [[ ]] parse error")
			}
			if got != tt.want {
				t.Fatalf("wrong diagnostic:\nwant: %q\ngot:  %q", tt.want, got)
			}
		})
	}
}
