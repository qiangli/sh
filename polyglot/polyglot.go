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

func canonicalLanguage(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if language == "ts" {
		return "typescript"
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
	cmd := exec.CommandContext(ctx, p.executable(), "-c", pythonAnalyze)
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

func (p Python) arguments(Plan) []string { return []string{"-u", "-c", pythonWorker} }
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
	plan    Plan
	runtime Runtime
	mu      sync.Mutex
	cmd     *exec.Cmd
	in      io.WriteCloser
	out     *bufio.Reader
	nextID  uint64
}

func Start(plan Plan, runtime Runtime) *Module { return &Module{plan: plan, runtime: runtime} }

func (m *Module) Plan() Plan { return m.plan }

func (m *Module) ensure(ctx context.Context) error {
	if m.cmd != nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, m.runtime.executable(), m.runtime.arguments(m.plan)...)
	if configured, ok := m.runtime.(configuredRuntime); ok {
		configured.configure(cmd)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		in.Close()
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s runtime unavailable: %w", m.runtime.name(), err)
		}
		return err
	}
	m.cmd, m.in, m.out = cmd, in, bufio.NewReader(out)
	load := m.runtime.loadRequest(m.plan)
	var response workerResponse
	if err := m.exchange(load, &response); err != nil {
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
	return nil
}

func (m *Module) Call(ctx context.Context, name string, args ...any) (CallResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.ensure(ctx); err != nil {
		return CallResult{}, err
	}
	m.nextID++
	request := map[string]any{"id": m.nextID, "op": "call", "name": name, "args": encodeValue(args)}
	var response workerResponse
	done := make(chan error, 1)
	go func() { done <- m.exchange(request, &response) }()
	select {
	case <-ctx.Done():
		m.kill()
		<-done
		return CallResult{}, ctx.Err()
	case err := <-done:
		if err != nil {
			m.kill()
			return CallResult{}, err
		}
	}
	if response.ID != m.nextID {
		m.kill()
		return CallResult{}, fmt.Errorf("%s worker response ID %d does not match request ID %d", m.runtime.name(), response.ID, m.nextID)
	}
	result := CallResult{Stdout: response.Stdout, Stderr: response.Stderr}
	if !response.OK {
		return result, errors.New(response.Error)
	}
	decoded, decodeErr := decodeValue(response.Result)
	if decodeErr != nil {
		return result, fmt.Errorf("decode %s result: %w", m.runtime.name(), decodeErr)
	}
	result.Value = decoded
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
	m.cmd, m.in, m.out = nil, nil, nil
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

func encodeValue(v any) any {
	switch x := v.(type) {
	case []byte:
		return map[string]any{"$bytes": base64.StdEncoding.EncodeToString(x)}
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = encodeValue(x[i])
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for k, v := range x {
			out[k] = encodeValue(v)
		}
		return out
	default:
		return v
	}
}

func decodeValue(v any) (any, error) {
	switch x := v.(type) {
	case map[string]any:
		if raw, ok := x["$bytes"].(string); ok {
			return base64.StdEncoding.DecodeString(raw)
		}
		out := map[string]any{}
		for k, v := range x {
			var err error
			out[k], err = decodeValue(v)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i := range x {
			var err error
			out[i], err = decodeValue(x[i])
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

const pythonPathBootstrap = `
import os, sys
_cwd=os.path.normcase(os.path.realpath(os.getcwd()))
_explicit_cwd=sum(os.path.normcase(os.path.realpath(os.path.abspath(p))) == _cwd for p in os.environ.get('PYTHONPATH','').split(os.pathsep) if p)
_paths=[]
for _path in sys.path:
    if not _path: continue
    if os.path.normcase(os.path.realpath(os.path.abspath(_path))) == _cwd:
        if not _explicit_cwd: continue
        _explicit_cwd-=1
    _paths.append(_path)
sys.path[:]=_paths
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
import ast, base64, contextlib, io, json, traceback
ns={'__name__':'__bashpp__'}
def dec(v):
    if isinstance(v,dict) and set(v)=={'$bytes'}: return base64.b64decode(v['$bytes'])
    if isinstance(v,list): return [dec(x) for x in v]
    if isinstance(v,dict): return {k:dec(x) for k,x in v.items()}
    return v
def enc(v):
    if isinstance(v,bytes): return {'$bytes':base64.b64encode(v).decode('ascii')}
    if isinstance(v,(list,tuple)): return [enc(x) for x in v]
    if isinstance(v,dict): return {str(k):enc(x) for k,x in v.items()}
    if v is None or isinstance(v,(bool,int,float,str)): return v
    raise TypeError('unsupported foreign result type: '+type(v).__name__)
for line in sys.stdin:
    req=json.loads(line); rid=req.get('id',0)
    try:
        if req['op']=='load':
            source='from __future__ import annotations\n'+req['source']
            exec(compile(source,'<bash++ python>','exec'),ns,ns); res={'id':rid,'ok':True}
        elif req['op']=='call':
            out,err=io.StringIO(),io.StringIO()
            with contextlib.redirect_stdout(out),contextlib.redirect_stderr(err): value=ns[req['name']](*dec(req.get('args',[])))
            res={'id':rid,'ok':True,'result':enc(value),'stdout':out.getvalue(),'stderr':err.getvalue()}
        else: raise ValueError('unknown operation')
    except Exception:
        res={'id':rid,'ok':False,'error':traceback.format_exc(),'stdout':locals().get('out',io.StringIO()).getvalue(),'stderr':locals().get('err',io.StringIO()).getvalue()}
    print(json.dumps(res,separators=(',',':')),flush=True)
`
