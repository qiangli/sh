package interp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type bashPPImportSource struct {
	*os.File
	buildPath string
	overlay   string
	cleanup   func()
}

// bashPPScratchPolicy decides whether private helper scratch may be placed
// inside the caller's own source directory. The two bash++ evaluation paths
// answer differently, and the split is exactly the GoSource/Classic boundary:
// GoSource work reaches [bashPPImportTempSource] only through the native
// session bridge, while [nativeBashPPEvaluator.Call] and
// [nativeBashPPEvaluator.Values] return early whenever a bridge is attached
// and therefore build helpers only for Classic bash++ imports.
type bashPPScratchPolicy int

const (
	// bashPPScratchIsolated keeps every helper input and artifact out of the
	// source directory. A GoSource session treats the original module as
	// read-only input, so a TMPDIR pointing back into it is a request the
	// session cannot honour and must reject rather than silently relocate.
	bashPPScratchIsolated bashPPScratchPolicy = iota
	// bashPPScratchSourceRoot gives an overlay-backed helper a logical path in
	// the source directory itself. Compiler directives such as go:embed resolve
	// their patterns relative to that logical path without writing there.
	bashPPScratchSourceRoot
	// bashPPScratchSourceTree additionally tolerates a scratch root inside
	// the source directory. Classic bash++ imports have always evaluated
	// helpers from a dot directory under the importer, and profiles point
	// TMPDIR at the exec source root on purpose.
	bashPPScratchSourceTree
)

// Physical helper inputs and artifacts belong to private temporary storage.
// The overlay gives Go a virtual importer within the original module/internal
// visibility tree without creating even a directory there. Canonical paths are
// essential: Go resolves its cwd through symlinks before matching overlay keys.
//
// A scratch root inside the source directory needs no such indirection — the
// helper already sits in the importer's module — and could not use one anyway,
// since its virtual directory would collide with the real scratch directory it
// is meant to stand in for. Those requests build the real file in place and
// carry an empty overlay so that callers keep a single build invocation.
func bashPPImportTempSource(dir, pattern string, env []string, policy bashPPScratchPolicy) (*bashPPImportSource, error) {
	if dir == "" {
		dir = "."
	}
	contextDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	contextDir, err = filepath.EvalSymlinks(contextDir)
	if err != nil {
		return nil, err
	}
	root := os.TempDir()
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "TMPDIR="); ok && value != "" {
			root = value
		}
	}
	if !filepath.IsAbs(root) {
		root = filepath.Join(contextDir, root)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	inSource := pathWithin(contextDir, root)
	if inSource && policy != bashPPScratchSourceTree {
		return nil, fmt.Errorf("bash++: private helper TMPDIR is inside the source directory")
	}
	work, err := os.MkdirTemp(root, ".bashpp-eval-")
	if err != nil {
		return nil, err
	}
	cleanup := func() { _ = os.RemoveAll(work) }
	f, err := os.CreateTemp(work, pattern)
	if err != nil {
		cleanup()
		return nil, err
	}
	replace := map[string]string{}
	buildPath := f.Name()
	if !inSource {
		virtualDir := filepath.Join(contextDir, filepath.Base(work))
		collisionPath := virtualDir
		if policy == bashPPScratchSourceRoot {
			virtualDir = contextDir
			collisionPath = filepath.Join(virtualDir, filepath.Base(f.Name()))
		}
		buildPath = filepath.Join(virtualDir, filepath.Base(f.Name()))
		if _, err := os.Lstat(collisionPath); !os.IsNotExist(err) {
			f.Close()
			cleanup()
			return nil, fmt.Errorf("bash++: helper overlay would mask an existing source path")
		}
		replace[buildPath] = f.Name()
	}
	overlay := filepath.Join(work, "overlay.json")
	data, err := json.Marshal(struct{ Replace map[string]string }{replace})
	if err == nil {
		err = os.WriteFile(overlay, data, 0600)
	}
	if err != nil {
		f.Close()
		cleanup()
		return nil, err
	}
	return &bashPPImportSource{File: f, buildPath: buildPath, overlay: overlay, cleanup: cleanup}, nil
}
