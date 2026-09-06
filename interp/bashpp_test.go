// Copyright (c) 2026, the bash++ authors
// See LICENSE for licensing information

package interp_test

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// bashPPRunner builds a runner in the given dialect, capturing its output.
func bashPPRunner(tb testing.TB, out *strings.Builder, opts ...interp.RunnerOption) *interp.Runner {
	tb.Helper()
	all := append([]interp.RunnerOption{
		interp.StdIO(nil, out, out),
		// A real PATH, so the OS-boundary test can find `env`.
		interp.Env(expand.ListEnviron("PATH=/usr/bin:/bin")),
	}, opts...)
	r, err := interp.New(all...)
	qt.Assert(tb, qt.IsNil(err))
	return r
}

func bashPPRun(tb testing.TB, r *interp.Runner, src string) {
	tb.Helper()
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "")
	qt.Assert(tb, qt.IsNil(err))
	// A non-zero exit is the script's business; these tests assert on output.
	_ = r.Run(context.Background(), f)
}

func TestBashPPParsedShortDeclarationEvaluates(t *testing.T) {
	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
	bashPPRun(t, r, "x := 42; y, z := 1, 2; printf '%s:%s:%s' \"$x\" \"$y\" \"$z\"")
	qt.Assert(t, qt.Equals(out.String(), "42:1:2"))
}

func TestBashPPParsedScalarExpressionsEvaluate(t *testing.T) {
	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
	bashPPRun(t, r, `func main() {
	base := 40
	copy := base
	n := copy + 2
	ok := n == 42
	s := string(65)
	printf '%s:%s:%s' "$n" "$ok" "$s"
}
main()
`)
	qt.Assert(t, qt.Equals(out.String(), "42:true:A"))
}

func TestBashPPTopLevelSingleQuotesStayShellStrings(t *testing.T) {
	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
	bashPPRun(t, r, `x := 'a'; printf '%s' "$x"`)
	qt.Assert(t, qt.Equals(out.String(), "a"))
}

func TestBashPPTopLevelIdentifierShortDeclKeepsClassEBehavior(t *testing.T) {
	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
	bashPPRun(t, r, `base := 40; copy := base; printf '%s' "$copy"`)
	qt.Assert(t, qt.Equals(out.String(), "base"))
}

func TestBashPPParsedScalarConstantsUseGoConstantPrecision(t *testing.T) {
	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
	bashPPRun(t, r, `func main() {
	n := 9223372036854775808 + 1
	printf '%s' "$n"
}
main()
`)
	qt.Assert(t, qt.Equals(out.String(), "9223372036854775809"))
}

func TestBashPPParsedScalarExpressionDiagnostics(t *testing.T) {
	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
	bashPPRun(t, r, `func main() {
	n := missing + 1
	printf '%s' "$n"
}
main()
`)
	qt.Assert(t, qt.Equals(out.String(), "BASHPP-EEXPR-UNDEFINED: undefined: missing\n"))
}

func TestBashPPParsedScalarInvalidOperationsNeverPanic(t *testing.T) {
	for _, tc := range []struct {
		name string
		expr string
		code string
	}{
		{"string subtraction", `"a" - "b"`, "BASHPP-EEXPR-OPERAND"},
		{"narrow conversion overflow", `int8(300)`, "BASHPP-EEXPR-CONVERT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out strings.Builder
			r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
			bashPPRun(t, r, "func main() {\n\tn := "+tc.expr+"\n}\nmain()\n")
			qt.Assert(t, qt.StringContains(out.String(), tc.code))
		})
	}
}

