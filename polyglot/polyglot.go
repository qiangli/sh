// Package polyglot plans and runs declaration-only foreign source modules.
package polyglot

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Block struct {
	Language string
	Alias    string
	Source   string
	Filename string
	Line     int
}

type Signature struct {
	Params   []string `json:"params"`
	Results  []string `json:"results"`
	Dynamic  bool     `json:"dynamic"`
	Variadic bool     `json:"variadic,omitempty"`
}

type Export struct {
	Name      string    `json:"name"`
	Signature Signature `json:"signature"`
}

type Plan struct {
	ID       string
	Language string
	Alias    string
	Source   string
	Artifact string
	Exports  []Export
}

type Analyzer interface {
	Analyze(context.Context, string) ([]Export, error)
}

type artifactAnalyzer interface {
	AnalyzeArtifact(context.Context, string) ([]Export, string, error)
}

// Prepare aggregates same-language blocks into one immutable module. Alias
// policy is per source unit: all blocks for a language must use the same alias.
func Prepare(ctx context.Context, blocks []Block, analyzers map[string]Analyzer) ([]Plan, error) {
	type aggregate struct {
		alias  string
		blocks []Block
	}
	groups := map[string]*aggregate{}
	for _, block := range blocks {
		lang := canonicalLanguage(block.Language)
		if lang == "" {
			return nil, errors.New("polyglot: empty language")
		}
		group := groups[lang]
		if group == nil {
			group = &aggregate{alias: block.Alias}
			groups[lang] = group
		}
		if group.alias != block.Alias {
			return nil, fmt.Errorf("polyglot: inconsistent aliases for %s", lang)
		}
		group.blocks = append(group.blocks, block)
	}
	langs := make([]string, 0, len(groups))
	for lang := range groups {
		langs = append(langs, lang)
	}
	sort.Strings(langs)
	plans := make([]Plan, 0, len(langs))
	for _, lang := range langs {
		analyzer := analyzers[lang]
		if analyzer == nil {
			return nil, fmt.Errorf("polyglot: unsupported language %q", lang)
		}
		group := groups[lang]
		source := aggregateSource(lang, group.blocks)
		var exports []Export
		var artifact string
		var err error
		if aa, ok := analyzer.(artifactAnalyzer); ok {
			exports, artifact, err = aa.AnalyzeArtifact(ctx, source)
		} else {
			exports, err = analyzer.Analyze(ctx, source)
		}
		if err != nil {
			return nil, fmt.Errorf("polyglot %s: %w", lang, err)
		}
		hash := sha256.Sum256([]byte(lang + "\x00" + group.alias + "\x00" + source + "\x00" + artifact))
		plans = append(plans, Plan{ID: hex.EncodeToString(hash[:]), Language: lang, Alias: group.alias, Source: source, Artifact: artifact, Exports: exports})
	}
	return plans, nil
}

