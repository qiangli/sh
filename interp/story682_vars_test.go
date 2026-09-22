// Copyright (c) 2026, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"testing"

	"mvdan.cc/sh/v3/expand"
)

// Sprint 245, story 682 (ifs-posix): in windows mode the names bash's
// ifs-posix.tests relies on being distinct — `s`/`S`, `r`/`R`, `x`/`y`,
// `ifs`/`IFS` — must stay distinct when the process environment does not
// carry their upper-case forms; only an inherited name (PATH) folds. The
// old fold-everything rule collapsed s/S and r/R and failed half the
// fixture.
func TestStory682IFSPosixNamesWindowsMode(t *testing.T) {
	t.Parallel()
	base := expand.ListEnviron("PATH=C:\\Windows", "TEMP=C:\\Temp")
	o := &overlayEnviron{parent: base, values: map[string]namedVariable{}}
	for _, name := range []string{"s", "S", "r", "R", "x", "y", "i", "g", "ifs", "IFS", "ksh_read", "f1"} {
		if got := o.normalizeMode(name, true); got != name {
			t.Errorf("normalizeMode(%q, windows) = %q; the fixture needs its spelling kept", name, got)
		}
	}
	if got := o.normalizeMode("path", true); got != "PATH" {
		t.Errorf("normalizeMode(path, windows) = %q; an inherited name still folds", got)
	}
}
