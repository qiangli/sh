package polyglot

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Go compiles declaration-only Go fences with the checkout's selected Go
// command. The generated files exist in the module only through -overlay.
type Go struct {
	Command     string
	Environment *EnvironmentPlan
}

func (g Go) executable() string {
	if g.Environment != nil && g.Environment.Executable != "" {
		return g.Environment.Executable
	}
	if g.Command != "" {
		return g.Command
	}
	return "go"
}
func (Go) arguments(Plan) []string         { return nil }
func (Go) loadRequest(Plan) map[string]any { return nil }
func (Go) name() string                    { return "Go" }
func (g Go) configure(cmd *exec.Cmd) {
	if g.Environment != nil {
		cmd.Dir = g.Environment.Dir
		cmd.Env = append([]string(nil), g.Environment.Env...)
	}
}

type goExport struct {
	Export
	params      []string
	result      string
	resultError bool
}

func (g Go) Analyze(ctx context.Context, source string) ([]Export, error) {
	exports, _, err := g.AnalyzeArtifact(ctx, source)
	return exports, err
}

func (g Go) AnalyzeArtifact(ctx context.Context, source string) ([]Export, string, error) {
	parsed, err := analyzeGoExports(source)
	if err != nil {
		return nil, "", err
	}
	if len(parsed) == 0 {
		return nil, "", errors.New("no supported exported Go functions")
	}
	moduleSource := "package main\n\n" + source
	workerSource := goWorkerSource(parsed)
	dir, err := os.MkdirTemp("", "bashpp-go-build-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir)
	moduleBacking := filepath.Join(dir, "module.go")
	workerBacking := filepath.Join(dir, "worker.go")
	overlayFile := filepath.Join(dir, "overlay.json")
	output := filepath.Join(dir, "module")
	if runtime.GOOS == "windows" {
		output += ".exe"
	}
	root := ""
	if g.Environment != nil {
		root = g.Environment.Root
	}
	if root == "" {
		root, err = os.Getwd()
		if err != nil {
			return nil, "", err
		}
	}
	// The go command walks the working directory as $PWD spells it, and an
	// overlay key must match that spelling byte for byte: one cleaned
	// spelling serves as the root, the process directory and its PWD.
	root = filepath.Clean(root)
	hash := strconv.FormatInt(int64(len(source)), 10)
	// Use a virtual subdirectory so a module whose root is a library package
	// does not collide with the generated package main. Keeping it beneath the
	// module still permits imports of that module's internal packages.
	virtualDir := filepath.Join(root, ".bashpp-overlay-"+hash)
	moduleVirtual := filepath.Join(virtualDir, "fence.go")
	workerVirtual := filepath.Join(virtualDir, "worker.go")
	for name, data := range map[string]string{moduleBacking: moduleSource, workerBacking: workerSource} {
		if err := os.WriteFile(name, []byte(data), 0o600); err != nil {
			return nil, "", err
		}
	}
	replace := map[string]string{
		moduleVirtual: moduleBacking,
		workerVirtual: workerBacking,
	}
	args := []string{"build", "-overlay=" + overlayFile, "-o", output, moduleVirtual, workerVirtual}
	if g.Environment != nil && g.Environment.ModuleFile != "" {
		// A gomod fence is the module: present it (and its go.sum) at the
		// root and read/write the real files beside the fence.
		modFile := g.Environment.ModuleFile
		sumFile := strings.TrimSuffix(modFile, ".mod") + ".sum"
		if _, err := os.Stat(sumFile); err != nil {
			if err := os.WriteFile(sumFile, nil, 0o644); err != nil {
				return nil, "", err
			}
		}
		replace[filepath.Join(root, "go.mod")] = modFile
		replace[filepath.Join(root, "go.sum")] = sumFile
		args = append([]string{"build", "-overlay=" + overlayFile, "-modfile=" + modFile, "-o", output, moduleVirtual, workerVirtual}, args[len(args):]...)
	}
	overlay, err := json.Marshal(map[string]any{"Replace": replace})
	if err != nil {
		return nil, "", err
	}
	if err := os.WriteFile(overlayFile, overlay, 0o600); err != nil {
		return nil, "", err
	}
	if g.Environment != nil && g.Environment.Manager == "bashy" {
		args = append([]string{"go"}, args...)
	}
	args = append(leadingArgs(g.Environment), args...)
	cmd := exec.CommandContext(ctx, g.executable(), args...)
	g.configure(cmd)
	if g.Environment != nil && g.Environment.ModuleFile != "" {
		cmd.Dir = root
		cmd.Env = append(cmd.Env, "PWD="+root)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, "", fmt.Errorf("Go toolchain unavailable: %w", err)
		}
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, "", errors.New(message)
	}
	binary, err := os.ReadFile(output)
	if err != nil {
		return nil, "", err
	}
	exports := make([]Export, len(parsed))
	for i := range parsed {
		exports[i] = parsed[i].Export
	}
	return exports, string(binary), nil
}

