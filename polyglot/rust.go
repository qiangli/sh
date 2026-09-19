package polyglot

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// Rust analyzes declaration-only Rust fences and compiles them to a native
// call worker. The worker is carried in Plan.Artifact, so a lowered Bash++
// program does not need rustc at execution time.
type Rust struct {
	Command     string
	Environment *EnvironmentPlan
}

func (r Rust) executable() string {
	if r.Environment != nil && r.Environment.Executable != "" {
		return r.Environment.Executable
	}
	if r.Command != "" {
		return r.Command
	}
	return "rustc"
}
func (Rust) arguments(Plan) []string         { return nil }
func (Rust) loadRequest(Plan) map[string]any { return nil }
func (Rust) name() string                    { return "Rust" }
func (r Rust) configure(cmd *exec.Cmd) {
	if r.Environment != nil {
		cmd.Dir = r.Environment.Dir
		cmd.Env = append([]string(nil), r.Environment.Env...)
	}
}

type rustExport struct {
	Export
	params     []string
	result     string
	resultWrap bool
}

var rustPublicFunction = regexp.MustCompile(`(?m)^[\t ]*pub[\t ]+fn[\t ]+([[:alpha:]_][[:alnum:]_]*)[\t ]*\(([^)]*)\)[\t ]*(?:->[\t ]*([^\{\n]+))?[\t ]*\{`)

func (r Rust) Analyze(ctx context.Context, source string) ([]Export, error) {
	exports, _, err := r.AnalyzeArtifact(ctx, source)
	return exports, err
}

func (r Rust) AnalyzeArtifact(ctx context.Context, source string) ([]Export, string, error) {
	parsed, err := analyzeRustExports(source)
	if err != nil {
		return nil, "", err
	}
	if len(parsed) == 0 {
		return nil, "", errors.New("no supported public Rust functions")
	}
	generated, err := rustWorkerSource(source, parsed)
	if err != nil {
		return nil, "", err
	}
	dir, err := os.MkdirTemp("", "bashpp-rust-build-")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir)
	sourceFile := filepath.Join(dir, "module.rs")
	output := filepath.Join(dir, "module")
	if runtime.GOOS == "windows" {
		output += ".exe"
	}
	if err := os.WriteFile(sourceFile, []byte(generated), 0o600); err != nil {
		return nil, "", err
	}
	args := append(leadingArgs(r.Environment), "--edition=2024", "-C", "panic=unwind", "-C", "opt-level=0")
	args = append(args, rustLinkerArgs()...)
	args = append(args, "-o", output, sourceFile)
	cmd := exec.CommandContext(ctx, r.executable(), args...)
	r.configure(cmd)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, "", fmt.Errorf("Rust compiler unavailable: %w", err)
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

func analyzeRustExports(source string) ([]rustExport, error) {
	matches := rustPublicFunction.FindAllStringSubmatch(source, -1)
	out := make([]rustExport, 0, len(matches))
	seen := map[string]bool{}
	for _, match := range matches {
		name := match[1]
		if seen[name] {
			return nil, fmt.Errorf("Rust function %s is exported more than once", name)
		}
		seen[name] = true
		var params, bridgeParams []string
		if strings.TrimSpace(match[2]) != "" {
			for _, field := range splitRustTypes(match[2]) {
				_, typ, ok := strings.Cut(field, ":")
				if !ok {
					return nil, fmt.Errorf("Rust function %s has an unsupported parameter %q", name, strings.TrimSpace(field))
				}
				typ = normalizeRustType(typ)
				bridge, ok := rustBridgeType(typ)
				if !ok {
					return nil, fmt.Errorf("Rust function %s has unsupported parameter type %s", name, typ)
				}
				params, bridgeParams = append(params, typ), append(bridgeParams, bridge)
			}
		}
		result := normalizeRustType(match[3])
		wrapped := false
		if result == "" {
			result = "()"
		}
		if inner, ok := rustResultInner(result); ok {
			result, wrapped = inner, true
		}
		if result == "&str" {
			return nil, fmt.Errorf("Rust function %s has unsupported borrowed result type &str; return String instead", name)
		}
		bridgeResult, ok := rustBridgeType(result)
		if result == "()" {
			bridgeResult, ok = "nil", true
		}
		if !ok {
			return nil, fmt.Errorf("Rust function %s has unsupported result type %s", name, result)
		}
		sig := Signature{Params: bridgeParams}
		if bridgeResult != "nil" {
			sig.Results = []string{bridgeResult}
		}
		out = append(out, rustExport{Export: Export{Name: name, Signature: sig}, params: params, result: result, resultWrap: wrapped})
	}
	return out, nil
}