func aggregateSource(language string, blocks []Block) string {
	if language != "c" && language != "cpp" {
		sources := make([]string, len(blocks))
		for i := range blocks {
			sources[i] = blocks[i].Source
		}
		return strings.Join(sources, "\n")
	}
	var out strings.Builder
	for _, block := range blocks {
		name := block.Filename
		if name == "" {
			name = "<bash++ " + language + ">"
		}
		line := block.Line
		if line < 1 {
			line = 1
		}
		fmt.Fprintf(&out, "#line %d %s\n%s", line, strconv.Quote(name), block.Source)
		if !strings.HasSuffix(block.Source, "\n") {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// CanonicalLanguage maps a fence or import language spelling to the name the
// analyzers and runtimes are keyed by. Short spellings are aliases, never
// separate languages: `~~~ts` is `typescript` and `~~~py` is `python`.
func CanonicalLanguage(language string) string {
	return canonicalLanguage(language)
}

func canonicalLanguage(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	switch language {
	case "ts":
		return "typescript"
	case "py":
		return "python"
	case "rs":
		return "rust"
	case "cxx":
		return "cpp"
	}
	return language
}

type Python struct {
	Command     string
	Environment *EnvironmentPlan
}

func (p Python) executable() string {
	if p.Environment != nil && p.Environment.Executable != "" {
		return p.Environment.Executable
	}
	if p.Command != "" {
		return p.Command
	}
	return "python3"
}

func (p Python) Analyze(ctx context.Context, source string) ([]Export, error) {
	name, argv := workerExecArgs(p.executable(), p.pythonArguments(pythonAnalyze, false))
	cmd := exec.CommandContext(ctx, name, argv...)
	p.configure(cmd)
	cmd.Stdin = strings.NewReader(source)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("Python runtime unavailable: %w", err)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New(msg)
	}
	var exports []Export
	if err := json.Unmarshal(stdout.Bytes(), &exports); err != nil {
		return nil, fmt.Errorf("invalid analyzer response: %w", err)
	}
	return exports, nil
}

type CallResult struct {
	Value          any
	Stdout, Stderr string
}

// ErrorDetail is the neutral wire/detail shape for structured foreign worker
// failures. It is deliberately not an error type and it is not tied to any
// command-envelope package: callers that need a domain-specific envelope
// should map this DTO at their own boundary. Workers may still send the legacy
// plain string; decoding normalizes that form into Message while leaving Code
// and Help empty.
type ErrorDetail struct {
	Code    string       `json:"code,omitempty"`
	Message string       `json:"message,omitempty"`
	Help    string       `json:"help,omitempty"`
	Cause   *ErrorDetail `json:"cause,omitempty"`
}

// ForeignErrorDetail returns the structured detail carried by an error
// returned from a foreign worker. This is the stable mapper seam for embedders:
// a separate coreutils/weave run can convert the returned detail to its own
// weavecli.EnvelopeError without making sh import coreutils or weave packages.
func ForeignErrorDetail(err error) (*ErrorDetail, bool) {
	var foreign *foreignError
	if !errors.As(err, &foreign) {
		return nil, false
	}
	detail := foreign.detail.clone()
	return &detail, true
}

type foreignError struct {
	detail ErrorDetail
	cause  error
}

func (e *foreignError) Error() string {
	if e == nil {
		return ""
	}
	message := e.detail.Message
	if message == "" {
		message = e.detail.Code
	}
	if e.detail.Code != "" && e.detail.Message != "" {
		message = e.detail.Code + ": " + e.detail.Message
	}
	if e.cause != nil {
		cause := e.cause.Error()
		if cause != "" {
			message += ": " + cause
		}
	}
	if message == "" {
		return "foreign worker error"
	}
	return message
}

func (e *foreignError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (d *ErrorDetail) UnmarshalJSON(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*d = ErrorDetail{}
		return nil
	}
	var legacy string
	if err := json.Unmarshal(data, &legacy); err == nil {
		*d = ErrorDetail{Message: legacy}
		return nil
	}
	type errorDetail ErrorDetail
	var structured errorDetail
	if err := json.Unmarshal(data, &structured); err != nil {
		return err
	}
	*d = ErrorDetail(structured)
	return nil
}

func (d ErrorDetail) clone() ErrorDetail {
	out := ErrorDetail{Code: d.Code, Message: d.Message, Help: d.Help}
	if d.Cause != nil {
		cause := d.Cause.clone()
		out.Cause = &cause
	}
	return out
}

func (d ErrorDetail) err() error {
	cause := error(nil)
	if d.Cause != nil {
		cause = d.Cause.err()
	}
	return &foreignError{detail: d.clone(), cause: cause}
}

// StringsToAny adapts a typed variadic shell boundary to Module.Call.
func StringsToAny(values []string) []any {
	result := make([]any, len(values))
	for i := range values {
		result[i] = values[i]
	}
	return result
}

// leadingArgs are the arguments a resolved tool carries before any of its
// own (zig cc → [cc]); every launch places them right after the executable.
func leadingArgs(environment *EnvironmentPlan) []string {
	if environment == nil {
		return nil
	}
	return append([]string(nil), environment.ExecutableArgs...)
}

type Runtime interface {
	executable() string
	arguments(Plan) []string
	loadRequest(Plan) map[string]any
	name() string
}

type configuredRuntime interface{ configure(*exec.Cmd) }

// artifactWorker is a Runtime whose persistent worker is the compiled binary
// carried in Plan.Artifact rather than an interpreter reading a script. ensure
// writes the artifact to a private temp binary (under artifactPrefix) and
// launches it directly; a restart after kill rewrites it from the immutable
// plan.
type artifactWorker interface {
	Runtime
	artifactPrefix() string
}

// Embedded adapts an in-process runtime without making polyglot depend on its
// implementation package. Dialect islands use this path and never start an
// external worker.
type Embedded struct {
	RuntimeName string
	AnalyzeFunc func(context.Context, string) ([]Export, error)
	CallFunc    func(context.Context, Plan, string, []any, map[string]any) (CallResult, error)
}

func (e Embedded) Analyze(ctx context.Context, source string) ([]Export, error) {
	return e.AnalyzeFunc(ctx, source)
}
func (e Embedded) executable() string              { return "" }
func (e Embedded) arguments(Plan) []string         { return nil }
func (e Embedded) loadRequest(Plan) map[string]any { return nil }
func (e Embedded) name() string                    { return e.RuntimeName }

func (p Python) arguments(Plan) []string { return p.pythonArguments(pythonWorker, true) }
func (p Python) pythonArguments(script string, unbuffered bool) []string {
	args := append(leadingArgs(p.Environment), pythonLauncherArgs(p.executable())...)
	args = append(args, "-I")
	if unbuffered {
		args = append(args, "-u")
	}
	args = append(args, "-c", script)
	if p.Environment != nil {
		args = append(args, p.Environment.PythonPath...)
	}
	return args
}

// pythonLauncherArgs pins CPython 3 when the resolved runtime is the Windows
// launcher: `py` is not itself an interpreter, and without -3 it starts
// whatever default the host registers.
func pythonLauncherArgs(executable string) []string {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(executable)), ".exe")
	if base == "py" {
		return []string{"-3"}
	}
	return nil
}