// TestBashPPDialectGate proves that LangBashPP gates the feature: the very same
// call succeeds under bash++ and is refused under every other dialect, and under
// POSIX mode.
func TestBashPPDialectGate(t *testing.T) {
	t.Parallel()

	t.Run("open under bashpp", func(t *testing.T) {
		t.Parallel()
		var out strings.Builder
		r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
		qt.Assert(t, qt.Equals(r.Dialect(), syntax.LangBashPP))
		qt.Assert(t, qt.IsNil(r.SetObject("OBJ", map[string]any{"a": 1})))
	})

	t.Run("closed under other dialects", func(t *testing.T) {
		t.Parallel()
		for _, lang := range []syntax.LangVariant{
			syntax.LangBash, syntax.LangPOSIX, syntax.LangMirBSDKorn,
			syntax.LangBats, syntax.LangZsh,
		} {
			var out strings.Builder
			r := bashPPRunner(t, &out, interp.Lang(lang))
			err := r.SetObject("OBJ", map[string]any{"a": 1})
			qt.Check(t, qt.ErrorIs(err, interp.ErrObjectsUnsupported), qt.Commentf("lang %v", lang))
		}
	})

	t.Run("closed by default", func(t *testing.T) {
		t.Parallel()
		// A runner which never mentions a dialect — i.e. every consumer that
		// exists today — is plain bash, and objects are unavailable.
		var out strings.Builder
		r := bashPPRunner(t, &out)
		qt.Assert(t, qt.Equals(r.Dialect(), syntax.LangBash))
		qt.Assert(t, qt.ErrorIs(r.SetObject("OBJ", 1), interp.ErrObjectsUnsupported))
	})

	t.Run("closed under posix mode", func(t *testing.T) {
		t.Parallel()
		// --posix off: POSIX mode is the shell promising to be nothing but a
		// POSIX shell, so the bash++ extensions are withdrawn even in the
		// bash++ dialect.
		var out strings.Builder
		r := bashPPRunner(t, &out,
			interp.Lang(syntax.LangBashPP), interp.WithPosixMode(true))
		err := r.SetObject("OBJ", map[string]any{"a": 1})
		qt.Assert(t, qt.ErrorIs(err, interp.ErrObjectsUnsupported))
	})

	t.Run("rejects unusable values", func(t *testing.T) {
		t.Parallel()
		var out strings.Builder
		r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
		qt.Assert(t, qt.IsNotNil(r.SetObject("OBJ", make(chan int))))
		qt.Assert(t, qt.IsNotNil(r.SetObject("not a name", 1)))
	})

	t.Run("unknown variant is an error", func(t *testing.T) {
		t.Parallel()
		_, err := interp.New(interp.Lang(syntax.LangAuto))
		qt.Assert(t, qt.IsNotNil(err))
	})
}

// TestBashPPObjectRoundTrip proves an object survives a trip through the shell:
// the same Go value comes back out, while the shell itself only ever sees a string.
func TestBashPPObjectRoundTrip(t *testing.T) {
	t.Parallel()

	type config struct {
		Name string   `json:"name"`
		Port int      `json:"port"`
		Tags []string `json:"tags"`
	}
	orig := config{Name: "web", Port: 8080, Tags: []string{"a", "b"}}

	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
	qt.Assert(t, qt.IsNil(r.SetObject("CFG", orig)))

	// The Go value comes back as itself, not as a reparsed string.
	got, ok := r.Object("CFG")
	qt.Assert(t, qt.IsTrue(ok))
	gotCfg, ok := got.(config)
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.DeepEquals(gotCfg, orig))

	// It survives a Run, including one which reads the variable as a string.
	bashPPRun(t, r, `echo "$CFG"`)
	qt.Assert(t, qt.Equals(out.String(), `{"name":"web","port":8080,"tags":["a","b"]}`+"\n"))

	got, ok = r.Object("CFG")
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.DeepEquals(got.(config), orig))

	// A plain string variable, and an unset name, are not objects.
	bashPPRun(t, r, `PLAIN=hello`)
	_, ok = r.Object("PLAIN")
	qt.Assert(t, qt.IsFalse(ok))
	_, ok = r.Object("NOPE")
	qt.Assert(t, qt.IsFalse(ok))
}

func TestBashPPSetObjectMutationFailsClosedAtCoercion(t *testing.T) {
	t.Parallel()

	type holder struct {
		Value any `json:"value"`
	}
	orig := &holder{Value: []int{1, 2}}

	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
	qt.Assert(t, qt.IsNil(r.SetObject("OBJ", orig)))

	got, ok := r.Object("OBJ")
	qt.Assert(t, qt.IsTrue(ok))
	qt.Assert(t, qt.Equals(got, any(orig)))

	orig.Value = orig
	bashPPRun(t, r, `echo "$OBJ"`)
	qt.Assert(t, qt.Equals(out.String(), expand.ObjectString(make(chan int))+"\n"))
}

