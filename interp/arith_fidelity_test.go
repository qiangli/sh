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
		{
			input: `a=(0 1 2 3 4)
unset a[1]
unset a[4]
echo "${a[@]}"
echo -1 ${a[-1]}
echo -2 ${a[-2]}
echo -3 ${a[-3]}
echo -4 ${a[-4]}
echo -5 ${a[-5]}
a[-1]+=0
echo ${a[@]}
(( a[-1] += 42 ))
echo ${a[@]}`,
			want: "0 2 3\n-1 3\n-2 2\n-3\n-4 0\na: bad array subscript\n-5\n0 2 30\n0 2 72\n",
		},
		{
			input: "a=(1 2 3 4 5)\necho ${a[@]: 1: -3}",
			want:  " -3: substring expression < 0\n",
		},
		{
			input: "a[5 # 1]=x\necho status=$? len=${#a[@]}",
			want:  "./s: line 1: 5 # 1: arithmetic syntax error: invalid arithmetic operator (error token is \"# 1\")\nstatus=1 len=0\n",
		},
		{
			input: "a=(x)\na['2']=x\necho status=$? len=${#a[@]}\na[0]=x\necho status=$? len=${#a[@]}\na[3 + '4']=x\necho status=$? len=${#a[@]}",
			want:  "arithmetic syntax error: operand expected (error token is \"'2' \")\nstatus=1 len=1\nstatus=0 len=1\narithmetic syntax error: operand expected (error token is \"'4' \")\nstatus=1 len=1\n",
		},
		{
			input: "hello=100\na=([hello]=1 [hello]+=2)\nprintf 'keys: '; printf '<%s>\\n' \"${!a[@]}\"\nprintf 'vals: '; printf '<%s>\\n' \"${a[@]}\"\na=([100]=1 2 3 4 [5]=a b c d)\nprintf 'keys:'; for k in \"${!a[@]}\"; do printf ' <%s>' \"$k\"; done; echo\nprintf 'vals:'; for v in \"${a[@]}\"; do printf ' <%s>' \"$v\"; done; echo",
			want:  "keys: <100>\nvals: <12>\nkeys: <5> <6> <7> <8> <100> <101> <102> <103>\nvals: <a> <b> <c> <d> <1> <2> <3> <4>\n",
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

func TestArithFidelityBashSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{
			input: "echo $(( '1' + '2' * 3 ))\necho status=$?\n\necho $(( '1 + 2' * 3 ))\necho status=$?",
			want:  "./s: line 1: '1' + '2' * 3 : arithmetic syntax error: operand expected (error token is \"'1' \")\nstatus=1\n./s: line 4: '1 + 2' * 3 : arithmetic syntax error: operand expected (error token is \"'1 + 2' \")\nstatus=1\n",
		},
		{
			input: "echo $(( '1' + 2))",
			want:  "./s: line 1: '1' + 2: arithmetic syntax error: operand expected (error token is \"'1' \")\nexit status 1",
		},
		{
			input: "set -u\n(( undef1++ ))\n(( ++undef2 ))\necho \"[$undef1][$undef2]\"",
			want:  "./s: line 2: undef1: unbound variable\nexit status 1",
		},
		{
			input: "(( 1[2] = 3 ))\necho status=$?\n(( a[1][2] = 3 ))\necho status=$?",
			want:  "./s: line 1: ((: 1[2] = 3 : arithmetic syntax error: invalid arithmetic operator (error token is \"[2] = 3 \")\nstatus=1\n./s: line 3: ((: a[1][2] = 3 : arithmetic syntax error: invalid arithmetic operator (error token is \"[2] = 3 \")\nstatus=1\n",
		},
		{
			input: "a=(1 2 3)\necho $(( a[1] ))\necho $(( a[1][1] ))",
			want:  "2\n./s: line 3: a[1][1] : arithmetic syntax error: invalid arithmetic operator (error token is \"[1] \")\nexit status 1",
		},
		{
			input: "echo $(( 2**-1 * 5 ))",
			want:  "./s: line 1: 2**-1 * 5 : exponent less than 0 (error token is \"* 5 \")\n",
		},
		{
			input: "s='12 34'\necho '12 34' $(( s[0] )) $(( s[1] ))\necho status=$?",
			want:  "./s: line 2: 12 34: arithmetic syntax error in expression (error token is \"34\")\nstatus=1\n",
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
			r, err := New(
				Dir(t.TempDir()),
				StdIO(nil, &buf, &buf),
				WithBashCompatErrors(true),
				WithBashSource([]byte(tt.input)),
			)
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