func analyzeGoExports(source string) ([]goExport, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fence.go", "package main\n"+source, parser.AllErrors)
	if err != nil {
		return nil, err
	}
	var out []goExport
	seen := map[string]bool{}
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.GenDecl:
			if decl.Tok != token.IMPORT {
				return nil, fmt.Errorf("Go source fences allow only imports and function declarations")
			}
		case *ast.FuncDecl:
			if decl.Recv != nil {
				return nil, fmt.Errorf("Go source fences do not export methods")
			}
			if decl.Name.Name == "main" || strings.HasPrefix(decl.Name.Name, "__bpp") {
				return nil, fmt.Errorf("Go function name %s is reserved by the fence worker", decl.Name.Name)
			}
			if !ast.IsExported(decl.Name.Name) {
				continue
			}
			if seen[decl.Name.Name] {
				return nil, fmt.Errorf("Go function %s is exported more than once", decl.Name.Name)
			}
			seen[decl.Name.Name] = true
			export := goExport{Export: Export{Name: decl.Name.Name}}
			for _, field := range decl.Type.Params.List {
				if _, ok := field.Type.(*ast.Ellipsis); ok {
					return nil, fmt.Errorf("Go function %s is variadic", decl.Name.Name)
				}
				typ, bridge, ok := goBridgeType(field.Type)
				if !ok {
					return nil, fmt.Errorf("Go function %s has an unsupported parameter type", decl.Name.Name)
				}
				count := len(field.Names)
				if count == 0 {
					count = 1
				}
				for range count {
					export.params = append(export.params, typ)
					export.Signature.Params = append(export.Signature.Params, bridge)
				}
			}
			var results []ast.Expr
			for _, field := range decl.Type.Results.List {
				count := len(field.Names)
				if count == 0 {
					count = 1
				}
				for range count {
					results = append(results, field.Type)
				}
			}
			if len(results) > 2 {
				return nil, fmt.Errorf("Go function %s has too many results", decl.Name.Name)
			}
			if len(results) > 0 && isGoIdent(results[len(results)-1], "error") {
				export.resultError = true
				results = results[:len(results)-1]
			}
			if len(results) > 1 {
				return nil, fmt.Errorf("Go function %s must return at most one value plus error", decl.Name.Name)
			}
			if len(results) == 1 {
				typ, bridge, ok := goBridgeType(results[0])
				if !ok || bridge == "nil" {
					return nil, fmt.Errorf("Go function %s has an unsupported result type", decl.Name.Name)
				}
				export.result = typ
				export.Signature.Results = []string{bridge}
			}
			out = append(out, export)
		}
	}
	return out, nil
}

func goBridgeType(expr ast.Expr) (string, string, bool) {
	if id, ok := expr.(*ast.Ident); ok {
		switch id.Name {
		case "bool", "string":
			return id.Name, id.Name, true
		case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "byte", "rune":
			return id.Name, "int", true
		case "float32", "float64":
			return id.Name, "float64", true
		}
	}
	if array, ok := expr.(*ast.ArrayType); ok && array.Len == nil && isGoIdent(array.Elt, "byte") {
		return "[]byte", "bytes", true
	}
	return "", "", false
}

func isGoIdent(expr ast.Expr, name string) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == name
}

