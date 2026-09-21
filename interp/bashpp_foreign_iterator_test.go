//go:build full

package interp_test

import (
	"strings"
	"testing"
)

func TestPythonIteratorLanguage(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		fail             bool
	}{
		{"empty", "    if False: yield 0", "", false},
		{"many", "    for n in range(1000): yield n", "1000\n", false},
		{"partial-error", "    yield 3\n    raise ValueError('after-value')", "3\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loop := "for value := range values { echo \"$value\"; }"
			if tc.name == "many" {
				loop = "count := 0\nfor value := range values { count = count + 1; }\necho \"$count\""
			}
			source := "~~~python\ndef items() -> Iterator[int]:\n" + tc.body + "\n~~~\nfunc main() {\nvalues := items()\n" + loop + "\n}\nmain()\n"
			out, stderr, err := runPolyglot(t, source)
			if out != tc.want || (err != nil) != tc.fail {
				t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
			}
			if tc.fail && !strings.Contains(stderr, "after-value") {
				t.Fatal(stderr)
			}
		})
	}
}

func TestPythonIteratorPipeline(t *testing.T) {
	out, stderr, err := runPolyglot(t, `~~~python as py
def values() -> Iterator[int]:
    yield 1
    yield 2
~~~
py.values | cat
`)
	if err != nil || stderr != "" || out != "1\n2\n" {
		t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
	}
}

func TestPythonTextIOFilterLanguage(t *testing.T) {
	out, stderr, err := runPolyglot(t, `~~~python as py
def upper(stdin: TextIO) -> Iterator[str]:
    for line in stdin:
        yield line.upper()
~~~
printf 'one\ntwo\n' | py.upper | cat
`)
	if err != nil || stderr != "" || out != "ONE\nTWO\n" {
		t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
	}
}

// CPython v3.14.0, Lib/test/test_generators.py GeneratorTest.test_issue103488
// (PSF-2.0): the original generator body is `yield; raise ValueError()`.
// Preserve that body; add only the bridge's declared Iterator[Any] annotation.
func TestPythonIteratorOriginalGeneratorFailure(t *testing.T) {
	out, stderr, err := runPolyglot(t, `~~~python
def gen_raises() -> Iterator[Any]:
    yield
    raise ValueError()
~~~
func main() {
 values := gen_raises()
 for value := range values { echo seen; }
}
main()
`)
	if out != "seen\n" || err == nil || !strings.Contains(stderr, "ValueError") {
		t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
	}
}

func TestPythonIteratorEarlyBreak(t *testing.T) {
	out, stderr, err := runPolyglot(t, `~~~python
def forever() -> Iterator[int]:
    while True:
        yield 42
~~~
func main() {
 values := forever()
 for value := range values { echo "$value"; break; }
 echo done
}
main()
`)
	if out != "42\ndone\n" || stderr != "" || err != nil {
		t.Fatalf("out=%q stderr=%q err=%v", out, stderr, err)
	}
}
