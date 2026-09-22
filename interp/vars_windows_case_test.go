package interp

import (
	"testing"

	"mvdan.cc/sh/v3/expand"
)

// Windows folds a variable name only onto an INHERITED environment variable;
// names a script introduces stay case-sensitive, as in bash on every OS.
func TestOverlayEnvironNormalizeWindowsMode(t *testing.T) {
	base := expand.ListEnviron("MIXEDCASE_INTERP_GLOBAL=value", "PATH=x")
	o := &overlayEnviron{parent: base, values: map[string]namedVariable{}}
	cases := map[string]string{
		"MIXEDCASE_interp_global": "MIXEDCASE_INTERP_GLOBAL",
		"path":                    "PATH",
		"PATH":                    "PATH",
		"a":                       "a",
		"A":                       "A",
		"Foo":                     "Foo",
	}
	for in, want := range cases {
		if got := o.normalizeMode(in, true); got != want {
			t.Errorf("normalizeMode(%q, windows) = %q, want %q", in, got, want)
		}
		if got := o.normalizeMode(in, false); got != in {
			t.Errorf("normalizeMode(%q, unix) = %q, want identity", in, got)
		}
	}
	// A nested overlay reaches the same root.
	child := &overlayEnviron{parent: o, values: map[string]namedVariable{}}
	if got := child.normalizeMode("mixedcase_interp_global", true); got != "MIXEDCASE_INTERP_GLOBAL" {
		t.Errorf("nested normalize = %q", got)
	}
}