func goWorkerSource(exports []goExport) string {
	var out strings.Builder
	out.WriteString(`package main

import (
	__bpp_json "encoding/json"
	__bpp_fmt "fmt"
	__bpp_os "os"
)

type __bpp_request struct { Name string ` + "`json:\"name\"`" + `; Args []__bpp_json.RawMessage ` + "`json:\"args\"`" + ` }
type __bpp_response struct { OK bool ` + "`json:\"ok\"`" + `; Kind string ` + "`json:\"kind,omitempty\"`" + `; Result any ` + "`json:\"result,omitempty\"`" + `; Error string ` + "`json:\"error,omitempty\"`" + ` }
func __bpp_write(response __bpp_response) { data,_:=__bpp_json.Marshal(response); __bpp_fmt.Fprintln(__bpp_os.Stdout,"\x1eBASHPP"+string(data)) }
func __bpp_fail(err error) { __bpp_write(__bpp_response{Error:err.Error()}) }
func main() {
	defer func(){ if value:=recover(); value!=nil { __bpp_fail(__bpp_fmt.Errorf("Go function panicked: %v",value)) } }()
	var request __bpp_request
	if err:=__bpp_json.NewDecoder(__bpp_os.Stdin).Decode(&request); err!=nil { __bpp_fail(err); return }
	switch request.Name {
`)
	for _, export := range exports {
		fmt.Fprintf(&out, "case %q:\n", export.Name)
		fmt.Fprintf(&out, "if len(request.Args)!=%d { __bpp_fail(__bpp_fmt.Errorf(\"Go function %s expects %d arguments, got %%d\",len(request.Args))); return }\n", len(export.params), export.Name, len(export.params))
		var args []string
		for i, typ := range export.params {
			name := fmt.Sprintf("__bpp_arg%d", i)
			fmt.Fprintf(&out, "var %s %s\nif err:=__bpp_json.Unmarshal(request.Args[%d],&%s); err!=nil { __bpp_fail(err); return }\n", name, typ, i, name)
			args = append(args, name)
		}
		call := export.Name + "(" + strings.Join(args, ",") + ")"
		switch {
		case export.result != "" && export.resultError:
			fmt.Fprintf(&out, "value,err:=%s\nif err!=nil { __bpp_fail(err); return }\n__bpp_write(__bpp_response{OK:true,Kind:%q,Result:value})\n", call, export.Signature.Results[0])
		case export.resultError:
			fmt.Fprintf(&out, "if err:=%s; err!=nil { __bpp_fail(err); return }\n__bpp_write(__bpp_response{OK:true,Kind:\"nil\"})\n", call)
		case export.result != "":
			fmt.Fprintf(&out, "value:=%s\n__bpp_write(__bpp_response{OK:true,Kind:%q,Result:value})\n", call, export.Signature.Results[0])
		default:
			fmt.Fprintf(&out, "%s\n__bpp_write(__bpp_response{OK:true,Kind:\"nil\"})\n", call)
		}
	}
	out.WriteString("default: __bpp_fail(__bpp_fmt.Errorf(\"unknown Go function %s\",request.Name))\n}\n}\n")
	return out.String()
}

func (m *Module) callGo(ctx context.Context, name string, args []any, kwargs map[string]any) (CallResult, error) {
	if len(kwargs) != 0 {
		return CallResult{}, errors.New("Go functions do not accept named arguments")
	}
	path, err := m.artifactPath("bashpp-go-run-")
	if err != nil {
		return CallResult{}, err
	}
	request, err := json.Marshal(map[string]any{"name": name, "args": args})
	if err != nil {
		return CallResult{}, err
	}
	cmd := exec.CommandContext(ctx, path)
	cmd.Stdin = bytes.NewReader(request)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	data, marker := stdout.Bytes(), []byte("\x1eBASHPP")
	index := bytes.LastIndex(data, marker)
	if index < 0 {
		if runErr != nil {
			return CallResult{Stdout: string(data), Stderr: stderr.String()}, fmt.Errorf("Go worker failed: %w", runErr)
		}
		return CallResult{Stdout: string(data), Stderr: stderr.String()}, errors.New("Go worker returned no result frame")
	}
	result := CallResult{Stdout: string(data[:index]), Stderr: stderr.String()}
	var response struct {
		OK     bool            `json:"ok"`
		Kind   string          `json:"kind"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data[index+len(marker):]), &response); err != nil {
		return result, fmt.Errorf("invalid Go worker result frame: %w", err)
	}
	if !response.OK {
		return result, errors.New(response.Error)
	}
	switch response.Kind {
	case "nil":
	case "string":
		err = json.Unmarshal(response.Result, &result.Value)
	case "bytes":
		var encoded string
		if err = json.Unmarshal(response.Result, &encoded); err == nil {
			result.Value, err = base64.StdEncoding.DecodeString(encoded)
		}
	case "bool":
		var value bool
		err = json.Unmarshal(response.Result, &value)
		result.Value = value
	case "int":
		var value json.Number
		err = json.Unmarshal(response.Result, &value)
		if err == nil {
			result.Value, err = strconv.ParseInt(value.String(), 10, 64)
		}
	case "float64":
		var value float64
		err = json.Unmarshal(response.Result, &value)
		result.Value = value
	default:
		err = fmt.Errorf("unknown Go result type %q", response.Kind)
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

func (m *Module) artifactPath(prefix string) (string, error) {
	if m.tempDir == "" {
		dir, err := os.MkdirTemp("", prefix)
		if err != nil {
			return "", err
		}
		path := filepath.Join(dir, "module")
		if runtime.GOOS == "windows" {
			path += ".exe"
		}
		if err := os.WriteFile(path, []byte(m.plan.Artifact), 0o700); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
		m.tempDir = dir
	}
	path := filepath.Join(m.tempDir, "module")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	return path, nil
}
