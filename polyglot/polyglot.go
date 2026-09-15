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
	Params  []string `json:"params"`
	Results []string `json:"results"`
	Dynamic bool     `json:"dynamic"`
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
		alias   string
		sources []string
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
		group.sources = append(group.sources, block.Source)
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
		source := strings.Join(group.sources, "\n")
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
	cmd := exec.CommandContext(ctx, p.executable(), p.pythonArguments(pythonAnalyze, false)...)
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

type Runtime interface {
	executable() string
	arguments(Plan) []string
	loadRequest(Plan) map[string]any
	name() string
}

type configuredRuntime interface{ configure(*exec.Cmd) }

func (p Python) arguments(Plan) []string { return p.pythonArguments(pythonWorker, true) }
func (p Python) pythonArguments(script string, unbuffered bool) []string {
	args := []string{"-I"}
	if unbuffered {
		args = append(args, "-u")
	}
	args = append(args, "-c", script)
	if p.Environment != nil {
		args = append(args, p.Environment.PythonPath...)
	}
	return args
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
	cmd := exec.Command(m.runtime.executable(), m.runtime.arguments(m.plan)...)
	if configured, ok := m.runtime.(configuredRuntime); ok {
		configured.configure(cmd)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	protocolRead, protocolWrite, err := os.Pipe()
	if err != nil {
		in.Close()
		return err
	}
	cmd.ExtraFiles = []*os.File{protocolWrite}
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
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
		return errors.New(response.Error)
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
		return result, errors.New(response.Error)
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
	go func() { done <- m.exchange(request, response) }()
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

func (m *Module) Close() error { m.mu.Lock(); defer m.mu.Unlock(); return m.kill() }

func (m *Module) kill() error {
	if m.cmd == nil {
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
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

type workerResponse struct {
	ID                    uint64 `json:"id"`
	OK                    bool   `json:"ok"`
	Result                any    `json:"result"`
	Error, Stdout, Stderr string
}

func (m *Module) exchange(request any, response *workerResponse) error {
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if _, err := m.in.Write(append(data, '\n')); err != nil {
		return err
	}
	line, err := m.out.ReadBytes('\n')
	if err != nil {
		return err
	}
	var got workerResponse
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	if err := dec.Decode(&got); err != nil {
		return fmt.Errorf("invalid worker response: %w", err)
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
        name=arg.annotation.id if isinstance(arg.annotation,ast.Name) else None
        if name not in types: dynamic=True
        params.append(types.get(name,'any'))
    ret=node.returns.id if isinstance(node.returns,ast.Name) else ('None' if isinstance(node.returns,ast.Constant) and node.returns.value is None else None)
    if ret not in types: dynamic=True
    if not node.name.startswith('_'): out.append({'name':node.name,'signature':{'params':params,'results':[] if ret=='None' else [types.get(ret,'any')],'dynamic':dynamic}})
print(json.dumps(out,separators=(',',':')))
`

const pythonWorker = pythonPathBootstrap + `
import ast, base64, importlib, json, os, sys, tempfile, traceback
protocol=os.fdopen(3,'w',buffering=1)
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
class CaptureFailure(Exception):
    def __init__(self,trace,out,err): self.trace,self.out,self.err=trace,out,err
def capture(operation):
    sys.stdout.flush(); sys.stderr.flush()
    oldout,olderr=os.dup(1),os.dup(2)
    with tempfile.TemporaryFile() as out, tempfile.TemporaryFile() as err:
        failure=None
        try:
            os.dup2(out.fileno(),1); os.dup2(err.fileno(),2)
            value=operation()
        except BaseException:
            failure=traceback.format_exc()
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
        res={'id':rid,'ok':False,'error':failure.trace,'stdout':failure.out,'stderr':failure.err}
    except Exception:
        res={'id':rid,'ok':False,'error':traceback.format_exc(),'stdout':captured_out,'stderr':captured_err}
    protocol.write(json.dumps(res,separators=(',',':'))+'\n')
`
