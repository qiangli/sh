package interp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

type bashPPEvalRequest struct {
	Go       string
	Dir      string
	Env      []string
	Stdin    io.Reader
	Stdout   io.Writer
	Stderr   io.Writer
	Imports  map[string]string
	Selector []string
	Args     []string
	Results  int
}

type bashPPEvaluator interface {
	Resolve(context.Context, bashPPEvalRequest, string) (string, error)
	Call(context.Context, bashPPEvalRequest) error
}

type bashPPValuesEvaluator interface {
	Values(context.Context, bashPPEvalRequest) ([]any, error)
}

// bashPPToolchain is deliberately package-private. The zero value selects the
// Go toolchain which built this package, never an unrelated executable found
// first on PATH. Tests may inject the exact evaluator and identity under
// review without exposing an evaluator API to embedders.
type bashPPToolchain struct {
	goBinary  string
	goRoot    string
	goVersion string
	eval      bashPPEvaluator
}

type bashPPGoReview struct {
	Version string
	GOOS    string
	GOARCH  string
	SHA256  string
}

// bashPPGoReviews is the testable allow-list for platforms on which P2A may
// execute Go. Each digest reviews the bin/go payload in the official Go
// toolchain module, not whichever executable happens to be on PATH.
var bashPPGoReviews = []bashPPGoReview{
	{Version: "go1.27.0", GOOS: "darwin", GOARCH: "amd64", SHA256: "71189642c2912561f458bee762a3011335997594796fa963b6c284be0019a009"},
	{Version: "go1.27.0", GOOS: "darwin", GOARCH: "arm64", SHA256: "a19a71df81715c12d9a7e81bab036c12696fec1ddbd4258b48a2131a9080b267"},
	{Version: "go1.27.0", GOOS: "linux", GOARCH: "amd64", SHA256: "1db869c560a193573a71be466a34e0d4abb7792d78165c6102cdda069276a3a8"},
	{Version: "go1.27.0", GOOS: "linux", GOARCH: "arm64", SHA256: "b51e8499a917e56a0b290e2ab3ba96f11715dc47ad9739d307e03708e630343a"},
}

type bashPPGoIdentityInfo struct {
	Version string
	GOOS    string
	GOARCH  string
	Root    string
	Binary  string
	SHA256  string
}

type nativeBashPPEvaluator struct{}

func (nativeBashPPEvaluator) Resolve(ctx context.Context, req bashPPEvalRequest, path string) (string, error) {
	cmd := exec.CommandContext(ctx, req.Go, "list", "-json", path)
	cmd.Dir, cmd.Env = req.Dir, req.Env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, req.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("bash++ import %q: go list: %w", path, err)
	}
	var info struct {
		Name     string
		Standard bool
		Dir      string
	}
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		return "", fmt.Errorf("go list %q: %w", path, err)
	}
	if info.Standard && !syntax.BashPPStdlibImportAllowed(path) {
		return "", fmt.Errorf("bash++ import %q: package is not in the reviewed Go standard library", path)
	}
	if !syntax.ValidName(info.Name) {
		return "", fmt.Errorf("bash++ import %q: invalid package name %q", path, info.Name)
	}
	if err := validateBashPPImportVisibility(req.Dir, info.Dir, path); err != nil {
		return "", err
	}
	return info.Name, nil
}

func validateBashPPImportVisibility(importerDir, packageDir, importPath string) error {
	for _, elem := range strings.Split(importPath, "/") {
		if elem == "." || elem == ".." {
			return fmt.Errorf("bash++ import %q: path traversal is not allowed", importPath)
		}
		if elem == "vendor" {
			return fmt.Errorf("bash++ import %q: vendor packages must be imported by their canonical path", importPath)
		}
	}
	cleanImporter, err := filepath.EvalSymlinks(importerDir)
	if err != nil {
		return fmt.Errorf("bash++ import %q: resolve importer directory: %w", importPath, err)
	}
	cleanPackage, err := filepath.EvalSymlinks(packageDir)
	if err != nil {
		return fmt.Errorf("bash++ import %q: resolve package directory: %w", importPath, err)
	}
	for current := filepath.Clean(cleanPackage); ; current = filepath.Dir(current) {
		elem := filepath.Base(current)
		if elem != "internal" && elem != "vendor" {
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
			continue
		}
		root := filepath.Dir(current)
		if !pathWithin(root, cleanImporter) {
			return fmt.Errorf("bash++ import %q: use of %s package outside allowed tree %q", importPath, elem, root)
		}
		if root == current {
			break
		}
	}
	return nil
}