func splitRustTypes(source string) []string {
	var out []string
	start, depth := 0, 0
	for i, r := range source {
		switch r {
		case '<', '[', '(':
			depth++
		case '>', ']', ')':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(source[start:i]))
				start = i + 1
			}
		}
	}
	out = append(out, strings.TrimSpace(source[start:]))
	return out
}

func normalizeRustType(typ string) string {
	typ = strings.TrimSpace(typ)
	typ = strings.Join(strings.Fields(typ), " ")
	return strings.ReplaceAll(strings.ReplaceAll(typ, "< ", "<"), " >", ">")
}

func rustResultInner(typ string) (string, bool) {
	if !strings.HasPrefix(typ, "Result<") || !strings.HasSuffix(typ, ">") {
		return "", false
	}
	parts := splitRustTypes(typ[len("Result<") : len(typ)-1])
	if len(parts) != 2 {
		return "", false
	}
	return normalizeRustType(parts[0]), true
}

func rustBridgeType(typ string) (string, bool) {
	switch typ {
	case "bool":
		return "bool", true
	case "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize":
		return "int", true
	case "f32", "f64":
		return "float64", true
	case "String", "&str":
		return "string", true
	case "Vec<u8>":
		return "bytes", true
	}
	return "", false
}

func rustWorkerSource(source string, exports []rustExport) (string, error) {
	var out strings.Builder
	out.WriteString(source)
	out.WriteString(`
fn __bpp_hex(data: &[u8]) -> String { data.iter().map(|b| format!("{:02x}", b)).collect() }
fn __bpp_unhex(s: &str) -> Result<Vec<u8>, String> {
    if s.len() % 2 != 0 { return Err("invalid byte argument".into()); }
    (0..s.len()).step_by(2).map(|i| u8::from_str_radix(&s[i..i+2], 16).map_err(|e| e.to_string())).collect()
}
fn __bpp_ok(kind: &str, value: String) { println!("\u{1e}BASHPP\tOK\t{}\t{}", kind, __bpp_hex(value.as_bytes())); }
fn __bpp_err(value: String) { println!("\u{1e}BASHPP\tERR\terror\t{}", __bpp_hex(value.as_bytes())); }
fn __bpp_dispatch(args: &[String]) -> Result<(), String> {
    if args.is_empty() { return Err("missing Rust function name".into()); }
    match args[0].as_str() {
`)
	for _, export := range exports {
		fmt.Fprintf(&out, "%q => {\n", export.Name)
		fmt.Fprintf(&out, "if args.len() != %d { return Err(format!(\"Rust function %s expects %d arguments, got {}\", args.len()-1)); }\n", len(export.params)+1, export.Name, len(export.params))
		var callArgs []string
		for i, typ := range export.params {
			arg := fmt.Sprintf("args[%d].as_str()", i+1)
			local := fmt.Sprintf("__bpp_arg%d", i)
			var expr string
			switch typ {
			case "String", "&str":
				fmt.Fprintf(&out, "let %s = String::from_utf8(__bpp_unhex(%s)?).map_err(|e| e.to_string())?;\n", local, arg)
				if typ == "String" {
					expr = local
				} else {
					expr = local + ".as_str()"
				}
			case "Vec<u8>":
				fmt.Fprintf(&out, "let %s = __bpp_unhex(%s)?;\n", local, arg)
				expr = local
			default:
				var err error
				expr, err = rustArgumentExpr(typ, arg)
				if err != nil {
					return "", err
				}
			}
			callArgs = append(callArgs, expr)
		}
		call := fmt.Sprintf("%s(%s)", export.Name, strings.Join(callArgs, ","))
		if export.resultWrap {
			fmt.Fprintf(&out, "let value = match %s { Ok(value) => value, Err(error) => return Err(error.to_string()) };\n", call)
		} else if export.result == "()" {
			fmt.Fprintf(&out, "%s; __bpp_ok(\"nil\", String::new()); return Ok(());\n", call)
			out.WriteString("},\n")
			continue
		} else {
			fmt.Fprintf(&out, "let value = %s;\n", call)
		}
		kind, value, err := rustResultExpr(export.result)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&out, "__bpp_ok(%q, %s); Ok(())\n},\n", kind, value)
	}
	out.WriteString(`_ => Err(format!("unknown Rust function {}", args[0])),
    }
}
fn main() {
    let args: Vec<String> = std::env::args().skip(1).collect();
    let outcome = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| __bpp_dispatch(&args)));
    match outcome {
        Ok(Ok(())) => {},
        Ok(Err(error)) => __bpp_err(error),
        Err(_) => __bpp_err("Rust function panicked".into()),
    }
}
`)
	return out.String(), nil
}

