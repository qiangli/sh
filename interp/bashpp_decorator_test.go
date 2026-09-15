// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func runDecorated(t *testing.T, src string, opts ...interp.RunnerOption) (stdout, stderr string, err error) {
	t.Helper()
	f, parseErr := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "deco.bpp")
	qt.Assert(t, qt.IsNil(parseErr))
	var out, errs strings.Builder
	all := append([]interp.RunnerOption{interp.Lang(syntax.LangBashPP)}, opts...)
	r := bashPPRunner(t, &out, append(all, interp.StdIO(nil, &out, &errs))...)
	err = r.Run(context.Background(), f)
	return out.String(), errs.String(), err
}

func TestBashPPDecoratorTrace(t *testing.T) {
	const src = `func trace(c *Call) {
	name := c.Name
	echo "-> $name"
	c.Next()
	st := c.Status
	echo "<- $name status=$st"
}
@trace()
func deploy(env string) {
	echo "deploying $env"
}
deploy(prod)
`
	out, stderr, err := runDecorated(t, src)
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(out, "-> deploy\ndeploying prod\n<- deploy status=0\n"))
}

func TestBashPPDecoratorScripts(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{
			"stack is outermost first, per-call args, late resolution",
			`@tag(x)
@tag(y)
func f() { echo body; }
func tag(c *Call, label string) {
	echo "in $label"
	c.Next()
	echo "out $label"
}
f()
`,
			"in x\nin y\nbody\nout y\nout x\n",
		},
		{
			"arguments evaluate per invocation in the captured scope",
			`n := 1
func tag(c *Call, label string) { echo "tag $label"; c.Next(); }
@tag($n)
func f() { :; }
f()
n=2
f()
`,
			"tag 1\ntag 2\n",
		},
		{
			"keyword and default arguments",
			`func retry(c *Call, n int = 1, backoff string = "0") {
	i := 0
	while [ "$i" -lt "$n" ]; do
		c.Next()
		st := c.Status
		if [ "$st" -eq 0 ]; then return; fi
		i=$((i+1))
	done
}
attempts=0
@retry(n: 3)
func flaky() {
	attempts=$((attempts+1))
	echo "attempt $attempts"
	[ "$attempts" -ge 2 ]
}
flaky()
echo "status=$? attempts=$attempts"
`,
			"attempt 1\nattempt 2\nstatus=0 attempts=2\n",
		},
		{
			"results travel through the context",
			`func trace(c *Call) {
	c.Next()
	r := c.Results[0]
	echo "saw $r"
}
@trace()
func pick(a, b int) int { return $b }
x := pick(1, 7)
echo "x=$x"
`,
			"saw 7\nx=7\n",
		},
		{
			"result rewrite",
			`func upper(c *Call) {
	c.Next()
	c.Results = []string{"REWRITTEN"}
}
@upper()
func greet() string { return hello }
x := greet()
echo "x=$x"
`,
			"x=REWRITTEN\n",
		},
		{
			"skip denies the body and sets status",
			`func deny(c *Call) {
	echo "denied"
	c.Status = 3
}
@deny()
func secret() int { echo leaked; return 1 }
x := secret()
echo "x=<$x> status=$?"
`,
			"denied\nx=<0> status=3\n",
		},
		{
			"args are the bound parameters",
			`func show(c *Call) {
	args := c.Args
	n := len(args)
	a := c.Args[0]
	b := c.Args[1]
	echo "args=$a $b n=$n"
	c.Next()
}
@show()
func f(a string, b int) { :; }
f(x, 2)
`,
			"args=x 2 n=2\n",
		},
		{
			"shell function sees $@ and status",
			`func trace(c *Call) {
	name := c.Name
	a := c.Args[0]
	echo "-> $name $a"
	c.Next()
	st := c.Status
	echo "<- $name status=$st"
}
@trace()
function backup() { echo "backing up $1"; return 4; }
backup disk
echo "status=$?"
`,
			"-> backup disk\nbacking up disk\n<- backup status=4\nstatus=4\n",
		},
		{
			"shell function posix spelling and decorator status rewrite",
			`func fix(c *Call) { c.Next(); c.Status = 0; }
@fix()
b() { false; }
b
echo "status=$?"
`,
			"status=0\n",
		},
		{
			"method and indirect routes",
			`type T int
func trace(c *Call) { name := c.Name; echo "trace $name"; c.Next(); }
@trace()
func (v T) Show() { echo "show" }
type I interface { Show() }
var v T = 1
var i I = v
f := v.Show
v.Show()
f()
i.Show()
`,
			"trace T.Show\nshow\ntrace T.Show\nshow\ntrace T.Show\nshow\n",
		},
		{
			"function value keeps the decoration",
			`func trace(c *Call) { echo "traced"; c.Next(); }
@trace()
func f() { echo body }
g := f
g()
`,
			"traced\nbody\n",
		},
		{
			"decorator frames are visible in FUNCNAME",
			`func trace(c *Call) { c.Next(); }
@trace()
function f() { echo "${FUNCNAME[0]} ${FUNCNAME[1]}"; }
f
`,
			"f trace\n",
		},
		{
			"agentic declaration runs inside its scope and reports Agentic",
			`func trace(c *Call) { a := c.Agentic; echo "agentic=$a"; c.Next(); }
@trace()
agentic func act() { echo acted }
agentic { act(); }
`,
			"agentic=true\nacted\n",
		},
		{
			"recursion re-enters the chain",
			`func trace(c *Call) { n := c.Args[0]; echo "enter $n"; c.Next(); }
@trace()
func down(n int) { if [ "$n" -gt 0 ]; then down($((n-1))); fi }
down(2)
`,
			"enter 2\nenter 1\nenter 0\n",
		},
		{
			"body defers run when the target returns",
			`func trace(c *Call) { c.Next(); echo "after next"; }
func cleanup() { echo cleanup }
@trace()
func f() { defer cleanup(); echo body }
f()
echo done
`,
			"body\nafter next\ncleanup\ndone\n",
		},
		{
			"redefinition drops the decoration",
			`func trace(c *Call) { echo traced; c.Next(); }
@trace()
function f() { echo one; }
f
f() { echo two; }
f
`,
			"traced\none\ntwo\n",
		},
		{
			"unset -f drops the decoration",
			`func trace(c *Call) { echo traced; c.Next(); }
@trace()
function f() { echo one; }
unset -f f
f() { echo two; }
f
`,
			"two\n",
		},
		{
			"declare -f prints source decorators",
			`func trace(c *Call) { c.Next(); }
@trace(level: 2)
function f() { echo one; }
declare -f f
`,
			"@trace(level: 2)\nf () \n{ \n    echo one\n}\n",
		},
		{
			"subshell inherits the table",
			`func trace(c *Call) { echo traced; c.Next(); }
@trace()
function f() { echo body; }
( f )
`,
			"traced\nbody\n",
		},
		{
			"user Call type shadows the predeclared one",
			`type Call struct { Name string }
func mine(c *Call) { n := c.Name; echo "mine $n"; }
func main() {
	v := Call{Name: "shadowed"}
	p := &v
	mine(p)
}
main()
`,
			"mine shadowed\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, err := runDecorated(t, tc.src)
			qt.Assert(t, qt.Equals(stderr, ""))
			qt.Assert(t, qt.IsNil(err))
			qt.Assert(t, qt.Equals(out, tc.want))
		})
	}
}

func TestBashPPDecoratorDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"undefined", "@missing()\nfunc f() { echo leaked }\nf()\n", "BASHPP-EDECO-UNDEF"},
		{"shell function is not a decorator", "d() { :; }\n@d()\nfunc f() { echo leaked }\nf()\n", "BASHPP-EDECO-SIG"},
		{"wrong first parameter", "func d(n int) { :; }\n@d()\nfunc f() { echo leaked }\nf()\n", "BASHPP-EDECO-SIG"},
		{"shadowed Call is not the predeclared one", "type Call struct { X int }\nfunc d(c *Call) { :; }\n@d()\nfunc f() { echo leaked }\nf()\n", "BASHPP-EDECO-SIG"},
		{"self", "@d()\nfunc d(c *Call) { c.Next(); }\n", "BASHPP-EDECO-SELF"},
		{"reserved namespace", "@ns.d()\nfunc f() { :; }\n", "BASHPP-EDECO-RESERVED"},
		{"cycle", "@b()\nfunc a(c *Call) { c.Next(); }\n@a()\nfunc b(c *Call) { c.Next(); }\n@a()\nfunc f() { echo leaked }\nf()\n", "BASHPP-EDECO-CYCLE"},
		{"result count", "func d(c *Call) { c.Next(); c.Results = []string{\"a\", \"b\"}; }\n@d()\nfunc f() int { return 1 }\nx := f()\n", "BASHPP-EDECO-RESULT"},
		{"result type", "func d(c *Call) { c.Next(); c.Results = []string{\"nope\"}; }\n@d()\nfunc f() int { return 1 }\nx := f()\n", "BASHPP-EDECO-RESULT"},
		{"results for a result-less function", "func d(c *Call) { c.Next(); c.Results = []string{\"x\"}; }\n@d()\nfunc f() { :; }\nf()\n", "BASHPP-EDECO-RESULT"},
		{"export -f refuses", "func d(c *Call) { c.Next(); }\n@d()\nfunction f() { :; }\nexport -f f\n", "cannot export"},
		{"agentic gate stays first", "func d(c *Call) { echo leaked; c.Next(); }\n@d()\nagentic func f() { :; }\nf()\n", "requires an explicit agentic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, err := runDecorated(t, tc.src)
			qt.Assert(t, qt.IsNotNil(err))
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stderr %q does not mention %q", stderr, tc.want)
			}
			qt.Assert(t, qt.IsFalse(strings.Contains(out, "leaked")))
		})
	}
}

