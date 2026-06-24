package interp

import (
	"bytes"
	"context"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func TestArithFidelity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{
			input: "set -u\n(( undef1++ ))\n(( ++undef2 ))\necho \"[$undef1][$undef2]\"",
			want:  "undef1: unbound variable\nexit status 1",
		},
		{
			input: "typeset -A assoc\nassoc[0]=42\necho $((assoc + 5))",
			want:  "47\n",
		},
		{
			input: "echo $((a + x42))\necho status=$?\necho $((a + 42x))\necho status=$?",
			want:  "0\nstatus=0\nvalue too great for base (error token is \"42x\")\nstatus=1\n",
		},
		{
			input: "foo=bar\nxbar=42\necho $(( x$foo++ ))\necho xbar=$xbar",
			want:  "42\nxbar=43\n",
		},
		{
			input: "foo=bar\nxbar[5]=42\necho $(( x$foo[5]++ ))\necho 'xbar[5]='${xbar[5]}",
			want:  "42\nxbar[5]=43\n",
		},
		{
			input: "(( 1[2] = 3 ))\necho status=$?\n(( a[1][2] = 3 ))\necho status=$?",
			want:  "arithmetic syntax error: invalid arithmetic operator (error token is \"[2] = 3 \")\nstatus=1\narithmetic syntax error: invalid arithmetic operator (error token is \"[2] = 3 \")\nstatus=1\n",
		},
		{
			input: "a=(1 2 3)\necho $(( a[1] ))\necho $(( a[1][1] ))\necho status=$?",
			want:  "2\narithmetic syntax error: invalid arithmetic operator (error token is \"[1] \")\nstatus=1\n",
		},
		{
			input: "echo $(( '1' + 2))\necho status=$?",
			want:  "arithmetic syntax error: operand expected (error token is \"'1' \")\nstatus=1\n",
		},
		{
			input: "a=(a b c d e f)\necho slice ${a[@]: }\necho status=$?\necho slice ${a[@]: : }\necho status=$?\necho slice ${a[@]:: }\necho status=$?",
			want:  "slice a b c d e f\nstatus=0\nslice\nstatus=0\nslice\nstatus=0\n",
		},
		{
			input: "a=(1 2 3)\na[]=42\necho status=$?\necho ${a[@]}\necho ${a[]}\necho status=$?",
			want:  "a[]: bad array subscript\nstatus=1\n1 2 3\nbad substitution\nstatus=1\n",
		},
		{
			input: "echo $(( 1 + 2.3 ))",
			want:  "arithmetic syntax error: invalid arithmetic operator (error token is \".3\")\nexit status 1",
		},
		{
			input: "echo $(( 5 << 1 ))\necho $(( 5 << 0 ))\necho $(( 5 << -1 ))",
			want:  "10\n5\n-9223372036854775808\n",
		},
		{
			input: `echo $(( "1 + 2" * 3 ))
echo $(( "1+2" * 3 ))
x='1 + 2'
echo $(( $x * 3 ))
echo $(( "$x" * 3 ))`,
			want: "7\n7\n7\n7\n",
		},
		{
			input: "base=16\necho $(( ${base}#a ))\nzero=0\necho $(( ${zero}11 ))\necho $(( ${zero}xAB ))",
			want:  "10\n9\n171\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run("", func(t *testing.T) {
			t.Parallel()

			parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
			file, err := parser.Parse(bytes.NewReader([]byte(tt.input)), "./s")
			if err != nil {
				t.Fatal(err)
			}

			var buf bytes.Buffer
			r, err := New(Dir(t.TempDir()), StdIO(nil, &buf, &buf))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := r.Run(ctx, file); err != nil {
				buf.WriteString(err.Error())
			}
			if got := buf.String(); got != tt.want {
				t.Fatalf("wrong output in %q:\nwant: %q\ngot:  %q", tt.input, tt.want, got)
			}
		})
	}
}