// workerExecArgs rewrites a worker launch whose executable is a Windows App
// Execution Alias — a reparse point os/exec refuses with "not supported by
// windows" — to run through cmd.exe, which starts aliases fine. Everything
// else launches unchanged.
func workerExecArgs(path string, args []string) (string, []string) {
	if !execNeedsCmdShim(lstatFileMode(path), runtime.GOOS) {
		return path, args
	}
	return "cmd.exe", append([]string{"/d", "/c", path}, args...)
}

func lstatFileMode(path string) os.FileMode {
	info, err := os.Lstat(path)
	if err != nil {
		return 0
	}
	return info.Mode()
}

func execNeedsCmdShim(mode os.FileMode, goos string) bool {
	return goos == "windows" && mode&os.ModeIrregular != 0
}
func (p Python) loadRequest(plan Plan) map[string]any {
	return map[string]any{"id": 0, "op": "load", "source": plan.Source}
}
func (p Python) name() string { return "Python" }

func (p Python) configure(cmd *exec.Cmd) {
	if p.Environment == nil {
		return
	}
	cmd.Dir = p.Environment.Dir
	cmd.Env = append([]string(nil), p.Environment.Env...)
}

type Module struct {
	plan       Plan
	importPlan *ImportPlan
	runtime    Runtime
	mu         sync.Mutex
	cmd        *exec.Cmd
	in         io.WriteCloser
	out        *bufio.Reader
	outFile    io.ReadCloser
	nextID     uint64
	generation uint64
	pendingOut string
	pendingErr string
	tempDir    string
}

func Start(plan Plan, runtime Runtime) *Module { return &Module{plan: plan, runtime: runtime} }

// StartImport creates a lazy direct Python import. The process and module are
// not loaded until the first operation.
func StartImport(plan ImportPlan) *Module {
	copy := plan.Clone()
	return &Module{
		plan:       Plan{ID: plan.ID, Language: plan.Language, Alias: plan.Alias},
		importPlan: &copy, runtime: Python{Environment: &copy.Environment},
	}
}

func (m *Module) Plan() Plan { return m.plan }

