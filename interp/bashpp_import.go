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
	"path/filepath"
	"runtime"
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
}

type bashPPEvaluator interface {
	Resolve(context.Context, bashPPEvalRequest, string) (string, error)
	Call(context.Context, bashPPEvalRequest) error
}

// bashPPToolchain is deliberately package-private. The zero value selects the
// Go toolchain which built this package, never an unrelated executable found
// first on PATH. Tests may inject the exact evaluator and identity under
// review without exposing a second interpreter API to embedders.
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
		return "", err
	}
	var info struct {
		Name     string
		Standard bool
	}
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		return "", fmt.Errorf("go list %q: %w", path, err)
	}
	if !info.Standard {
		return "", fmt.Errorf("bash++ import %q: package is not in the selected Go standard library", path)
	}
	if !syntax.ValidName(info.Name) {
		return "", fmt.Errorf("bash++ import %q: invalid package name %q", path, info.Name)
	}
	return info.Name, nil
}

func (nativeBashPPEvaluator) Call(ctx context.Context, req bashPPEvalRequest) error {
	if len(req.Selector) < 2 {
		return errors.New("bash++: selector call requires an imported package")
	}
	path, ok := req.Imports[req.Selector[0]]
	if !ok {
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
	for i, text := range req.Args {
		expr, err := parser.ParseExpr(text)
		if err != nil {
			return fmt.Errorf("bash++: invalid Go argument %d: %w", i+1, err)
		}
		args[i] = expr
	}
	file := &ast.File{Name: ast.NewIdent("main"), Decls: []ast.Decl{
		&ast.GenDecl{Tok: token.IMPORT, Specs: []ast.Spec{&ast.ImportSpec{
			Name: ast.NewIdent(req.Selector[0]), Path: &ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(path)},
		}}},
		&ast.FuncDecl{Name: ast.NewIdent("main"), Type: &ast.FuncType{Params: &ast.FieldList{}},
			Body: &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: selector, Args: args}}}}},
	}}
	var src bytes.Buffer
	if err := format.Node(&src, token.NewFileSet(), file); err != nil {
		return fmt.Errorf("bash++: construct selector call: %w", err)
	}
	f, err := os.CreateTemp("", "bashpp-*.go")
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
		return err
	}
	cmd := exec.CommandContext(ctx, bin)
	cmd.Dir, cmd.Env = req.Dir, req.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = req.Stdin, req.Stdout, req.Stderr
	return cmd.Run()
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
		Stdout: r.stdout, Stderr: r.stderr, Imports: r.bashPPImports}, nil
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
	bootstrap := filepath.Join(runtime.GOROOT(), "bin", name)
	cmd := exec.Command(bootstrap, "env", "GOROOT", "GOOS", "GOARCH")
	cmd.Env = setEnvString(os.Environ(), "GOTOOLCHAIN", "go1.27.0")
	cmd.Env = setEnvString(cmd.Env, "GOROOT", runtime.GOROOT())
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
	f, err := os.Open(real)
	if err != nil {
		return bashPPGoIdentityInfo{}, err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, f)
	closeErr := f.Close()
	if copyErr != nil {
		return bashPPGoIdentityInfo{}, copyErr
	}
	if closeErr != nil {
		return bashPPGoIdentityInfo{}, closeErr
	}
	identity := bashPPGoIdentityInfo{Version: versionFields[2], GOOS: goos, GOARCH: goarch,
		Root: root, Binary: real, SHA256: fmt.Sprintf("%x", h.Sum(nil))}
	if err := validateBashPPGoIdentity(identity, bashPPGoReviews); err != nil {
		return bashPPGoIdentityInfo{}, err
	}
	return identity, nil
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
	pathText := imp.Path.Parts[0].(*syntax.Lit).Value
	path, err := strconv.Unquote(`"` + pathText + `"`)
	if err != nil { // The syntax parser has already validated this invariant.
		r.exit.fatal(fmt.Errorf("bash++: invalid interpreted import path: %w", err))
		return
	}
	name, err := r.bashPPTools.eval.Resolve(ctx, req, path)
	if err != nil {
		r.exit.fatal(err)
		return
	}
	if imp.Alias != nil {
		name = imp.Alias.Value
	}
	if oldPath, exists := r.bashPPImports[name]; exists {
		if oldPath == path {
			return
		}
		r.exit.fatal(fmt.Errorf("bash++: import name %s already refers to %q", name, oldPath))
		return
	}
	for oldName, oldPath := range r.bashPPImports {
		if oldPath == path {
			r.exit.fatal(fmt.Errorf("bash++: import path %q already uses name %s", path, oldName))
			return
		}
	}
	next := make(map[string]string, len(r.bashPPImports)+1)
	for k, v := range r.bashPPImports {
		next[k] = v
	}
	next[name] = path
	r.bashPPImports = next
}

func (r *Runner) shellFallbackImport(ctx context.Context, imp *syntax.BashPPImport) {
	call := &syntax.CallExpr{Args: []*syntax.Word{{Parts: []syntax.WordPart{imp.Kw}}}}
	if imp.Alias != nil {
		call.Args = append(call.Args, &syntax.Word{Parts: []syntax.WordPart{imp.Alias}})
	}
	call.Args = append(call.Args, &syntax.Word{Parts: []syntax.WordPart{imp.Path}})
	r.cmd(ctx, call)
}