func TestBashPPDecoratorNative(t *testing.T) {
	var seen []string
	natives := map[string]interp.DecoratorFunc{
		"trace": func(ctx context.Context, c *interp.Call, args []interp.DecoratorArg) error {
			seen = append(seen, "enter "+c.Name+" "+strings.Join(c.Args, ","))
			for _, a := range args {
				seen = append(seen, "arg "+a.Name+"="+a.Value)
			}
			c.Next(ctx)
			seen = append(seen, "leave "+c.Name+" status="+itoa(c.Status)+" results="+strings.Join(c.Results, ","))
			return nil
		},
		"deny": func(ctx context.Context, c *interp.Call, args []interp.DecoratorArg) error {
			c.Status = 77
			return nil
		},
		"boom": func(ctx context.Context, c *interp.Call, args []interp.DecoratorArg) error {
			return errBoom
		},
	}
	t.Run("native wraps typed and shell functions", func(t *testing.T) {
		seen = nil
		out, stderr, err := runDecorated(t, `@trace(level: 2)
func pick(a int) int { return $a }
x := pick(5)
echo "x=$x"
@trace()
function sh() { return 3; }
sh a b
echo "status=$?"
`, interp.Decorators(natives))
		qt.Assert(t, qt.Equals(stderr, ""))
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.Equals(out, "x=5\nstatus=3\n"))
		qt.Assert(t, qt.DeepEquals(seen, []string{
			"enter pick 5", "arg level=2", "leave pick status=0 results=5",
			"enter sh a,b", "leave sh status=3 results=",
		}))
	})
	t.Run("user function shadows a native", func(t *testing.T) {
		seen = nil
		out, _, err := runDecorated(t, "func trace(c *Call) { echo scripted; c.Next(); }\n@trace()\nfunc f() { echo body }\nf()\n", interp.Decorators(natives))
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.Equals(out, "scripted\nbody\n"))
		qt.Assert(t, qt.HasLen(seen, 0))
	})
	t.Run("native deny and error", func(t *testing.T) {
		out, _, err := runDecorated(t, "@deny()\nfunc f() { echo leaked }\nf()\necho \"status=$?\"\n", interp.Decorators(natives))
		qt.Assert(t, qt.IsNil(err))
		qt.Assert(t, qt.Equals(out, "status=77\n"))
		out, stderr, err := runDecorated(t, "@boom()\nfunc f() { echo leaked }\nf()\n", interp.Decorators(natives))
		qt.Assert(t, qt.IsNotNil(err))
		qt.Assert(t, qt.Equals(out, ""))
		qt.Assert(t, qt.IsTrue(strings.Contains(stderr, "BASHPP-EDECO-NATIVE")))
	})
}

func TestBashPPDecoratorAdvice(t *testing.T) {
	var log []string
	natives := map[string]interp.DecoratorFunc{
		"audit": func(ctx context.Context, c *interp.Call, args []interp.DecoratorArg) error {
			log = append(log, "audit "+c.Name+" advised="+c.Advised)
			c.Next(ctx)
			return nil
		},
	}
	advice := func(name, file string, agentic bool) []interp.DecoratorSpec {
		if !strings.HasPrefix(name, "pay") {
			return nil
		}
		// Duplicated rule ids collapse to one application.
		return []interp.DecoratorSpec{{ID: "r1", Name: "audit"}, {ID: "r1", Name: "audit"}}
	}
	log = nil
	out, stderr, err := runDecorated(t, `func trace(c *Call) { echo "trace in"; c.Next(); echo "trace out"; }
@trace()
func pay() { echo paying }
function payout() { echo "paying out"; }
func other() { echo other }
pay()
payout
other()
eval 'payout() { echo "paying out again"; }'
payout
declare -f payout
`, interp.Decorators(natives), interp.Advice(advice))
	qt.Assert(t, qt.Equals(stderr, ""))
	qt.Assert(t, qt.IsNil(err))
	qt.Assert(t, qt.Equals(out, "trace in\npaying\ntrace out\npaying out\nother\npaying out again\npayout () \n{ \n    echo \"paying out again\"\n}\n"))
	// Advice is outermost: the audit entry precedes the source trace, and
	// re-registration via eval applied it exactly once more.
	qt.Assert(t, qt.DeepEquals(log, []string{"audit pay advised=r1", "audit payout advised=r1", "audit payout advised=r1"}))
	_, _, err = runDecorated(t, "function payout() { :; }\nexport -f payout\n", interp.Decorators(natives), interp.Advice(advice))
	qt.Assert(t, qt.IsNotNil(err))
}

var errBoom = errors.New("boom")

func itoa(n int) string { return strconv.Itoa(n) }
