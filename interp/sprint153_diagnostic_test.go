package interp_test

// Sprint: #153; Story: S153.4; Story-ID: e58cccba74f8
import (
	"strings"
	"testing"
)

// A refusal recorded inside a callee is the program's outcome wherever the
// callee was called from. A dependency operation the bridge refuses has no
// native counterpart, so this is pinned by the diagnostic itself rather than
// by a native oracle: the same refusal must surface whether the refusing
// function is called as a statement, as an assignment's producer, or as the
// single call a `return` forwards — the last used to come back as a bare
// "exit status 2".
func TestSprint153FatalDiagnosticSurvivesReturnCall(t *testing.T) {
	const body = `
type Stream struct{}

func Pipe(s Stream) Stream {
	it := func(fn func(int) bool) {}
	fmt.Println(reflect.TypeOf(it).String())
	return Stream{}
}

func Forward(s Stream) Stream { return Pipe(s) }

func Nested(s Stream) Stream { return Forward(s) }
`
	for name, main := range map[string]string{
		"statement":      `Pipe(Stream{})`,
		"assignment":     `s := Stream{}; s = Pipe(s); _ = s`,
		"return_call":    `s := Stream{}; s = Forward(s); _ = s`,
		"return_nested":  `Nested(Stream{})`,
		"return_in_main": `_ = Nested(Stream{})`,
	} {
		t.Run(name, func(t *testing.T) {
			source := "package main\n\nimport (\n\t\"fmt\"\n\t\"reflect\"\n)\n" + body + "\nfunc main() {\n\t" + main + "\n}\n"
			got := runGoSourceRunnerError(t, source)
			if !strings.Contains(got, "retained original function callbacks are unsupported for reflect.TypeOf") {
				t.Fatalf("diagnostic lost: %q", got)
			}
		})
	}
}
