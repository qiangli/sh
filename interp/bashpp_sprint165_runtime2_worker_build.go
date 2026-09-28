package interp

// Sprint: #165; Story: #99; Story-ID: ca559d7ee23d
//
// The dependency worker built through the policy-free toolchain path
// (docs/bashpp-multi-package-execution.md §4.3).
//
// `go build` of the worker's virtual path applied cmd/go's DIRECTORY rule to
// the worker's imports: a program whose declared identity the checker admitted
// for `internal/runtime/sys` (D8, syntax.BashPPInternalImportVisible) was
// refused again at execute, because the worker's virtual importer sits in the
// program's module, never inside std. The worker is not a package of any
// module — it is the helper of a program whose imports were already decided —
// so it is compiled the way the compiler was designed to be driven: the
// importcfg lists every dependency's export data (`go list -export -deps`,
// the same cache work as `go build`), then `go tool compile -p main
// -importcfg` and `go tool link -importcfg`. Nothing is re-decided: the
// worker imports only what the check admitted (std + the module's native
// paths), and the reviewed toolchain (bashPPGoBootstrap) is the one that
// compiles and links it.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// bashPPWorkerImportPaths reads the import paths of the generated worker
// source. The template's fixed imports and the program's admitted imports
// both come from the source itself, so the importcfg cannot drift from what
// the compiler will resolve.
func bashPPWorkerImportPaths(source string) ([]string, error) {
	file, err := parser.ParseFile(token.NewFileSet(), "worker.go", source, parser.ImportsOnly)
	if err != nil {
		return nil, fmt.Errorf("gosource: dependency worker imports: %w", err)
	}
	seen := map[string]bool{"runtime": true}
	paths := []string{"runtime"}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			return nil, fmt.Errorf("gosource: dependency worker imports: %w", err)
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

// bashPPWorkerImportcfg lists the export data of the worker's dependency
// closure in the module context, in the importcfg form the compiler and the
// linker read. Packages without export data (unsafe) are omitted. A non-empty
// overlay is threaded to the listing so a mapped companion's dependency is
// exported from the overlaid package (the generated helper beside its assembly)
// rather than from its original interpreted source.
func bashPPWorkerImportcfg(ctx context.Context, goBinary, dir string, env []string, overlay string, imports []string) ([]byte, error) {
	args := []string{"list", "-export", "-deps", "-p", "2"}
	if overlay != "" {
		args = append(args, "-overlay="+overlay)
	}
	args = append(args, "-f", "{{if .Export}}packagefile {{.ImportPath}}={{.Export}}{{end}}")
	args = append(args, imports...)
	list := exec.CommandContext(ctx, goBinary, args...)
	list.Dir, list.Env = dir, env
	var out, diagnostics bytes.Buffer
	list.Stdout, list.Stderr = &out, &diagnostics
	if err := list.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("gosource: build dependency bridge: %w: %s", err, diagnostics.String())
	}
	var cfg bytes.Buffer
	for _, line := range strings.Split(out.String(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			cfg.WriteString(line)
			cfg.WriteByte('\n')
		}
	}
	return cfg.Bytes(), nil
}

// bashPPBuildWorkerImportcfg compiles and links the worker source into binary.
// work is the private scratch directory the source already lives in; every
// intermediate artifact is written there and removed with it. A non-empty
// overlay is threaded to the dependency listing so a mapped companion's
// package is exported from its overlaid form; the compile and link steps read
// the worker source directly and never re-apply cmd/go's directory rule.
func bashPPBuildWorkerImportcfg(ctx context.Context, goBinary, dir string, env []string, overlay, work, source, binary string, linkValues map[string]string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	imports, err := bashPPWorkerImportPaths(string(data))
	if err != nil {
		return err
	}
	cfg, err := bashPPWorkerImportcfg(ctx, goBinary, dir, env, overlay, imports)
	if err != nil {
		return err
	}
	importcfg := filepath.Join(work, "importcfg")
	if err := os.WriteFile(importcfg, cfg, 0600); err != nil {
		return err
	}
	archive := filepath.Join(work, "worker.a")
	run := func(args ...string) error {
		cmd := exec.CommandContext(ctx, goBinary, args...)
		cmd.Dir, cmd.Env = dir, env
		var diagnostics bytes.Buffer
		cmd.Stdout, cmd.Stderr = &diagnostics, &diagnostics
		if err := cmd.Run(); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			return fmt.Errorf("gosource: build dependency bridge: %w: %s", err, diagnostics.String())
		}
		return nil
	}
	cache, err := openBashPPWorkerArchiveCache(ctx, data, cfg, goBinary, env)
	if err != nil {
		return err
	}
	if cache != nil {
		defer cache.close()
		archive = cache.path
	}
	if cache == nil || cache.owned {
		compiled := archive
		if cache != nil {
			compiled = cache.temp
		}
		if err := run("tool", "compile", "-p", "main", "-importcfg", importcfg, "-o", compiled, "-pack", source); err != nil {
			return err
		}
		if cache != nil {
			if err := os.Rename(compiled, archive); err != nil {
				return fmt.Errorf("gosource: publish dependency bridge cache: %w", err)
			}
		}
	}
	linkArgs := []string{"tool", "link", "-importcfg", importcfg, "-buildmode=exe"}
	for _, value := range bashPPWorkerLinkValues(linkValues) {
		linkArgs = append(linkArgs, "-X", value)
	}
	linkArgs = append(linkArgs, "-o", binary, archive)
	return run(linkArgs...)
}