func pathWithin(root, name string) bool {
	rel, err := filepath.Rel(root, name)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (nativeBashPPEvaluator) Call(ctx context.Context, req bashPPEvalRequest) error {
	if len(req.Selector) == 0 {
		return errors.New("bash++: selector call requires an imported package")
	}
	var dotPaths []string
	for name, importedPath := range req.Imports {
		if strings.HasPrefix(name, ".:") {
			dotPaths = append(dotPaths, importedPath)
		}
	}
	sort.Strings(dotPaths)
	hasDot := len(dotPaths) > 0
	_, named := req.Imports[req.Selector[0]]
	if len(req.Selector) < 2 && !hasDot {
		return errors.New("bash++: selector call requires an imported package")
	}
	if len(req.Selector) >= 2 && !named {
		return fmt.Errorf("bash++: package %s is not imported", req.Selector[0])
	}
	if !token.IsIdentifier(req.Selector[0]) || token.Lookup(req.Selector[0]).IsKeyword() {
		return fmt.Errorf("bash++: invalid import name %q", req.Selector[0])
	}
	selector := ast.Expr(ast.NewIdent(req.Selector[0]))
	for _, part := range req.Selector[1:] {
		if !token.IsIdentifier(part) || token.Lookup(part).IsKeyword() {
			return fmt.Errorf("bash++: invalid selector %q", part)
		}
		selector = &ast.SelectorExpr{X: selector, Sel: ast.NewIdent(part)}
	}
	args := make([]ast.Expr, len(req.Args))
	usedImports := map[string]bool{req.Selector[0]: named}
	for i, text := range req.Args {
		expr, err := parser.ParseExpr(text)
		if err != nil {
			return fmt.Errorf("bash++: invalid Go argument %d: %w", i+1, err)
		}
		args[i] = expr
		ast.Inspect(expr, func(node ast.Node) bool {
			sel, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if ident, ok := sel.X.(*ast.Ident); ok {
				_, imported := req.Imports[ident.Name]
				usedImports[ident.Name] = imported
			}
			return true
		})
	}
	var importSpecs []ast.Spec
	if !named {
		for _, dotPath := range dotPaths {
			importSpecs = append(importSpecs, &ast.ImportSpec{Name: ast.NewIdent("."), Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(dotPath)}})
		}
	}
	names := make([]string, 0, len(req.Imports))
	for name := range req.Imports {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		importedPath := req.Imports[name]
		if strings.HasPrefix(name, "_:") {
			importSpecs = append(importSpecs, &ast.ImportSpec{Name: ast.NewIdent("_"), Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(importedPath)}})
		} else if usedImports[name] {
			importSpecs = append(importSpecs, &ast.ImportSpec{Name: ast.NewIdent(name), Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(importedPath)}})
		}
	}
	file := &ast.File{Name: ast.NewIdent("main"), Decls: []ast.Decl{
		&ast.GenDecl{Tok: token.IMPORT, Specs: importSpecs},
		&ast.FuncDecl{Name: ast.NewIdent("main"), Type: &ast.FuncType{Params: &ast.FieldList{}},
			Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: selector, Args: args}}}}},
	}}
	var src bytes.Buffer
	if err := format.Node(&src, token.NewFileSet(), file); err != nil {
		return fmt.Errorf("bash++: construct selector call: %w", err)
	}
	f, err := os.CreateTemp(req.Dir, "bashpp-*.go")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := io.Copy(f, &src); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	bin := name + ".bin"
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	defer os.Remove(bin)
	build := exec.CommandContext(ctx, req.Go, "build", "-o", bin, name)
	build.Dir, build.Env = req.Dir, req.Env
	build.Stdout, build.Stderr = req.Stdout, req.Stderr
	if err := build.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir, cmd.Env = req.Dir, req.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = req.Stdin, req.Stdout, req.Stderr
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	return nil
}

