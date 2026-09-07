package lower

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

type listError struct {
	ImportStack []string `json:"ImportStack"`
	Pos         string   `json:"Pos"`
	Err         string   `json:"Err"`
}

type listPackage struct {
	Dir        string            `json:"Dir"`
	ImportPath string            `json:"ImportPath"`
	Export     string            `json:"Export"`
	Error      *listError        `json:"Error"`
	DepsErrors []*listError      `json:"DepsErrors"`
	ImportMap  map[string]string `json:"ImportMap"`
}

type moduleMetadata struct {
	Path string `json:"Path"`
	Dir  string `json:"Dir"`
}

type moduleImporter struct {
	dir        string
	packages   map[string]*listPackage
	importMap  map[string]string
	callerPath string
	mu         sync.Mutex
	delegate   types.Importer
}

// newModuleImporter creates a types.Importer that invokes `go list -export -deps -json`
// using runtime.GOROOT()/bin/go in the specified directory with GOTOOLCHAIN=local.
// The returned importer also implements types.ImporterFrom and enforces internal package visibility.
func newModuleImporter(dir string) types.Importer {
	if dir == "" {
		dir, _ = os.Getwd()
	}
	dir, _ = filepath.EvalSymlinks(dir)
	m := &moduleImporter{
		dir:        dir,
		packages:   make(map[string]*listPackage),
		importMap:  make(map[string]string),
		callerPath: determineCallerPath(dir),
	}
	m.delegate = importer.ForCompiler(token.NewFileSet(), "gc", m.lookup)
	return m
}

func goBinPath() (string, error) {
	return filepath.Join(runtime.GOROOT(), "bin", "go"), nil
}

func determineCallerPath(dir string) string {
	cleanDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		cleanDir = filepath.Clean(dir)
	}

	goBin, err := goBinPath()
	if err == nil {
		cmd := exec.Command(goBin, "list", "-m", "-json")
		cmd.Dir = cleanDir
		cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
		var stdout bytes.Buffer
		cmd.Stdout = &stdout

		if cmd.Run() == nil {
			decoder := json.NewDecoder(&stdout)
			var bestMod moduleMetadata
			bestModLen := -1

			for decoder.More() {
				var mod moduleMetadata
				if err := decoder.Decode(&mod); err == nil && mod.Path != "" && mod.Dir != "" {
					cleanModDir, err := filepath.EvalSymlinks(mod.Dir)
					if err != nil {
						cleanModDir = filepath.Clean(mod.Dir)
					}
					if cleanDir == cleanModDir || strings.HasPrefix(cleanDir, cleanModDir+string(filepath.Separator)) {
						if len(cleanModDir) > bestModLen {
							bestMod = mod
							bestModLen = len(cleanModDir)
						}
					}
				}
			}

			if bestModLen >= 0 {
				cleanModDir, err := filepath.EvalSymlinks(bestMod.Dir)
				if err != nil {
					cleanModDir = filepath.Clean(bestMod.Dir)
				}
				rel, err := filepath.Rel(cleanModDir, cleanDir)
				if err == nil && !strings.HasPrefix(rel, "..") {
					if rel == "." {
						return bestMod.Path
					}
					return filepath.ToSlash(filepath.Join(bestMod.Path, rel))
				}
			}
		}
	}

	gopathEnv := os.Getenv("GOPATH")
	if gopathEnv == "" {
		if home, err := os.UserHomeDir(); err == nil {
			gopathEnv = filepath.Join(home, "go")
		}
	}

	for _, gopath := range filepath.SplitList(gopathEnv) {
		if gopath == "" {
			continue
		}
		srcDir := filepath.Join(gopath, "src")
		cleanSrcDir, err := filepath.EvalSymlinks(srcDir)
		if err != nil {
			cleanSrcDir = filepath.Clean(srcDir)
		}

		rel, err := filepath.Rel(cleanSrcDir, cleanDir)
		if err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
			return filepath.ToSlash(rel)
		}
	}

	return filepath.Base(cleanDir)
}

