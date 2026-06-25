// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"bytes"
	"context"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

// TestQuoteArrayFidelity covers the make test-bash `quotearray` fixture
// constructs: associative array references in arithmetic/conditional
// contexts expand their subscript exactly once (no re-run of an embedded
// `$(…)` or re-parse of `]`/`[`), an indexed subscript like `7<(4+2)` is
// arithmetic rather than a process substitution, a malformed expanded
// subscript reports its error token without the `((: … :` wrapper, and
// `unset` of a quoted subscript keeps the key literal.
func TestQuoteArrayFidelity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{
			// `[[ name[$key] -eq name[$key] ]]`: an associative subscript
			// is an opaque key. `$key` holds `]`, `[`, and `$(…)`, and
			// `$key3` is `~`; bash neither runs the command substitution
			// nor tilde-expands the key, so both lookups find 42 and the
			// comparison is true.
			input: "declare -A assoc\nkey='x],b[$(echo uname >&2)'\nkey3='~'\nassoc[$key]=42\nassoc[$key3]=42\n[[ assoc[$key] -eq assoc[$key3] ]]\necho $?",
			want:  "0\n",
		},
		{
			// An indexed subscript `7<(4+2)` is the arithmetic `7 < (4+2)`
			// (= 0), not a process substitution: index[0] is unset (0) and
			// assoc[0] is 0, so `-le` holds.
			input: "declare -a index\ndeclare -A assoc\nassoc[0]=0\n[[ index[7<(4+2)] -le assoc[0] ]]\necho $?",
			want:  "0\n",
		},
		{
			// `[[ -v assoc[sub] ]]`: the subscript is expanded exactly
			// once. `$key5` holds `$( echo 2>& date)` — found verbatim, no
			// second command substitution. A single-quoted `'$key5'` is the
			// literal key `$key5`, which is not present.
			input: "declare -A assoc\nkey5='$( echo 2>& date)'\nassoc[$key5]=foo\n[[ -v assoc[$key5] ]]\necho $?\n[[ -v assoc['$key5'] ]]\necho $?",
			want:  "0\n1\n",
		},
		{
			// `((array[$index]++))` with `$index` = `0],b[1`: the expanded
			// subscript is a malformed array reference. Bash reports the
			// echoed expansion directly (`0\],b\[1: …`) with no `((: … :`
			// command wrapper, since the value carried no `$`/backtick and
			// its brackets were backslash-escaped.
			input: "declare -a array\nindex='0],b[1'\n((array[$index]++))\necho status=$?",
			want:  "./s: line 3: 0\\],b\\[1: arithmetic syntax error: invalid arithmetic operator (error token is \"\\],b\\[1\")\nstatus=1\n",
		},
		{
			// `unset -v a['$key']`: the single-quoted subscript pins the
			// key to the literal `$key` (no expansion), so the element set
			// by `a['$key']=2` is removed and the array is left empty.
			input: "declare -A a\nkey='$(echo foo)'\na['$key']=2\nunset -v a['$key']\ndeclare -p a",
			want:  "declare -A a=()\n",
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
