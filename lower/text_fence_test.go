//go:build full

package lower_test

import (
	"strings"
	"testing"

	"mvdan.cc/sh/v3/lower"
	"mvdan.cc/sh/v3/polyglot"
)

// A built-in text row lowers: the body is embedded verbatim in the plan, the
// verb table is the export set (with its effect atoms) and the runtime is
// the row's lowered literal. A runner fence does not lower, and says so.
func TestLowerTextRowAndRunnerFence(t *testing.T) {
	polyglot.RegisterLanguage(polyglot.Language{
		Canonical: "fakecfg2",
		NewRuntime: func(cfg polyglot.RuntimeConfig) polyglot.LanguageRuntime {
			return polyglot.Text{Type: "fakecfg2", FileName: "fake.cfg", Tool: "fake-tool",
				Verbs: []polyglot.Verb{{Name: "show", Args: []string{"show", "{file}"}}, {Name: "apply", Args: []string{"apply", "{file}"}, Effects: []string{"world"}}}}
		},
		LoweredRuntime: func(prefix, _ string) string {
			return polyglot.Text{Type: "fakecfg2", FileName: "fake.cfg", Tool: "fake-tool",
				Verbs: []polyglot.Verb{{Name: "show", Args: []string{"show", "{file}"}}, {Name: "apply", Args: []string{"apply", "{file}"}, Effects: []string{"world"}}}}.LoweredLiteral(prefix)
		},
	})
	file := parse(t, "~~~fakecfg2 as cfg\nk = v\n~~~\nout := cfg.show()\necho \"$out\"\n", "text.bpp")
	result, err := lower.Compile(file, lower.Options{})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	generated := string(result.Source)
	// The generated source is gofmt'd, so the literals carry gofmt's spacing.
	for _, want := range []string{
		`polyglot.Text{Type: "fakecfg2", FileName: "fake.cfg", Tool: "fake-tool", Verbs: []`,
		`{Name: "apply", Args: []string{"apply", "{file}"}, Effects: []string{"world"}, Result: ""}`,
		`Source: "k = v\n"`,
		`Effects: []string{"world"}}`,
		`Variadic: true`,
		`Runner: ""`,
	} {
		if !strings.Contains(generated, want) {
			t.Errorf("generated source lacks %q:\n%s", want, generated)
		}
	}
	// No alias: refused.
	if _, err := lower.Compile(parse(t, "~~~fakecfg2\nk = v\n~~~\n", "text.bpp"), lower.Options{}); err == nil || !strings.Contains(err.Error(), "needs an alias") {
		t.Errorf("unaliased text row: %v", err)
	}
	// A runner fence is interpreted only.
	_, err = lower.Compile(parse(t, "r() { :; }\n~~~notes as n !r\nx\n~~~\n", "runner.bpp"), lower.Options{})
	if err == nil || !strings.Contains(err.Error(), "runner fences run interpreted") {
		t.Errorf("runner fence lowering: %v", err)
	}
}
