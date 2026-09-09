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

// Physical helper inputs and artifacts belong to private temporary storage.
// The overlay gives Go a virtual importer within the original module/internal
// visibility tree without creating even a directory there. Canonical paths are
// essential: Go resolves its cwd through symlinks before matching overlay keys.
func bashPPImportTempSource(dir, pattern string, env []string) (*bashPPImportSource, error) {
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
	if pathWithin(contextDir, root) {
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
	virtualDir := filepath.Join(contextDir, filepath.Base(work))
	if _, err := os.Lstat(virtualDir); !os.IsNotExist(err) {
		f.Close()
		cleanup()
		return nil, fmt.Errorf("bash++: helper overlay would mask an existing source path")
	}
	virtual := filepath.Join(virtualDir, filepath.Base(f.Name()))
	overlay := filepath.Join(work, "overlay.json")
	data, err := json.Marshal(struct{ Replace map[string]string }{map[string]string{virtual: f.Name()}})
	if err == nil {
		err = os.WriteFile(overlay, data, 0600)
	}
	if err != nil {
		f.Close()
		cleanup()
		return nil, err
	}
	return &bashPPImportSource{File: f, buildPath: virtual, overlay: overlay, cleanup: cleanup}, nil
}
