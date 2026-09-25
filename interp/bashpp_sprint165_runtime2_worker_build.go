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
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
// linker read. Packages without export data (unsafe) are omitted.
func bashPPWorkerImportcfg(ctx context.Context, goBinary, dir string, env []string, imports []string) ([]byte, error) {
	args := append([]string{"list", "-export", "-deps", "-p", "2", "-f", "{{if .Export}}packagefile {{.ImportPath}}={{.Export}}{{end}}"}, imports...)
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
// intermediate artifact is written there and removed with it.
func bashPPBuildWorkerImportcfg(ctx context.Context, goBinary, dir string, env []string, work, source, binary string) error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	imports, err := bashPPWorkerImportPaths(string(data))
	if err != nil {
		return err
	}
	cfg, err := bashPPWorkerImportcfg(ctx, goBinary, dir, env, imports)
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
	if err := run("tool", "compile", "-p", "main", "-importcfg", importcfg, "-o", archive, "-pack", source); err != nil {
		return err
	}
	return run("tool", "link", "-importcfg", importcfg, "-buildmode=exe", "-o", binary, archive)
}