const bashPPWorkerCacheFormat = "bashpp-worker-archive-v1"

type bashPPWorkerArchiveCache struct {
	path, temp, lock string
	owned            bool
	stop, stopped    chan struct{}
}

// openBashPPWorkerArchiveCache shares only the expensive compilation of the
// generated dependency worker. The reexec launcher's private directory and
// authenticated interpreter identity bound the cache to one parent program;
// each child still links a fresh binary with fresh connection credentials.
func openBashPPWorkerArchiveCache(ctx context.Context, source, importcfg []byte, goBinary string, env []string) (*bashPPWorkerArchiveCache, error) {
	dir, interpreterID := os.Getenv(reexecPreparedCacheEnv), os.Getenv(reexecInterpreterIDEnv)
	if dir == "" || interpreterID == "" {
		return nil, nil
	}
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("gosource: dependency bridge cache directory must be absolute")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("gosource: create dependency bridge cache: %w", err)
	}
	h := sha256.New()
	for _, value := range [][]byte{[]byte(bashPPWorkerCacheFormat), []byte(interpreterID), []byte(goBinary), source, importcfg} {
		h.Write(value)
		h.Write([]byte{0})
	}
	for _, name := range []string{"GOROOT", "GOTOOLCHAIN", "GOOS", "GOARCH", "GOEXPERIMENT", "GOAMD64", "GOARM64", "GO386", "GOMIPS", "GOMIPS64", "GOPPC64", "GORISCV64", "CGO_ENABLED"} {
		h.Write([]byte(name))
		h.Write([]byte{'='})
		for _, entry := range env {
			key, value, ok := strings.Cut(entry, "=")
			if ok && strings.EqualFold(key, name) {
				h.Write([]byte(value))
				break
			}
		}
		h.Write([]byte{0})
	}
	key := hex.EncodeToString(h.Sum(nil))
	cache := &bashPPWorkerArchiveCache{
		path: filepath.Join(dir, "worker-"+key+".a"),
		lock: filepath.Join(dir, "worker-"+key+".lock"),
	}
	for {
		if info, err := os.Stat(cache.path); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return cache, nil
		}
		lock, err := os.OpenFile(cache.lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_ = lock.Close()
			tmp, err := os.CreateTemp(dir, ".worker-*.a")
			if err != nil {
				_ = os.Remove(cache.lock)
				return nil, err
			}
			cache.temp = tmp.Name()
			_ = tmp.Close()
			cache.owned = true
			cache.stop, cache.stopped = make(chan struct{}), make(chan struct{})
			go cache.keepLockFresh()
			return cache, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("gosource: lock dependency bridge cache: %w", err)
		}
		if info, statErr := os.Stat(cache.lock); statErr == nil && time.Since(info.ModTime()) > 15*time.Minute {
			_ = os.Remove(cache.lock)
			continue
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func (c *bashPPWorkerArchiveCache) close() {
	if c == nil || !c.owned {
		return
	}
	close(c.stop)
	<-c.stopped
	_ = os.Remove(c.temp)
	_ = os.Remove(c.lock)
	c.owned = false
}

func (c *bashPPWorkerArchiveCache) keepLockFresh() {
	defer close(c.stopped)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case now := <-ticker.C:
			_ = os.Chtimes(c.lock, now, now)
		case <-c.stop:
			return
		}
	}
}

func bashPPWorkerLinkValues(values map[string]string) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, "main."+name+"="+values[name])
	}
	return out
}

func bashPPWorkerBuildLDFlags(values map[string]string) string {
	var flags []string
	for _, value := range bashPPWorkerLinkValues(values) {
		flags = append(flags, "-X", strconv.Quote(value))
	}
	return strings.Join(flags, " ")
}