func (m *Module) ensure(ctx context.Context) error {
	if m.cmd != nil {
		return nil
	}
	var name string
	var argv []string
	if worker, ok := m.runtime.(artifactWorker); ok {
		path, err := m.artifactPath(worker.artifactPrefix())
		if err != nil {
			return err
		}
		name, argv = workerExecArgs(path, nil)
	} else {
		name, argv = workerExecArgs(m.runtime.executable(), m.runtime.arguments(m.plan))
	}
	cmd := exec.Command(name, argv...)
	if configured, ok := m.runtime.(configuredRuntime); ok {
		configured.configure(cmd)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	var protocolRead io.ReadCloser
	var protocolWrite *os.File
	_, typeScript := m.runtime.(TypeScript)
	// Windows refuses ExtraFiles outright (syscall.StartProcess: more than
	// three files → EWINDOWS, "not supported by windows"), so there the
	// protocol rides the worker's stdout: every worker falls back to it when
	// fd 3 is absent, and island output is captured per call, never on
	// stdout between calls.
	if runtime.GOOS == "windows" {
		protocolRead, err = cmd.StdoutPipe()
		if err != nil {
			in.Close()
			return err
		}
		cmd.Stderr = io.Discard
	} else {
		protocolRead, protocolWrite, err = os.Pipe()
		if err != nil {
			in.Close()
			return err
		}
		cmd.ExtraFiles = []*os.File{protocolWrite}
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	}
	if err := cmd.Start(); err != nil {
		_ = protocolRead.Close()
		if protocolWrite != nil {
			_ = protocolWrite.Close()
		}
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s runtime unavailable: %w", m.runtime.name(), err)
		}
		return err
	}
	if protocolWrite != nil {
		_ = protocolWrite.Close()
	}
	m.generation++
	m.cmd, m.in, m.out, m.outFile = cmd, in, bufio.NewReader(protocolRead), protocolRead
	load := m.runtime.loadRequest(m.plan)
	if typeScript {
		m.tempDir, err = os.MkdirTemp("", "bashpp-typescript-")
		if err != nil {
			m.kill()
			return err
		}
		load["module_dir"] = m.tempDir
	}
	if m.importPlan != nil {
		load = map[string]any{"id": 0, "op": "import", "module": m.importPlan.Module}
	}
	var response workerResponse
	if err := m.exchangeContext(ctx, load, &response); err != nil {
		m.kill()
		return fmt.Errorf("load %s module: %w", m.runtime.name(), err)
	}
	if !response.OK {
		m.kill()
		return response.Error.err()
	}
	if response.ID != 0 {
		m.kill()
		return fmt.Errorf("load %s module: response ID %d does not match request ID 0", m.runtime.name(), response.ID)
	}
	m.pendingOut, m.pendingErr = response.Stdout, response.Stderr
	return nil
}

func (m *Module) Call(ctx context.Context, name string, args ...any) (CallResult, error) {
	return m.CallKeywords(ctx, name, args, nil)
}

// CallKeywords invokes a source export or direct-module attribute.
func (m *Module) CallKeywords(ctx context.Context, name string, args []any, kwargs map[string]any) (CallResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if embedded, ok := m.runtime.(Embedded); ok {
		return embedded.CallFunc(ctx, m.plan, name, args, kwargs)
	}
	if _, ok := m.runtime.(Go); ok {
		return m.callGo(ctx, name, args, kwargs)
	}
	if _, ok := m.runtime.(Rust); ok && len(kwargs) != 0 {
		return CallResult{}, errors.New("Rust functions do not accept named arguments")
	}
	if _, ok := m.runtime.(C); ok {
		return m.callNativeArtifact(ctx, "C", name, args, kwargs)
	}
	if _, ok := m.runtime.(CPP); ok {
		return m.callNativeArtifact(ctx, "C++", name, args, kwargs)
	}
	if err := m.ensure(ctx); err != nil {
		return CallResult{}, err
	}
	m.nextID++
	encodedArgs, err := m.encodeValue(args)
	if err != nil {
		return CallResult{}, err
	}
	encodedKwargs, err := m.encodeValue(kwargs)
	if err != nil {
		return CallResult{}, err
	}
	request := map[string]any{"id": m.nextID, "op": "call", "name": name, "args": encodedArgs, "kwargs": encodedKwargs}
	return m.request(ctx, request, name)
}

// Handle is an opaque value owned by one worker generation.
type Handle struct {
	module     *Module
	id         uint64
	generation uint64
	Type       string
	Repr       string
	Callable   bool
}

func (h *Handle) GetAttr(ctx context.Context, name string) (CallResult, error) {
	if h == nil || h.module == nil {
		return CallResult{}, errors.New("polyglot: stale or foreign Python handle")
	}
	return h.module.GetAttr(ctx, h, name)
}

func (h *Handle) Call(ctx context.Context, args []any, kwargs map[string]any) (CallResult, error) {
	if h == nil || h.module == nil {
		return CallResult{}, errors.New("polyglot: stale or foreign Python handle")
	}
	return h.module.CallHandle(ctx, h, args, kwargs)
}

