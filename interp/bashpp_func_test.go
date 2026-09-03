package interp_test

import (
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func runBashPPFunc(t *testing.T, src string) string {
	t.Helper()
	var out strings.Builder
	r := bashPPRunner(t, &out, interp.Lang(syntax.LangBashPP))
	bashPPRun(t, r, src)
	return out.String()
}

func TestBashPPFuncRuntimeProbe(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"typed params and result", "func pick(a int, b string) int {\n return a\n}\nx := pick(7, hello)\necho \"x=$x\"\n", "x=7\n"},
		{"multiple results", "func pair(a, b int) (x, y int) {\n x=a\n y=b\n return\n}\nx, y := pair(1, 2)\necho \"$x:$y\"\n", "1:2\n"},
		{"named result", "func named() (n int) {\n n=5\n return\n}\nx := named()\necho \"x=$x\"\n", "x=5\n"},
		{"closure observes live cell", "var prefix = 1\nfunc f() int {\n return prefix\n}\nprefix=2\nx := f()\necho \"x=$x\"\n", "x=2\n"},
		{"recursion", "func down(n int) {\n if [ \"$n\" -gt 0 ]; then\n  echo $n\n  down($((n-1)))\n fi\n}\ndown(3)\n", "3\n2\n1\n"},
		{"defer order", "func g(v) {\n echo \"g:$v\"\n}\nfunc f() {\n defer g(1)\n defer g(2)\n}\nf()\n", "g:2\ng:1\n"},
		{"defer captures and propagates failure", "func bad() {\n return 7\n}\nfunc f() int {\n n=1\n defer bad()\n n=2\n return n\n}\nx := f()\necho \"x=$x status=$?\"\n", "x=2 status=7\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			qt.Assert(t, qt.Equals(runBashPPFunc(t, tc.src), tc.want))
		})
	}
}
