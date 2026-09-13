package interp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-quicktest/qt"
	"mvdan.cc/sh/v3/gosource"
)

func TestSprint162ComparableMapKeys(t *testing.T) {
	root := filepath.Join("testdata", "sprint162", "interp-collections-3", "comparable-map-keys")
	for _, name := range []string{"comparable_map_keys", "dynamic_unhashable"} {
		source, err := os.ReadFile(filepath.Join(root, name+".go"))
		qt.Assert(t, qt.IsNil(err))
		want, err := os.ReadFile(filepath.Join(root, name+".expected"))
		qt.Assert(t, qt.IsNil(err))
		out, stderr, err := runGoSource(t, name, string(source))
		qt.Assert(t, qt.IsNil(err), qt.Commentf("%s stderr: %s", name, stderr))
		qt.Assert(t, qt.Equals(out, string(want)), qt.Commentf("case %s", name))
	}
}

func TestSprint162ComparableMapKeysRejectStaticUnhashable(t *testing.T) {
	path := filepath.Join("testdata", "sprint162", "interp-collections-3", "comparable-map-keys", "static_unhashable.go")
	source, err := os.ReadFile(path)
	qt.Assert(t, qt.IsNil(err))
	_, err = gosource.Parse(strings.NewReader(string(source)), path, gosource.Options{RunMain: true})
	qt.Assert(t, qt.ErrorMatches(err, `(?s).*invalid map key type \[\]int.*`))
}
