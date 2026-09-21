//go:build full

package lower_test

import "testing"

func TestPythonIteratorLowered(t *testing.T) {
	source := `~~~python
def items() -> Iterator[int]:
    yield 1
    yield 2
~~~
func main() {
 values := items()
 for value := range values { echo "$value"; }
}
main()
`
	out := testPythonFenceInterpretedNativeParityAt(t, source, "input.bpp")
	if out != "1\n2\n" {
		t.Fatalf("out=%q", out)
	}
}

func TestPythonTextIOFilterLowered(t *testing.T) {
	source := `~~~python as py
def upper(stdin: TextIO) -> Iterator[str]:
    for line in stdin:
        yield line.upper()
~~~
printf 'one\ntwo\n' | py.upper | cat
`
	out := testPythonFenceInterpretedNativeParityAt(t, source, "input.bpp")
	if out != "ONE\nTWO\n" {
		t.Fatalf("out=%q", out)
	}
}