func (h *Handle) CallAttr(ctx context.Context, name string, args []any, kwargs map[string]any) (CallResult, error) {
	if h == nil || h.module == nil {
		return CallResult{}, errors.New("polyglot: stale or foreign Python handle")
	}
	return h.module.CallAttr(ctx, h, name, args, kwargs)
}

func (h *Handle) Release(ctx context.Context) error {
	if h == nil || h.module == nil {
		return errors.New("polyglot: stale or foreign Python handle")
	}
	return h.module.Release(ctx, h)
}

func (m *Module) GetAttr(ctx context.Context, handle *Handle, name string) (CallResult, error) {
	if !publicPythonAttribute(name) {
		return CallResult{}, fmt.Errorf("Python attribute %q is private", name)
	}
	return m.handleRequest(ctx, handle, map[string]any{"op": "getattr", "name": name})
}

func (m *Module) CallHandle(ctx context.Context, handle *Handle, args []any, kwargs map[string]any) (CallResult, error) {
	return m.callHandle(ctx, handle, "", args, kwargs)
}

func (m *Module) CallAttr(ctx context.Context, handle *Handle, name string, args []any, kwargs map[string]any) (CallResult, error) {
	if !publicPythonAttribute(name) {
		return CallResult{}, fmt.Errorf("Python attribute %q is private", name)
	}
	return m.callHandle(ctx, handle, name, args, kwargs)
}

func (m *Module) callHandle(ctx context.Context, handle *Handle, name string, args []any, kwargs map[string]any) (CallResult, error) {
	encodedArgs, err := m.encodeValue(args)
	if err != nil {
		return CallResult{}, err
	}
	encodedKwargs, err := m.encodeValue(kwargs)
	if err != nil {
		return CallResult{}, err
	}
	return m.handleRequest(ctx, handle, map[string]any{"op": "call", "name": name, "args": encodedArgs, "kwargs": encodedKwargs})
}

func (m *Module) Release(ctx context.Context, handle *Handle) error {
	_, err := m.handleRequest(ctx, handle, map[string]any{"op": "release"})
	return err
}

func publicPythonAttribute(name string) bool {
	return name == "__name__" || name != "" && name[0] != '_'
}

func (m *Module) handleRequest(ctx context.Context, handle *Handle, request map[string]any) (CallResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensure(ctx); err != nil {
		return CallResult{}, err
	}
	if handle == nil || handle.module != m || handle.generation != m.generation {
		return CallResult{}, errors.New("polyglot: stale or foreign Python handle")
	}
	m.nextID++
	request["id"], request["handle"] = m.nextID, handle.id
	return m.request(ctx, request, request["name"])
}

func (m *Module) request(ctx context.Context, request map[string]any, annotation any) (CallResult, error) {
	var response workerResponse
	if err := m.exchangeContext(ctx, request, &response); err != nil {
		m.kill()
		return CallResult{}, err
	}
	if response.ID != m.nextID {
		m.kill()
		return CallResult{}, fmt.Errorf("%s worker response ID %d does not match request ID %d", m.runtime.name(), response.ID, m.nextID)
	}
	result := CallResult{Stdout: m.pendingOut + response.Stdout, Stderr: m.pendingErr + response.Stderr}
	m.pendingOut, m.pendingErr = "", ""
	if !response.OK {
		return result, response.Error.err()
	}
	decoded, decodeErr := m.decodeValue(response.Result)
	if decodeErr != nil {
		return result, fmt.Errorf("decode %s result: %w", m.runtime.name(), decodeErr)
	}
	result.Value = decoded
	name, _ := annotation.(string)
	for _, export := range m.plan.Exports {
		if export.Name != name || export.Signature.Dynamic {
			continue
		}
		want := "nil"
		if len(export.Signature.Results) > 0 {
			want = export.Signature.Results[0]
		}
		coerced, coerceErr := coerceResult(result.Value, want)
		if coerceErr != nil {
			return result, fmt.Errorf("%s function %s violated its %s result annotation: %w", m.runtime.name(), name, want, coerceErr)
		}
		result.Value = coerced
		break
	}
	return result, nil
}

