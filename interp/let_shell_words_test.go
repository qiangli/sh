package interp

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func TestLetShellWordsRuntime(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"fd-dup", `n=1; let n 1>&2; printf '%s:%s' "$?" "$n"`, "0:1"},
		{"implicit-fd", `n=1; let n >&2; printf '%s:%s' "$?" "$n"`, "0:1"},
		{"stderr-file", `n=1; let n 2>err; printf '%s:%s' "$?" "$n"`, "0:1"},
		{"leading-redirect", `n=1; let 2>err n; printf '%s:%s' "$?" "$n"`, "0:1"},
		{"interleaved-redirect", `let n=1 >out n+=2; printf '%s:%s' "$?" "$n"`, "0:3"},
		{"adjacent-redirect", `n=1; let n>out; printf '%s:%s' "$?" "$n"`, "0:1"},
		{"quoted-comparison", `n=1; let 'n>0'; printf '%s:%s' "$?" "$n"`, "0:1"},
		{"assignment-group", `let n=(6 + 4) m=(8 + 5); printf '%s:%s' "$n" "$m"`, "10:13"},
		{"assignment-group-tail", `let n=(6*4)/2; printf '%s' "$n"`, "12"},
		{"quoted-group", `let "n=(1 + 2)"; printf '%s:%s' "$?" "$n"`, "0:3"},
		{"unreachable-operand", `if false; then let .sh.subshell; fi; printf reached`, "reached"},
		{"unreachable-case", `case bash in ksh) let .sh.subshell;; esac; printf reached`, "reached"},
		{"executed-invalid", `let .sh.subshell 2>err; printf 'status:%s' "$?"`, "status:1"},
		{"truncated-expression", `let n+ 2>err; printf 'status:%s' "$?"`, "status:1"},
		{"continuation", "let n=1 \\\n n+=2; printf '%s' \"$n\"", "3"},
		{"backquote", "n=1; result=`let n 1>&2; printf yes`; printf '%s' \"$result\"", "yes"},
		{"field-splitting", `args='n=1 n+=2'; let $args; printf '%s:%s' "$?" "$n"`, "0:3"},
		{"quote-removal", `declare -a a; let a\[" "\]=13; printf '%s' "${a[0]}"`, "13"},
		{"expand-before-eval", `n=4; let n=2 m=$n; printf '%s:%s' "$n" "$m"`, "2:4"},
		{"expand-once", `n=0; let "m=$(printf 3; printf x >>calls)"; printf '%s:%s' "$m" "$(<calls)"`, "3:x"},
		{"abort-on-error", `let 1/0 n=4 2>err; printf '%s:%s' "$?" "${n-unset}"`, "1:unset"},
		{"empty-unquoted", `args=; let $args 2>err; printf '%s' "$?"`, "1"},
		{"valid-assoc-comma", `declare -A a; a[x]=1; let 'a[x],n=2'; printf '%s' "$n"`, "2"},
		{"raw-key-option-unset", `shopt -u assoc_expand_once; declare -A m; k="'clock'"; let "m[$k]=4"; printf '%s' "${m[clock]}"`, "4"},
		{"ordinary-arithmetic", `shopt -s assoc_expand_once; declare -A m; k="o'clock"; ((m[$k]=4)); printf '%s' "${m[$k]}"`, "4"},
		{"last-operand", `let 1 0; printf '%s:' "$?"; let 0 1; printf '%s' "$?"`, "1:0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := syntax.NewParser().Parse(strings.NewReader(tc.src), "let")
			if err != nil {
				t.Fatal(err)
			}
			var out, errs bytes.Buffer
			r, err := New(Dir(t.TempDir()), StdIO(nil, &out, &errs))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := r.Run(ctx, f); err != nil {
				t.Fatalf("run: %v; stderr=%q", err, errs.String())
			}
			if out.String() != tc.want || errs.Len() != 0 {
				t.Fatalf("stdout=%q want=%q stderr=%q", out.String(), tc.want, errs.String())
			}
		})
	}
}

func TestLetShellWordsTrace(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"arguments", `set -x; let n=1 n+=2`, "+ let n=1 n+=2\n"},
		{"radix", `set -x; let '2#11'`, "+ let 2#11\n"},
		{"quoted-argument", `set -x; let 'n = 2'`, "+ let 'n = 2'\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := syntax.NewParser().Parse(strings.NewReader(tc.src), "let")
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			r, err := New(Dir(t.TempDir()), StdIO(nil, &out, &out))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := r.Run(ctx, f); err != nil {
				t.Fatal(err)
			}
			if out.String() != tc.want {
				t.Fatalf("output=%q want=%q", out.String(), tc.want)
			}
		})
	}
}
