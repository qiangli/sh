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
			in: "argv.py() { python - \"$@\" <<'PY'\n" +
				"import sys\n" +
				"print(sys.argv[1:])\n" +
				"PY\n" +
				"}\n" +
				"single=('')\n" +
				"argv.py ${single[@]:-none} x \"${single[@]:-none}\"\n",
			want: "['none', 'x', 'none']\n",
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