func (m *Module) exchangeContext(ctx context.Context, request any, response *workerResponse) error {
	done := make(chan error, 1)
	in, out := m.in, m.out
	// On Windows the TypeScript and Rust workers share stdout with island
	// output, so only marker-framed lines are protocol.
	_, typeScript := m.runtime.(TypeScript)
	_, rust := m.runtime.(Rust)
	requireMarker := runtime.GOOS == "windows" && (typeScript || rust)
	go func() { done <- exchange(in, out, request, response, requireMarker) }()
	select {
	case <-ctx.Done():
		_ = m.kill()
		<-done
		return ctx.Err()
	case err := <-done:
		return err
	}
}

func coerceResult(value any, want string) (any, error) {
	switch want {
	case "any":
		return value, nil
	case "object":
		return jsonObjectValue(value)
	case "nil":
		if value == nil {
			return nil, nil
		}
	case "int":
		if _, ok := value.(int64); ok {
			return value, nil
		}
	case "float64":
		switch value := value.(type) {
		case float64:
			return value, nil
		case int64:
			return float64(value), nil
		}
	case "string":
		if _, ok := value.(string); ok {
			return value, nil
		}
	case "bool":
		if _, ok := value.(bool); ok {
			return value, nil
		}
	case "bytes":
		if _, ok := value.([]byte); ok {
			return value, nil
		}
	}
	return nil, fmt.Errorf("got %T", value)
}