func rustArgumentExpr(typ, arg string) (string, error) {
	switch typ {
	case "bool", "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize", "f32", "f64":
		return arg + ".parse::<" + typ + ">().map_err(|e| e.to_string())?", nil
	}
	return "", fmt.Errorf("unsupported Rust parameter type %s", typ)
}

func rustResultExpr(typ string) (kind, value string, err error) {
	switch typ {
	case "String":
		return "string", "value", nil
	case "&str":
		return "string", "value.to_string()", nil
	case "Vec<u8>":
		return "bytes", "__bpp_hex(&value)", nil
	case "bool":
		return "bool", "value.to_string()", nil
	case "i8", "i16", "i32", "i64", "isize", "u8", "u16", "u32", "u64", "usize":
		return "int", "value.to_string()", nil
	case "f32", "f64":
		return "float64", "value.to_string()", nil
	}
	return "", "", fmt.Errorf("unsupported Rust result type %s", typ)
}

func (m *Module) callRust(ctx context.Context, _ Rust, name string, args []any, kwargs map[string]any) (CallResult, error) {
	if len(kwargs) != 0 {
		return CallResult{}, errors.New("Rust functions do not accept named arguments")
	}
	if m.tempDir == "" {
		dir, err := os.MkdirTemp("", "bashpp-rust-run-")
		if err != nil {
			return CallResult{}, err
		}
		path := filepath.Join(dir, "module")
		if runtime.GOOS == "windows" {
			path += ".exe"
		}
		if err := os.WriteFile(path, []byte(m.plan.Artifact), 0o700); err != nil {
			os.RemoveAll(dir)
			return CallResult{}, err
		}
		m.tempDir = dir
	}
	path := filepath.Join(m.tempDir, "module")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	argv := []string{name}
	var params []string
	for _, export := range m.plan.Exports {
		if export.Name == name {
			params = export.Signature.Params
			break
		}
	}
	for i, arg := range args {
		switch value := arg.(type) {
		case []byte:
			argv = append(argv, hex.EncodeToString(value))
		default:
			encoded := fmt.Sprint(value)
			if i < len(params) && params[i] == "string" {
				encoded = hex.EncodeToString([]byte(encoded))
			}
			argv = append(argv, encoded)
		}
	}
	cmd := exec.CommandContext(ctx, path, argv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	data := stdout.Bytes()
	marker := []byte("\x1eBASHPP\t")
	index := bytes.LastIndex(data, marker)
	if index < 0 {
		if runErr != nil {
			return CallResult{Stdout: string(data), Stderr: stderr.String()}, fmt.Errorf("Rust worker failed: %w", runErr)
		}
		return CallResult{Stdout: string(data), Stderr: stderr.String()}, errors.New("Rust worker returned no result frame")
	}
	result := CallResult{Stdout: string(data[:index]), Stderr: stderr.String()}
	fields := strings.Split(strings.TrimSpace(string(data[index+len(marker):])), "\t")
	if len(fields) != 3 {
		return result, errors.New("invalid Rust worker result frame")
	}
	payload, err := hex.DecodeString(fields[2])
	if err != nil {
		return result, errors.New("invalid Rust worker result payload")
	}
	if fields[0] != "OK" {
		return result, errors.New(string(payload))
	}
	switch fields[1] {
	case "nil":
		result.Value = nil
	case "string":
		result.Value = string(payload)
	case "bytes":
		result.Value, err = hex.DecodeString(string(payload))
	case "bool":
		result.Value, err = strconv.ParseBool(string(payload))
	case "int":
		result.Value, err = strconv.ParseInt(string(payload), 10, 64)
	case "float64":
		result.Value, err = strconv.ParseFloat(string(payload), 64)
	default:
		err = fmt.Errorf("unknown Rust result type %q", fields[1])
	}
	if err != nil {
		return result, err
	}
	return result, nil
}

// rustLinkerArgs names rustc's linker when the embedder's tool resolver
// provides one ("cc-linker": a single program that behaves as cc — bashy
// answers with a wrapper over its provisioned zig cc). Without it rustc
// looks for `cc` on PATH, which a host with no toolchain does not have;
// on Windows the provisioned toolchain is the gnu one, linked the same way.
func rustLinkerArgs() []string {
	if ToolResolver == nil {
		return nil
	}
	argv, _, err := ToolResolver("cc-linker")
	if err != nil || len(argv) != 1 {
		return nil
	}
	return []string{"-C", "linker=" + argv[0]}
}