func (nativeBashPPEvaluator) Values(ctx context.Context, req bashPPEvalRequest) ([]any, error) {
	if len(req.Selector) < 2 || req.Results < 1 {
		return nil, errors.New("bash++: value call requires an imported selector and result names")
	}
	path, ok := req.Imports[req.Selector[0]]
	if !ok {
		return nil, fmt.Errorf("bash++: package %s is not imported", req.Selector[0])
	}
	for _, part := range req.Selector {
		if !token.IsIdentifier(part) || token.Lookup(part).IsKeyword() {
			return nil, fmt.Errorf("bash++: invalid selector %q", part)
		}
	}
	var src strings.Builder
	src.WriteString("package main\nimport (\nbashppjson \"encoding/json\"\nbashppos \"os\"\n")
	src.WriteString(req.Selector[0])
	src.WriteByte(' ')
	src.WriteString(strconv.Quote(path))
	src.WriteString("\n)\nfunc main() {\n")
	for i := 0; i < req.Results; i++ {
		if i > 0 {
			src.WriteByte(',')
		}
		fmt.Fprintf(&src, "bashppv%d", i)
	}
	src.WriteString(" := ")
	src.WriteString(strings.Join(req.Selector, "."))
	src.WriteByte('(')
	src.WriteString(strings.Join(req.Args, ","))
	src.WriteString(")\n_ = bashppjson.NewEncoder(bashppos.Stdout).Encode([]any{")
	for i := 0; i < req.Results; i++ {
		if i > 0 {
			src.WriteByte(',')
		}
		fmt.Fprintf(&src, "bashppv%d", i)
	}
	src.WriteString("})\n}\n")
	formatted, err := format.Source([]byte(src.String()))
	if err != nil {
		return nil, fmt.Errorf("bash++: construct value call: %w", err)
	}
	f, err := os.CreateTemp(req.Dir, "bashpp-values-*.go")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(formatted); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	bin := name + ".bin"
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	defer os.Remove(bin)
	build := exec.CommandContext(ctx, req.Go, "build", "-o", bin, name)
	build.Dir, build.Env, build.Stdout, build.Stderr = req.Dir, req.Env, req.Stdout, req.Stderr
	if err := build.Run(); err != nil {
		return nil, err
	}
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir, cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = req.Dir, req.Env, req.Stdin, &stdout, req.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	var values []any
	if err := json.Unmarshal(stdout.Bytes(), &values); err != nil {
		return nil, fmt.Errorf("bash++: decode value call: %w", err)
	}
	return values, nil
}

func (r *Runner) bashPPEvalRequest() (bashPPEvalRequest, error) {
	env := environStrings(r.writeEnv)
	if r.bashPPTools.goBinary == "" {
		identity, err := bashPPGoIdentity()
		if err != nil {
			return bashPPEvalRequest{}, fmt.Errorf("bash++: selected Go toolchain: %w", err)
		}
		r.bashPPTools.goBinary = identity.Binary
		r.bashPPTools.goRoot = identity.Root
		r.bashPPTools.goVersion = identity.Version
	}
	if r.bashPPTools.goRoot != "" {
		env = setEnvString(env, "GOROOT", r.bashPPTools.goRoot)
		env = setEnvString(env, "GOTOOLCHAIN", r.bashPPTools.goVersion)
	}
	return bashPPEvalRequest{Go: r.bashPPTools.goBinary, Dir: r.Dir, Env: env, Stdin: r.stdin,
		Stdout: r.bashPPWriter(r.stdout), Stderr: r.bashPPWriter(r.stderr), Imports: r.bashPPImports}, nil
}

func setEnvString(env []string, name, value string) []string {
	prefix := name + "="
	out := make([]string, 0, len(env)+1)
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return append(out, prefix+value)
}

func environStrings(env expand.Environ) []string {
	var out []string
	for name, vr := range env.Each {
		if vr.IsSet() {
			out = append(out, name+"="+vr.String())
		}
	}
	return out
}

