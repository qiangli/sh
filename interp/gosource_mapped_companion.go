// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"bytes"
	"context"
	"fmt"
	"go/format"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func bashPPMappedCompanionSource(pkg bashPPMappedCompanion) (string, error) {
	var source strings.Builder
	fmt.Fprintf(&source, "package %s\n\n", pkg.Name)
	for _, fn := range pkg.Funcs {
		if !syntax.BashPPValidIdent(fn.Name) {
			return "", fmt.Errorf("gosource: invalid mapped companion function %q", fn.Name)
		}
		if fn.Linkname != "" {
			return "", fmt.Errorf("gosource: mapped companion function %s has unsupported linkname", fn.Name)
		}
		if fn.Results == "" {
			fmt.Fprintf(&source, "func %s(%s)\n", fn.Name, fn.Params)
		} else {
			fmt.Fprintf(&source, "func %s(%s)(%s)\n", fn.Name, fn.Params, fn.Results)
		}
	}
	fmt.Fprintf(&source, "func %s() map[string]any { return map[string]any{\n", pkg.Hook)
	for _, fn := range pkg.Funcs {
		fmt.Fprintf(&source, "%q: %s,\n", fn.Name, fn.Name)
	}
	source.WriteString("} }\n")
	formatted, err := format.Source([]byte(source.String()))
	if err != nil {
		return "", fmt.Errorf("gosource: mapped companion package %q helper: %w", pkg.Path, err)
	}
	return string(formatted), nil
}

func bashPPOverlayMappedCompanions(ctx context.Context, req bashPPEvalRequest, file *bashPPImportSource, env []string) error {
	for i, pkg := range req.MappedCompanions {
		stubs, err := bashPPCompanionRootStubs(pkg.SourceDir, pkg.SourceFiles)
		if err != nil {
			return fmt.Errorf("gosource: mapped package %q: %w", pkg.Path, err)
		}
		if err := file.overlayPackage(pkg.SourceDir, stubs); err != nil {
			return err
		}
		source, err := bashPPMappedCompanionSource(pkg)
		if err != nil {
			return err
		}
		var roots []string
		for path := range stubs {
			if bashPPCompanionGoFileName(filepath.Base(path)) && bashPPCompanionFileBuilt(path) {
				roots = append(roots, path)
			}
		}
		sort.Strings(roots)
		if len(roots) == 0 {
			return fmt.Errorf("gosource: mapped companion package %q has no selected Go file to overlay", pkg.Path)
		}
		name := filepath.Base(roots[0])
		if err := file.replacePackageSource(roots[0], fmt.Sprintf("bashpp-companion-%d.go", i), source); err != nil {
			return err
		}
		if err := bashPPMappedCompanionBuildIsolated(ctx, req, file, env, pkg, name); err != nil {
			return err
		}
	}
	return nil
}

func bashPPMappedCompanionBuildIsolated(ctx context.Context, req bashPPEvalRequest, file *bashPPImportSource, env []string, pkg bashPPMappedCompanion, generated string) error {
	list := exec.CommandContext(ctx, req.Go, "list", "-overlay="+file.overlay, "-f", "{{range .GoFiles}}go:{{.}}\n{{end}}{{range .SFiles}}s:{{.}}\n{{end}}", pkg.Path)
	list.Dir, list.Env = file.sourceDir, setEnvString(env, "PWD", file.sourceDir)
	var out, diagnostics bytes.Buffer
	list.Stdout, list.Stderr = &out, &diagnostics
	if err := list.Run(); err != nil {
		return fmt.Errorf("gosource: list mapped companion package %q: %w: %s", pkg.Path, err, diagnostics.String())
	}
	selected := make(map[string]bool, len(pkg.Files))
	canonicalDir, err := filepath.Abs(pkg.SourceDir)
	if err != nil {
		return err
	}
	canonicalDir, err = filepath.EvalSymlinks(canonicalDir)
	if err != nil {
		return err
	}
	for _, path := range pkg.Files {
		if !filepath.IsAbs(path) {
			path = filepath.Join(canonicalDir, path)
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			return fmt.Errorf("gosource: mapped companion package %q file %s: %w", pkg.Path, path, err)
		}
		if parent != canonicalDir || !strings.EqualFold(filepath.Ext(path), ".s") {
			return fmt.Errorf("gosource: mapped companion package %q file %s is not assembly in its source directory", pkg.Path, path)
		}
		name := filepath.Base(path)
		if selected[name] {
			return fmt.Errorf("gosource: mapped companion package %q selects assembly file %s more than once", pkg.Path, name)
		}
		selected[name] = true
	}
	var got []string
	generatedSeen := false
	for _, line := range strings.Fields(out.String()) {
		kind, name, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch kind {
		case "go":
			if name == generated {
				generatedSeen = true
				continue
			}
			if _, ok := file.replace[filepath.Join(canonicalDir, name)]; !ok {
				return fmt.Errorf("gosource: mapped companion package %q would compile original file %s", pkg.Path, name)
			}
		case "s":
			got = append(got, name)
			if !selected[name] {
				return fmt.Errorf("gosource: mapped companion package %q selected undeclared assembly file %s", pkg.Path, name)
			}
		}
	}
	if !generatedSeen {
		return fmt.Errorf("gosource: mapped companion package %q does not include its generated helper (selected %q)", pkg.Path, strings.TrimSpace(out.String()))
	}
	sort.Strings(got)
	var want []string
	for name := range selected {
		if bashPPCompanionFileBuilt(filepath.Join(pkg.SourceDir, name)) {
			want = append(want, name)
		}
	}
	sort.Strings(want)
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		return fmt.Errorf("gosource: mapped companion package %q built assembly files %v, want exact selection %v", pkg.Path, got, want)
	}
	return nil
}
