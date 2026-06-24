// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"context"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func TestArrayElementUnsetAndSetnessFidelity(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "assoc_nameref_element_target",
			in: "show-values() { echo values: ${A[@]}; }\n" +
				"declare -A A=(['K']=val)\n" +
				"show-values\n" +
				"declare -n ref='A[\"K\"]'\n" +
				"echo before $ref\n" +
				"ref='val2'\n" +
				"echo after $ref\n" +
				"show-values\n" +
				"echo ---\n" +
				"key=K\n" +
				"declare -n ref='A[$key]'\n" +
				"echo before $ref\n" +
				"ref='val3'\n" +
				"echo after $ref\n" +
				"show-values\n",
			want: "values: val\nbefore val\nafter val2\nvalues: val2\n---\nbefore val2\nafter val3\nvalues: val3\n",
		},
		{
			name: "assoc_indirect_and_printf_element_target",
			in: "show-values() { echo values: ${A[@]}; }\n" +
				"declare -A A=(['K']=val)\n" +
				"show-values\n" +
				"declare ref='A[\"K\"]'\n" +
				"echo ref ${!ref}\n" +
				"key=K\n" +
				"declare ref='A[$key]'\n" +
				"echo ref ${!ref}\n" +
				"printf -v 'A[$key]' '/%s/' val2\n" +
				"show-values\n",
			want: "values: val\nref val\nref val\nvalues: /val2/\n",
		},
		{
			name: "assoc_unset_element_delete",
			in: "show-len() { echo len=${#assoc[@]}; }\n" +
				"declare -A assoc=(['K']=val)\n" +
				"show-len\n" +
				"unset -v 'assoc[\"K\"]'\n" +
				"show-len\n" +
				"declare -A assoc=(['K']=val)\n" +
				"key=K\n" +
				"unset -v 'assoc[$key]'\n" +
				"show-len\n",
			want: "len=1\nlen=0\nlen=0\n",
		},
		{
			name: "element_setness",
			in: "typeset -A assoc\n" +
				"assoc=([empty]='' [k]=v)\n" +
				"[[ -v assoc[empty] ]]; echo empty=$?\n" +
				"[[ -v assoc[k] ]]; echo k=$?\n" +
				"[[ -v assoc[nonexistent] ]]; echo nonexistent=$?\n" +
				"array=(1 2 3 '')\n" +
				"test -v 'array[1+1]'; echo status=$?\n" +
				"test -v 'array[4+1]'; echo status=$?\n" +
				"typeset -a sparse\n" +
				"sparse=('' nonempty)\n" +
				"zero=0\n" +
				"[[ -v sparse[zero+0] ]]; echo zero=$?\n" +
				"[[ -v sparse[zero+1] ]]; echo one=$?\n" +
				"[[ -v sparse[zero+2] ]]; echo two=$?\n",
			want: "empty=0\nk=0\nnonexistent=1\nstatus=0\nstatus=1\nzero=0\none=0\ntwo=1\n",
		},
		{
			name: "empty_array_element_default",
			in: "argv.py() { python3 - \"$@\" <<'PY'\n" +
				"import sys\n" +
				"print(sys.argv[1:])\n" +
				"PY\n" +
				"}\n" +
				"single=('')\n" +
				"argv.py ${single[@]:-none} x \"${single[@]:-none}\"\n",
			want: "['none', 'x', 'none']\n",
		},
		{
			name: "assoc_unquoted_keys_split",
			in: "argv.py() { python3 - \"$@\" <<'PY'\n" +
				"import sys\n" +
				"print(sys.argv[1:])\n" +
				"PY\n" +
				"}\n" +
				"declare -A A\n" +
				"A['X X']='xx'\n" +
				"A['Y Y']='yy'\n" +
				"argv.py \"${A[*]}\"\n" +
				"argv.py \"${!A[*]}\"\n" +
				"argv.py ${A[@]}\n" +
				"argv.py ${!A[@]}\n",
			want: "['xx yy']\n['X X Y Y']\n['xx', 'yy']\n['X', 'X', 'Y', 'Y']\n",
		},
		{
			name: "assoc_slice_values",
			in: "argv.py() { python3 - \"$@\" <<'PY'\n" +
				"import sys\n" +
				"print(sys.argv[1:])\n" +
				"PY\n" +
				"}\n" +
				"declare -A a\n" +
				"a[xx]=1\n" +
				"a[yy]=2\n" +
				"a[zz]=3\n" +
				"a[aa]=4\n" +
				"a[bb]=5\n" +
				"argv.py ${a[@]: 0: 3}\n" +
				"argv.py ${a[@]: 1: 3}\n" +
				"argv.py ${a[@]: 2: 3}\n" +
				"argv.py ${a[@]: 3: 3}\n" +
				"argv.py ${a[@]: 4: 3}\n" +
				"argv.py ${a[@]: 5: 3}\n",
			want: "['4', '1', '3']\n['4', '1', '3']\n['1', '3', '5']\n['3', '5', '2']\n['5', '2']\n['2']\n",
		},
		{
			name: "assoc_literal_append_residue",
			in: "argv.py() { python3 - \"$@\" <<'PY'\n" +
				"import sys\n" +
				"print(sys.argv[1:])\n" +
				"PY\n" +
				"}\n" +
				"declare -A a\n" +
				"hello=100\n" +
				"a=([hello]=1 [hello]+=2)\n" +
				"printf 'keys: '; argv.py \"${!a[@]}\"\n" +
				"printf 'vals: '; argv.py \"${a[@]}\"\n" +
				"a+=([hello]+=:34 [hello]+=:56)\n" +
				"printf 'keys: '; argv.py \"${!a[@]}\"\n" +
				"printf 'vals: '; argv.py \"${a[@]}\"\n",
			want: "keys: ['hello']\nvals: ['2']\nkeys: ['hello']\nvals: ['2:34:56']\n",
		},
		{
			name: "indexed_subscript_indirect",
			in: "argv.py() { python3 - \"$@\" <<'PY'\n" +
				"import sys\n" +
				"print(sys.argv[1:])\n" +
				"PY\n" +
				"}\n" +
				"foo=bar\n" +
				"a=('1 2' foo '2 3')\n" +
				"argv.py \"${!a[1]}\"\n",
			want: "['bar']\n",
		},
		{
			name: "shopt_long_set_strict_array",
			// bash rejects `shopt --set` (the `--set`/`--unset` long
			// forms are OSH-only). As a bash drop-in we are `bash`,
			// so the OSH-only line is skipped and the array
			// reassignment behaves exactly like bash.
			in: "SH=bash\n" +
				"case $SH in bash) ;; *) shopt --set strict_array ;; esac\n" +
				"s1=hello\n" +
				"s2=world\n" +
				"eval 's1=(1 2 3 4)'\n" +
				"echo status=$?\n" +
				"declare -p s1\n" +
				"eval 's2+=(1 2 3 4)'\n" +
				"echo status=$?\n" +
				"declare -p s2\n",
			want: "status=0\ndeclare -a s1=([0]=\"1\" [1]=\"2\" [2]=\"3\" [3]=\"4\")\nstatus=0\ndeclare -a s2=([0]=\"world\" [1]=\"1\" [2]=\"2\" [3]=\"3\" [4]=\"4\")\n",
		},
		{
			name: "array_prefix_assignment_env_string",
			in:   "A=a B=(b b) true\n" + "echo status=$?\n",
			want: "status=0\n",
		},
		{
			name: "negative_array_slice_status",
			in: "a=(1 2 3 4 5)\n" +
				"echo \"${a[@]: 1: -3}\"\n" +
				"echo status=$?\n",
			want: "-3: substring expression < 0\nstatus=0\n",
		},
		{
			name: "test_v_negative_empty_array",
			in:   "e=()\n[[ -v e[-1] ]] && echo yes\n",
			want: "e: bad array subscript\nexit status 1",
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