func isDirInGOROOT(dir string) bool {
	goroot := runtime.GOROOT()
	if goroot == "" {
		return false
	}
	cleanDir, err1 := filepath.EvalSymlinks(dir)
	if err1 != nil {
		cleanDir = filepath.Clean(dir)
	}
	cleanGoroot, err2 := filepath.EvalSymlinks(goroot)
	if err2 != nil {
		cleanGoroot = filepath.Clean(goroot)
	}

	rel, err := filepath.Rel(cleanGoroot, cleanDir)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..") && rel != ".."
}

func checkInternalVisibility(targetPath, callerPath, callerDir string) error {
	parts := strings.Split(targetPath, "/")
	internalIdx := -1
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == "internal" {
			internalIdx = i
			break
		}
	}
	if internalIdx == -1 {
		return nil
	}

	parentPrefix := strings.Join(parts[:internalIdx], "/")

	if parentPrefix == "" {
		if !isDirInGOROOT(callerDir) {
			return fmt.Errorf("use of internal package %s not allowed", targetPath)
		}
		return nil
	}

	if callerPath == "" {
		return fmt.Errorf("use of internal package %s not allowed", targetPath)
	}

	if callerPath == parentPrefix || strings.HasPrefix(callerPath, parentPrefix+"/") {
		return nil
	}

	return fmt.Errorf("use of internal package %s not allowed", targetPath)
}

func (m *moduleImporter) Import(path string) (*types.Package, error) {
	return m.ImportFrom(path, "", 0)
}

func (m *moduleImporter) ImportFrom(path, srcDir string, mode types.ImportMode) (*types.Package, error) {
	m.mu.Lock()
	caller := m.callerPath
	callerDir := m.dir
	m.mu.Unlock()

	if err := checkInternalVisibility(path, caller, callerDir); err != nil {
		return nil, err
	}

	if from, ok := m.delegate.(types.ImporterFrom); ok {
		return from.ImportFrom(path, srcDir, mode)
	}
	return m.delegate.Import(path)
}

func (m *moduleImporter) lookup(path string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	actualPath := path
	if mapped, ok := m.importMap[path]; ok {
		actualPath = mapped
	}

	if _, ok := m.packages[actualPath]; !ok {
		if err := m.loadLocked(actualPath); err != nil {
			if errDot := m.loadLocked("."); errDot == nil {
				if mapped, ok := m.importMap[path]; ok {
					actualPath = mapped
				}
			} else {
				return nil, err
			}
		}
	}

	pkg, ok := m.packages[actualPath]
	if !ok {
		return nil, fmt.Errorf("package %q not found in go list output", path)
	}

	if pkg.Error != nil {
		return nil, fmt.Errorf("%s", pkg.Error.Err)
	}
	if len(pkg.DepsErrors) > 0 {
		return nil, fmt.Errorf("%s", pkg.DepsErrors[0].Err)
	}
	if pkg.Export == "" {
		return nil, fmt.Errorf("package %q has no export data", path)
	}

	return os.Open(pkg.Export)
}

func (m *moduleImporter) loadLocked(target string) error {
	goBin, err := goBinPath()
	if err != nil {
		return err
	}

	cmd := exec.Command(goBin, "list", "-export", "-deps", "-json", target)
	cmd.Dir = m.dir
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	decoder := json.NewDecoder(&stdout)
	for decoder.More() {
		var pkg listPackage
		if err := decoder.Decode(&pkg); err != nil {
			if runErr != nil {
				return fmt.Errorf("go list failed: %w\n%s", runErr, stderr.String())
			}
			return fmt.Errorf("failed to decode go list output: %w", err)
		}
		if pkg.ImportPath != "" {
			m.packages[pkg.ImportPath] = &pkg
			for k, v := range pkg.ImportMap {
				m.importMap[k] = v
			}
		}
	}

	pkg, ok := m.packages[target]
	if !ok {
		if mapped, has := m.importMap[target]; has {
			pkg, ok = m.packages[mapped]
		}
	}

	if !ok && runErr != nil {
		return fmt.Errorf("go list failed: %w\n%s", runErr, stderr.String())
	}
	if ok && pkg.Export == "" && pkg.Error == nil && len(pkg.DepsErrors) == 0 && runErr != nil {
		return fmt.Errorf("go build failed: %w\n%s", runErr, stderr.String())
	}

	return nil
}