func bashPPGoIdentity() (bashPPGoIdentityInfo, error) {
	name := "go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bootstrapRoot, bootstrap, err := bashPPGoBootstrap(name)
	if err != nil {
		return bashPPGoIdentityInfo{}, fmt.Errorf("resolve Go bootstrap: %w", err)
	}
	cmd := exec.Command(bootstrap, "env", "GOROOT", "GOOS", "GOARCH")
	cmd.Env = setEnvString(os.Environ(), "GOTOOLCHAIN", "go1.27.0")
	cmd.Env = setEnvString(cmd.Env, "GOROOT", bootstrapRoot)
	out, err := cmd.Output()
	if err != nil {
		return bashPPGoIdentityInfo{}, fmt.Errorf("resolve GOTOOLCHAIN=go1.27.0: %w", err)
	}
	fields := strings.Fields(string(out))
	if len(fields) != 3 {
		return bashPPGoIdentityInfo{}, fmt.Errorf("resolve GOTOOLCHAIN=go1.27.0: unexpected go env output %q", out)
	}
	root, goos, goarch := fields[0], fields[1], fields[2]
	path := filepath.Join(root, "bin", name)
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return bashPPGoIdentityInfo{}, err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return bashPPGoIdentityInfo{}, err
	}
	if fi.IsDir() || fi.Mode()&0111 == 0 {
		return bashPPGoIdentityInfo{}, fmt.Errorf("%s is not executable", real)
	}
	real, err = filepath.Abs(real)
	if err != nil {
		return bashPPGoIdentityInfo{}, err
	}
	versionOut, err := exec.Command(real, "version").Output()
	if err != nil {
		return bashPPGoIdentityInfo{}, fmt.Errorf("verify version: %w", err)
	}
	versionFields := strings.Fields(string(versionOut))
	if len(versionFields) != 4 || versionFields[0] != "go" || versionFields[1] != "version" {
		return bashPPGoIdentityInfo{}, fmt.Errorf("unexpected go version output %q", versionOut)
	}
	if versionFields[3] != goos+"/"+goarch {
		return bashPPGoIdentityInfo{}, fmt.Errorf("go version platform %q disagrees with go env %s/%s", versionFields[3], goos, goarch)
	}
	digest, err := bashPPGoDigest(real)
	if err != nil {
		return bashPPGoIdentityInfo{}, err
	}
	identity := bashPPGoIdentityInfo{Version: versionFields[2], GOOS: goos, GOARCH: goarch,
		Root: root, Binary: real, SHA256: digest}
	if err := validateBashPPGoIdentity(identity, bashPPGoReviews); err != nil {
		return bashPPGoIdentityInfo{}, err
	}
	return identity, nil
}

// bashPPGoBootstrap returns an absolute Go binary without consulting PATH.
// A -trimpath binary has no linker-recorded GOROOT, so in that case use the
// downloaded Go 1.27 toolchain module and authenticate its reviewed payload
// before executing it.
func bashPPGoBootstrap(name string) (root, binary string, err error) {
	if root = runtime.GOROOT(); root != "" {
		root, err = filepath.Abs(root)
		if err != nil {
			return "", "", err
		}
		binary, err = filepath.Abs(filepath.Join(root, "bin", name))
		return root, binary, err
	}

	modCache := os.Getenv("GOMODCACHE")
	if modCache == "" {
		if goPath := os.Getenv("GOPATH"); goPath != "" {
			for _, entry := range filepath.SplitList(goPath) {
				if entry != "" {
					modCache = filepath.Join(entry, "pkg", "mod")
					break
				}
			}
		}
	}
	if modCache == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return "", "", homeErr
		}
		modCache = filepath.Join(home, "go", "pkg", "mod")
	}
	if !filepath.IsAbs(modCache) {
		return "", "", fmt.Errorf("module cache path %q is not absolute", modCache)
	}
	root = filepath.Join(modCache, "golang.org",
		"toolchain@v0.0.1-go1.27.0."+runtime.GOOS+"-"+runtime.GOARCH)
	binary, err = filepath.EvalSymlinks(filepath.Join(root, "bin", name))
	if err != nil {
		return "", "", err
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return "", "", err
	}
	digest, err := bashPPGoDigest(binary)
	if err != nil {
		return "", "", err
	}
	identity := bashPPGoIdentityInfo{Version: "go1.27.0", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Root: root, Binary: binary, SHA256: digest}
	if err := validateBashPPGoIdentity(identity, bashPPGoReviews); err != nil {
		return "", "", err
	}
	return root, binary, nil
}

func bashPPGoDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, f)
	closeErr := f.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}

func validateBashPPGoIdentity(got bashPPGoIdentityInfo, reviews []bashPPGoReview) error {
	wantBinary := filepath.Join(got.Root, "bin", "go")
	if got.GOOS == "windows" {
		wantBinary += ".exe"
	}
	wantBinary, err := filepath.EvalSymlinks(wantBinary)
	if err != nil {
		return err
	}
	wantBinary, err = filepath.Abs(wantBinary)
	if err != nil {
		return err
	}
	if got.Binary != wantBinary {
		return fmt.Errorf("Go binary path %q, want reviewed GOROOT binary %q", got.Binary, wantBinary)
	}
	for _, review := range reviews {
		if got.Version == review.Version && got.GOOS == review.GOOS && got.GOARCH == review.GOARCH {
			if got.SHA256 != review.SHA256 {
				return fmt.Errorf("Go %s %s/%s checksum %s is not reviewed", got.Version, got.GOOS, got.GOARCH, got.SHA256)
			}
			return nil
		}
	}
	return fmt.Errorf("Go toolchain %s %s/%s is not reviewed", got.Version, got.GOOS, got.GOARCH)
}

func (r *Runner) bashPPImport(ctx context.Context, imp *syntax.BashPPImport) {
	if !r.bashPPEnabled() || r.PosixMode() {
		r.shellFallbackImport(ctx, imp)
		return
	}
	req, err := r.bashPPEvalRequest()
	if err != nil {
		r.exit.fatal(err)
		return
	}
	specs := imp.Specs
	if imp.Path != nil {
		specs = []*syntax.BashPPImportSpec{{Alias: imp.Alias, Path: imp.Path}}
	}
	next := make(map[string]string, len(r.bashPPImports)+len(specs))
	for k, v := range r.bashPPImports {
		next[k] = v
	}
	groupPaths := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		pathText := spec.Path.Parts[0].(*syntax.Lit).Value
		path, err := strconv.Unquote(`"` + pathText + `"`)
		if err != nil {
			r.exit.fatal(fmt.Errorf("bash++: invalid interpreted import path: %w", err))
			return
		}
		if pathpkg.Clean(path) != path || strings.HasPrefix(path, "/") || filepath.IsAbs(path) {
			r.exit.fatal(fmt.Errorf("bash++ import %q: path traversal or absolute paths are not allowed", path))
			return
		}
		name, err := r.bashPPTools.eval.Resolve(ctx, req, path)
		if err != nil {
			r.exit.fatal(err)
			return
		}
		if spec.Alias != nil {
			name = spec.Alias.Value
		}
		if _, exists := groupPaths[path]; exists {
			r.exit.fatal(fmt.Errorf("bash++: duplicate import path %q", path))
			return
		}
		groupPaths[path] = struct{}{}
		key := name
		if name == "_" || name == "." {
			key = name + ":" + path
		}
		if oldPath, exists := next[key]; exists {
			if oldPath == path {
				continue
			}
			r.exit.fatal(fmt.Errorf("bash++: import name %s already refers to %q", name, oldPath))
			return
		}
		for oldName, oldPath := range next {
			if oldPath == path {
				r.exit.fatal(fmt.Errorf("bash++: import path %q already uses name %s", path, oldName))
				return
			}
		}
		next[key] = path
	}
	r.bashPPImports = next
}

func (r *Runner) shellFallbackImport(ctx context.Context, imp *syntax.BashPPImport) {
	if imp.Path == nil {
		r.errf("bash++ grouped import evaluated with extensions disabled\n")
		r.exit = exitStatus{code: 2}
		return
	}
	call := &syntax.CallExpr{Args: []*syntax.Word{{Parts: []syntax.WordPart{imp.Kw}}}}
	if imp.Alias != nil {
		call.Args = append(call.Args, &syntax.Word{Parts: []syntax.WordPart{imp.Alias}})
	}
	call.Args = append(call.Args, &syntax.Word{Parts: []syntax.WordPart{imp.Path}})
	r.cmd(ctx, call)
}