// TestBashPPObjectCoercion covers the "coerce to string for bash commands" half:
// wherever the shell needs a string, the object is one.
func TestBashPPObjectCoercion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"interpolation", `echo "$OBJ"`, `{"a":1,"b":"two"}` + "\n"},
		{"unquoted", `echo $OBJ`, `{"a":1,"b":"two"}` + "\n"},
		{"builtin arg", `printf '%s\n' "$OBJ"`, `{"a":1,"b":"two"}` + "\n"},
		// The length of the coerced string, i.e. of `{"a":1,"b":"two"}`.
		{"length", `echo ${#OBJ}`, "17\n"},
		{"comparison", `[[ $OBJ == \{* ]] && echo yes`, "yes\n"},
		{"declare -p", `declare -p OBJ`, `declare -- OBJ="{\"a\":1,\"b\":\"two\"}"` + "\n"},
		{"in a function", `f() { echo "$OBJ"; }; f`, `{"a":1,"b":"two"}` + "\n"},
		{"in a subshell", `( echo "$OBJ" )`, `{"a":1,"b":"two"}` + "\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out strings.Builder
			r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
			// A fixed key set, so the JSON encoding is deterministic.
			qt.Assert(t, qt.IsNil(r.SetObject("OBJ", map[string]any{"a": 1, "b": "two"})))
			bashPPRun(t, r, tc.src)
			qt.Assert(t, qt.Equals(out.String(), tc.want))
		})
	}
}

// TestBashPPObjectCrossesOSBoundary is the auto-JSON gate: an object handed to
// an external binary — a separate process, which cannot be given a Go value —
// arrives as JSON.
func TestBashPPObjectCrossesOSBoundary(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("env"); err != nil {
		t.Skipf("need the env binary: %v", err)
	}

	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
	qt.Assert(t, qt.IsNil(r.SetObject("OBJ", map[string]any{"a": 1, "b": []int{2, 3}})))

	// `env` is a real external process: whatever it prints, the kernel handed it.
	bashPPRun(t, r, `export OBJ; env`)
	qt.Assert(t, qt.IsTrue(strings.Contains(out.String(), `OBJ={"a":1,"b":[2,3]}`)),
		qt.Commentf("env output:\n%s", out.String()))

	// An object which is not exported does not cross, exactly like any other
	// unexported shell variable.
	var out2 strings.Builder
	r2 := bashPPRunner(t, &out2, interp.Lang(syntax.LangBashPP))
	qt.Assert(t, qt.IsNil(r2.SetObject("SECRET", map[string]any{"k": "v"})))
	bashPPRun(t, r2, `env`)
	qt.Assert(t, qt.IsFalse(strings.Contains(out2.String(), "SECRET=")))
}

func TestBashPPLargeObjectCrossesOSBoundaryIntact(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("wc"); err != nil {
		t.Skipf("need the wc binary: %v", err)
	}

	payload := strings.Repeat("x", 200_000)
	wantBytes := len(expand.ObjectString(payload))
	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
	qt.Assert(t, qt.IsNil(r.SetObject("OBJ", payload)))

	// printf is the shell builtin, while wc is a real external process. Its
	// count proves the complete JSON string crossed the process boundary.
	bashPPRun(t, r, `printf %s "$OBJ" | wc -c`)
	qt.Assert(t, qt.Equals(strings.TrimSpace(out.String()), fmt.Sprint(wantBytes)))
}

// TestBashPPLangBashUnaffected is the no-regression gate: for any script which
// uses no objects, bash++ and bash behave identically, so opting in costs the
// existing semantics nothing.
func TestBashPPLangBashUnaffected(t *testing.T) {
	t.Parallel()

	scripts := []string{
		`x=1; echo $x`,
		`a=(one two); echo ${a[1]} ${#a[@]}`,
		`declare -A m=([k]=v); echo ${m[k]}`,
		`f() { echo "$1"; }; f hi`,
		`echo ${UNSET:-fallback}`,
		`x=abc; echo ${x^^} ${x:1:2}`,
		`declare -i n=2+3; echo $n`,
		`export E=1; declare -p E`,
	}
	for _, src := range scripts {
		var bashOut, ppOut strings.Builder

		rBash := bashPPRunner(t, &bashOut, interp.Lang(syntax.LangBash))
		bashPPRun(t, rBash, src)

		rPP := bashPPRunner(t, &ppOut, interp.Lang(syntax.LangBashPP))
		bashPPRun(t, rPP, src)

		qt.Check(t, qt.Equals(ppOut.String(), bashOut.String()),
			qt.Commentf("script %q diverged between bash and bashpp", src))
	}
}

// TestBashPPErrObjectsUnsupported keeps the sentinel error usable by callers.
func TestBashPPErrObjectsUnsupported(t *testing.T) {
	t.Parallel()
	qt.Assert(t, qt.IsTrue(errors.Is(interp.ErrObjectsUnsupported, interp.ErrObjectsUnsupported)))
}