func jsonObjectValue(value any) (any, error) {
	switch value := value.(type) {
	case nil, bool, string, int64, float64:
		return value, nil
	case []any:
		out := make([]any, len(value))
		for i := range value {
			var err error
			out[i], err = jsonObjectValue(value[i])
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			var err error
			out[key], err = jsonObjectValue(item)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("got non-JSON %T", value)
	}
}

func (m *Module) Close() error { m.mu.Lock(); defer m.mu.Unlock(); return m.kill() }

func (m *Module) kill() error {
	if m.cmd == nil {
		if m.tempDir != "" {
			_ = os.RemoveAll(m.tempDir)
			m.tempDir = ""
		}
		return nil
	}
	_ = m.in.Close()
	err := m.cmd.Process.Kill()
	_ = m.cmd.Wait()
	if m.outFile != nil {
		_ = m.outFile.Close()
	}
	m.cmd, m.in, m.out, m.outFile = nil, nil, nil, nil
	m.pendingOut, m.pendingErr = "", ""
	if m.tempDir != "" {
		_ = os.RemoveAll(m.tempDir)
		m.tempDir = ""
	}
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

type workerResponse struct {
	ID             uint64    `json:"id"`
	OK             bool      `json:"ok"`
	Result         any       `json:"result"`
	Error          workerErr `json:"error"`
	Stdout, Stderr string
}

type workerErr struct {
	ErrorDetail
	set bool
}

func (e *workerErr) UnmarshalJSON(data []byte) error {
	e.set = !bytes.Equal(bytes.TrimSpace(data), []byte("null"))
	return e.ErrorDetail.UnmarshalJSON(data)
}

func (e workerErr) err() error {
	if !e.set {
		return errors.New("foreign worker error")
	}
	return e.ErrorDetail.err()
}

func exchange(in io.Writer, out *bufio.Reader, request any, response *workerResponse, requireMarker bool) error {
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if _, err := in.Write(append(data, '\n')); err != nil {
		return err
	}
	var got workerResponse
	for {
		line, err := out.ReadBytes('\n')
		if err != nil {
			return err
		}
		if marker := bytes.Index(line, []byte("\x1eBASHPP")); marker >= 0 {
			line = line[marker+len("\x1eBASHPP"):]
		} else if requireMarker || len(bytes.TrimSpace(line)) == 0 || bytes.TrimSpace(line)[0] != '{' {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		if err := dec.Decode(&got); err != nil {
			return fmt.Errorf("invalid worker response: %w", err)
		}
		break
	}
	if response != nil {
		*response = got
	}
	return nil
}

func (m *Module) encodeValue(v any) (any, error) {
	switch x := v.(type) {
	case *Handle:
		if x == nil || x.module != m || x.generation != m.generation {
			return nil, errors.New("polyglot: stale or foreign Python handle")
		}
		return map[string]any{"$handle": x.id}, nil
	case []byte:
		return map[string]any{"$bytes": base64.StdEncoding.EncodeToString(x)}, nil
	case []any:
		out := make([]any, len(x))
		for i := range x {
			var err error
			out[i], err = m.encodeValue(x[i])
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	case map[string]any:
		out := map[string]any{}
		for k, v := range x {
			var err error
			out[k], err = m.encodeValue(v)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	default:
		return v, nil
	}
}

func (m *Module) decodeValue(v any) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		if raw, ok := x["$handle"].(map[string]any); ok {
			id, err := numericID(raw["id"])
			if err != nil {
				return nil, err
			}
			h := &Handle{module: m, id: id, generation: m.generation}
			h.Type, _ = raw["type"].(string)
			h.Repr, _ = raw["repr"].(string)
			h.Callable, _ = raw["callable"].(bool)
			return h, nil
		}
		if raw, ok := x["$bytes"].(string); ok {
			return base64.StdEncoding.DecodeString(raw)
		}
		out := map[string]any{}
		for k, v := range x {
			var err error
			out[k], err = m.decodeValue(v)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i := range x {
			var err error
			out[i], err = m.decodeValue(x[i])
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i, nil
		}
		return x.Float64()
	default:
		return v, nil
	}
}

func numericID(v any) (uint64, error) {
	switch n := v.(type) {
	case json.Number:
		return strconv.ParseUint(string(n), 10, 64)
	case float64:
		return uint64(n), nil
	default:
		return 0, fmt.Errorf("invalid Python handle id %v", v)
	}
}

const pythonPathBootstrap = `
import sys
_pythonpath=sys.argv[1:]
del sys.argv[1:]
sys.path[:0]=_pythonpath
`

const pythonAnalyze = pythonPathBootstrap + `
import ast, json
src=sys.stdin.read()
tree=ast.parse(src)
nodes=tree.body
if nodes and isinstance(nodes[0], ast.Expr) and isinstance(nodes[0].value, ast.Constant) and isinstance(nodes[0].value.value, str): nodes=nodes[1:]
types={'int':'int','float':'float64','str':'string','bool':'bool','bytes':'bytes','None':'nil','Any':'any'}
def annotation_type(node):
    if isinstance(node,ast.Name): return types.get(node.id)
    if isinstance(node,ast.Constant) and node.value is None: return 'nil'
    if not isinstance(node,ast.Subscript) or not isinstance(node.value,ast.Name): return None
    origin=node.value.id
    if origin in ('list','List'):
        return 'object' if annotation_type(node.slice) is not None else None
    if origin in ('dict','Dict'):
        parts=node.slice.elts if isinstance(node.slice,ast.Tuple) else []
        if len(parts)!=2 or annotation_type(parts[0])!='string' or annotation_type(parts[1]) is None: return None
        return 'object'
    return None
out=[]
for node in nodes:
    if not isinstance(node, ast.FunctionDef): raise SyntaxError('only module docstrings and synchronous function declarations are allowed')
    if node.decorator_list: raise SyntaxError('decorated functions are not allowed')
    if node.args.posonlyargs or node.args.kwonlyargs or node.args.vararg or node.args.kwarg: dynamic=True
    else: dynamic=False
    for default in list(node.args.defaults)+list(node.args.kw_defaults):
        if default is not None:
            try: ast.literal_eval(default)
            except Exception: raise SyntaxError('function defaults must be literals')
    params=[]
    for arg in list(node.args.args):
        typ=annotation_type(arg.annotation)
        if typ is None: dynamic=True
        params.append(typ or 'any')
    ret=annotation_type(node.returns)
    if ret is None: dynamic=True
    if not node.name.startswith('_'): out.append({'name':node.name,'signature':{'params':params,'results':[] if ret=='nil' else [ret or 'any'],'dynamic':dynamic}})
print(json.dumps(out,separators=(',',':')))
`

const pythonWorker = pythonPathBootstrap + `
import ast, base64, importlib, io, json, os, sys, tempfile, traceback
try:
    protocol=os.fdopen(3,'w',buffering=1,newline='\n')
except OSError:
    # No fd 3 (Windows): the protocol rides stdout; island output is captured per call.
    protocol=io.TextIOWrapper(io.FileIO(1,'w',closefd=False),line_buffering=True,newline='\n')
ns={'__name__':'__bashpp__'}
module=None
handles={}
next_handle=0
def dec(v):
    if isinstance(v,dict) and set(v)=={'$bytes'}: return base64.b64decode(v['$bytes'])
    if isinstance(v,dict) and set(v)=={'$handle'}:
        key=int(v['$handle'])
        if key not in handles: raise ValueError('stale Python handle')
        return handles[key]
    if isinstance(v,list): return [dec(x) for x in v]
    if isinstance(v,dict): return {k:dec(x) for k,x in v.items()}
    return v
def enc(v):
    global next_handle
    if isinstance(v,bytes): return {'$bytes':base64.b64encode(v).decode('ascii')}
    if isinstance(v,(list,tuple)): return [enc(x) for x in v]
    if isinstance(v,dict) and all(isinstance(k,str) for k in v): return {k:enc(x) for k,x in v.items()}
    if v is None or isinstance(v,(bool,int,float,str)): return v
    next_handle+=1; handles[next_handle]=v
    try: shown=repr(v)
    except Exception: shown='<unrepresentable>'
    return {'$handle':{'id':next_handle,'type':type(v).__name__,'repr':shown,'callable':callable(v)}}
def attr(target,name):
    if name.startswith('_') and name!='__name__': raise AttributeError('private Python attribute: '+name)
    return getattr(target,name)
def envelope_error(exc, trace=None, seen=None, depth=0):
    if trace is None: trace=''.join(traceback.format_exception(type(exc),exc,exc.__traceback__))
    if seen is None: seen=set()
    if id(exc) in seen:
        return {'code':type(exc).__name__,'message':'cycle in Python exception cause chain'}
    if depth>=16:
        return {'code':type(exc).__name__,'message':'Python exception cause chain truncated'}
    seen.add(id(exc))
    message=str(exc)
    cause=getattr(exc,'__cause__',None) or (getattr(exc,'__context__',None) if not getattr(exc,'__suppress_context__',False) else None)
    out={'code':type(exc).__name__,'message':message or type(exc).__name__,'help':trace}
    if cause is not None: out['cause']=envelope_error(cause, None, seen, depth+1)
    seen.remove(id(exc))
    return out
class CaptureFailure(Exception):
    def __init__(self,error,out,err): self.error,self.out,self.err=error,out,err
def capture(operation):
    sys.stdout.flush(); sys.stderr.flush()
    oldout,olderr=os.dup(1),os.dup(2)
    with tempfile.TemporaryFile() as out, tempfile.TemporaryFile() as err:
        failure=None
        try:
            os.dup2(out.fileno(),1); os.dup2(err.fileno(),2)
            value=operation()
        except BaseException as exc:
            failure=envelope_error(exc)
        finally:
            sys.stdout.flush(); sys.stderr.flush()
            os.dup2(oldout,1); os.dup2(olderr,2); os.close(oldout); os.close(olderr)
        out.seek(0); err.seek(0)
        captured_out,captured_err=out.read().decode(errors='replace'),err.read().decode(errors='replace')
        if failure is not None: raise CaptureFailure(failure,captured_out,captured_err)
        return value,captured_out,captured_err
for line in sys.stdin:
    req=json.loads(line); rid=req.get('id',0); captured_out=captured_err=''
    try:
        op=req['op']
        if op=='load':
            def action():
                source='from __future__ import annotations\n'+req['source']
                exec(compile(source,'<bash++ python>','exec'),ns,ns)
            value,captured_out,captured_err=capture(action)
        elif op=='import':
            def action():
                global module
                module=importlib.import_module(req['module'])
            value,captured_out,captured_err=capture(action)
        elif op=='getattr':
            value,captured_out,captured_err=capture(lambda:attr(handles[int(req['handle'])],req['name']))
        elif op=='call':
            def action():
                target=handles[int(req['handle'])] if 'handle' in req else module if module is not None else ns
                if req.get('name'): target=attr(target,req['name']) if module is not None or 'handle' in req else target[req['name']]
                if not callable(target): raise TypeError('Python target is not callable')
                return target(*dec(req.get('args',[])),**dec(req.get('kwargs',{})))
            value,captured_out,captured_err=capture(action)
        elif op=='release':
            handles.pop(int(req['handle']),None); value=None
        else: raise ValueError('unknown operation')
        res={'id':rid,'ok':True,'result':enc(value),'stdout':captured_out,'stderr':captured_err}
    except CaptureFailure as failure:
        res={'id':rid,'ok':False,'error':failure.error,'stdout':failure.out,'stderr':failure.err}
    except Exception as exc:
        res={'id':rid,'ok':False,'error':envelope_error(exc),'stdout':captured_out,'stderr':captured_err}
    protocol.write(json.dumps(res,separators=(',',':'))+'\n')
`
