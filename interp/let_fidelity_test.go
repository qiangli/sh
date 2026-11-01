package interp

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

func TestLetExpandedArgumentsFidelity(t *testing.T) {
	cases := []struct{ name, src string }{
		{"literal-dollar", "v=7\nlet 'n += $v'\nprintf 'status:%s\\n' \"$?\"\n"},
		{"error-position", "n=1\n\nlet 'n += $missing'\nprintf 'status:%s\\n' \"$?\"\n"},
		{"parentheses", `let n=(6 + 4) m=(8*6)/2; printf '%s:%s\n' "$n" "$m"`},
		{"apostrophe", `shopt -s assoc_expand_once; declare -A m; k="o'clock"; let "++m[$k]"; printf '%s\n' "${m[$k]}"`},
		{"raw-keys", `shopt -s assoc_expand_once; declare -A m; for k in "o'clock" 'a"b' 'a\b' 'a[b' '$v' '$(printf WRONG)' 'élève'; do let "m[$k]=3"; let "m[$k]+=2"; let "n=m[$k]"; printf '%s:%s\n' "$k" "$n"; done`},
		{"two-keys", `shopt -s assoc_expand_once; declare -A m; a="o'clock"; b='a"b'; let "m[$a]=3,m[$b]=m[$a]+2"; printf '%s:%s\n' "${m[$a]}" "${m[$b]}"`},
		{"nameref", `shopt -s assoc_expand_once; declare -A m; declare -n ref=m; k="o'clock"; let "++ref[$k]"; let "n=ref[$k]"; printf '%s:%s\n' "$n" "${m[$k]}"`},
		{"short-circuit", `shopt -s assoc_expand_once; declare -A m; k='$(printf WRONG)'; let "0 && ++m[$k]"; printf '%s\n' "${#m[@]}"`},
		{"raw-open-bracket-dollar", `shopt -s assoc_expand_once; declare -A m; v=3; let 'm[x[]+$v'; printf 'status:%s\n' "$?"`},
		{"parameter-index", `declare -A m; k=x; let 'm[$k]++'; printf '%s\n' "${m[x]}"`},
		{"braced-index", `declare -A m; k=x; let 'm[${k}]++'; printf '%s\n' "${m[x]}"`},
		{"command-index", `declare -A m; let 'm[$(printf x)]++'; printf '%s\n' "${m[x]}"`},
	}
	wants := map[string]string{
		"literal-dollar": "status:1\n", "error-position": "status:1\n", "parentheses": "10:24\n",
		"apostrophe": "1\n", "raw-keys": "o'clock:5\na\"b:5\na\\b:5\na[b:5\n$v:5\n$(printf WRONG):5\nélève:5\n",
		"two-keys": "3:5\n", "nameref": "1:1\n", "short-circuit": "0\n", "raw-open-bracket-dollar": "status:1\n",
		"parameter-index": "1\n", "braced-index": "1\n", "command-index": "1\n",
	}
	errorWants := map[string]string{
		"literal-dollar":          ": line 2: let: n += $v: arithmetic syntax error: operand expected (error token is \"$v\")\n",
		"error-position":          ": line 3: let: n += $missing: arithmetic syntax error: operand expected (error token is \"$missing\")\n",
		"raw-open-bracket-dollar": ": line 1: let: m[x[]+$v: arithmetic syntax error: operand expected (error token is \"$v\")\n",
	}
	reference := os.Getenv("SH_TEST_BASH_REFERENCE")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "case.sh")
			if err := os.WriteFile(path, []byte(tc.src), 0600); err != nil {
				t.Fatal(err)
			}
			f, err := syntax.NewParser().Parse(strings.NewReader(tc.src), path)
			if err != nil {
				t.Fatal(err)
			}
			var out, errs bytes.Buffer
			r, err := New(Dir(dir), StdIO(nil, &out, &errs), WithBashCompatErrors(true), WithBashSource([]byte(tc.src)))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			runErr := r.Run(ctx, f)
			wantErr := errorWants[tc.name]
			if wantErr != "" {
				wantErr = path + wantErr
			}
			if runErr != nil || out.String() != wants[tc.name] || errs.String() != wantErr {
				t.Fatalf("error=%v stdout=%q want=%q stderr=%q want=%q", runErr, out.String(), wants[tc.name], errs.String(), wantErr)
			}
			if reference == "" {
				return
			}
			cmd := exec.CommandContext(ctx, reference, path)
			cmd.Dir = dir
			var refOut, refErr bytes.Buffer
			cmd.Stdout = &refOut
			cmd.Stderr = &refErr
			refRunErr := cmd.Run()
			if (runErr == nil) != (refRunErr == nil) || out.String() != refOut.String() || errs.String() != refErr.String() {
				t.Fatalf("interpreter: error=%v stdout=%q stderr=%q\nreference: error=%v stdout=%q stderr=%q", runErr, out.String(), errs.String(), refRunErr, refOut.String(), refErr.String())
			}
		})
	}
}

func TestLetRawKeyRejection(t *testing.T) {
	cases := []struct{ name, src string }{
		{"empty", `shopt -s assoc_expand_once; declare -A m; let 'm[]=9'; printf '%s:%s' "$?" "${#m[@]}"`},
		{"extra-close", `shopt -s assoc_expand_once; declare -A m; let 'm[x]]=9'; printf '%s:%s' "$?" "${#m[@]}"`},
		{"nested-index", `shopt -s assoc_expand_once; declare -A m; declare -a a; k="o'clock"; let "a[m[$k]]=9"; printf '%s:%s' "$?" "${#a[@]}"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := syntax.NewParser().Parse(strings.NewReader(tc.src), "case.sh")
			if err != nil {
				t.Fatal(err)
			}
			var out, errs bytes.Buffer
			r, err := New(Dir(t.TempDir()), StdIO(nil, &out, &errs), WithBashCompatErrors(true))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := r.Run(ctx, f); err != nil {
				t.Fatal(err)
			}
			if out.String() != "1:0" || errs.Len() == 0 {
				t.Fatalf("stdout=%q stderr=%q", out.String(), errs.String())
			}
		})
	}
}
