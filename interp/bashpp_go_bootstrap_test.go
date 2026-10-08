package interp

import (
	"path/filepath"
	"strings"
	"testing"
)

// A binary built elsewhere carries the builder's GOROOT; when that directory
// does not exist on this host the bootstrap must not hand back a go binary
// inside it (Sprint 379: a Windows bashy looked for
// C:\\Users\\<builder>\\sdk\\go1.27.1\\bin\\go.exe and failed every whole-file Go run).
func TestBashPPGoBootstrapIgnoresMissingBuildGOROOT(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-goroot")
	prev := bashPPRuntimeGOROOT
	bashPPRuntimeGOROOT = func() string { return missing }
	t.Cleanup(func() { bashPPRuntimeGOROOT = prev })

	root, binary, err := bashPPGoBootstrap("go")
	if err == nil && (strings.HasPrefix(root, missing) || strings.HasPrefix(binary, missing)) {
		t.Fatalf("bootstrap returned the missing build GOROOT: root=%q binary=%q", root, binary)
	}
}
