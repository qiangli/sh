package polyglot

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// A text fence carries a declarative artifact — a Dockerfile, an OpenTofu
// module, a manifest, a task file — instead of source. Its alias exposes the
// verbs of the processor that reads that artifact, and those verbs are
// DECLARED, never inferred: a built-in row declares them in its table, a
// runner override declares them by answering `methods`. The body is
// materialized under the cache directory keyed by its content, never into
// the checkout, and is embedded verbatim in lowered output.

// Verb is one method of a text fence row.
type Verb struct {
	// Name is the method (`plan`, `apply`, `build`).
	Name string
	// Args are the processor's arguments for this verb. `{file}` is the
	// materialized artifact, `{dir}` its directory, `{root}` the fence's
	// materialization root and `{cwd}` the caller's directory; the call's
	// own string arguments follow.
	Args []string
	// Tool, when set, is this verb's processor instead of the row's (a local
	// `podman kube play` beside a row whose tool is kubectl).
	Tool string
	// Env are extra `KEY=value` entries for the processor, with the same
	// placeholders as Args.
	Env []string
	// Effects are the effect atoms a world-changing verb carries, read by
	// the contract layer; nil for a read-only verb.
	Effects []string
	// Result is the export's result type; "" means one string (the
	// processor's stdout).
	Result string
}

// Text is the runtime of a built-in text fence row: the artifact's file
// name, the processor tool and the verb table. The tool resolves through
// [ToolResolver] like every fence tool, never from PATH when a resolver is
// installed.
type Text struct {
	Type     string
	FileName string
	Tool     string
	Verbs    []Verb
	// WorkDir is where the processor runs: "" for the artifact's directory
	// (a self-contained module), "{cwd}" for the caller's directory (a task
	// file whose bodies address the checkout, a chart path beside a values
	// file).
	WorkDir string
	Dir     string
	// CwdFunc, when set, answers the caller's directory at call time and
	// wins over Dir; it is a host binding, never part of a lowered literal.
	CwdFunc func() string
	Environ []string
	// EnvFunc, when set, answers the caller's environment at call time and
	// wins over Environ; a host binding like CwdFunc.
	EnvFunc func() []string
}

func (t Text) environ() []string {
	if t.EnvFunc != nil {
		if env := t.EnvFunc(); env != nil {
			return env
		}
	}
	return t.Environ
}

// RunnerFence is the runtime of a fence whose opener named a runner
// (`~~~<type> as <alias> !<runner>`). The host supplies Invoke — the
// interpreter dispatches to a Bash++ function or a registered command, a
// lowered program calls the lowered function — with argv
// `<verb> <file> [args…]`; the runner's stdout is the result. Methods come
// from `runner methods <file>`: one export JSON object per line, `name`
// with an optional `signature` and `effects` (or a comma-joined `effect`).
type RunnerFence struct {
	Type     string
	FileName string
	Runner   string
	Invoke   func(ctx context.Context, argv []string) (string, error)
}

// MethodsVerb is the reserved verb a runner answers with its method list.
const MethodsVerb = "methods"

// TextFileName is the artifact file name a type materializes as when its row
// does not say: the type is the extension.
func TextFileName(typ string) string { return "fence." + typ }

// TextSignature is the default signature of a declared method: string
// arguments, one string result.
func TextSignature() Signature {
	return Signature{Params: []string{"string"}, Variadic: true, Results: []string{"string"}}
}

func (t Text) name() string                    { return "text/" + t.Type }
func (t Text) executable() string              { return "" }
func (t Text) arguments(Plan) []string         { return nil }
func (t Text) loadRequest(Plan) map[string]any { return nil }

// Analyze answers the row's verb table; the body is not read, the
// processor reads it when a verb runs.
func (t Text) Analyze(ctx context.Context, source string) ([]Export, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(t.Verbs) == 0 {
		return nil, fmt.Errorf("text fence %s declares no methods", t.Type)
	}
	exports := make([]Export, 0, len(t.Verbs))
	for _, verb := range t.Verbs {
		if verb.Name == "" {
			return nil, fmt.Errorf("text fence %s: a verb without a name", t.Type)
		}
		sig := TextSignature()
		if verb.Result != "" {
			sig.Results = []string{verb.Result}
		}
		exports = append(exports, Export{Name: verb.Name, Signature: sig, Effects: verb.Effects})
	}
	return exports, nil
}

func (t Text) verb(name string) (Verb, bool) {
	for _, verb := range t.Verbs {
		if verb.Name == name {
			return verb, true
		}
	}
	return Verb{}, false
}

// LoweredLiteral is the Go expression a lowered program uses to construct
// this runtime; prefix is the emitter's import prefix.
func (t Text) LoweredLiteral(prefix string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%spolyglot.Text{Type:%q,FileName:%q,Tool:%q,WorkDir:%q,Verbs:[]%spolyglot.Verb{", prefix, t.Type, t.FileName, t.Tool, t.WorkDir, prefix)
	for _, verb := range t.Verbs {
		fmt.Fprintf(&out, "{Name:%q,Args:%#v,Tool:%q,Env:%#v,Effects:%#v,Result:%q},", verb.Name, verb.Args, verb.Tool, verb.Env, verb.Effects, verb.Result)
	}
	out.WriteString("}}")
	return out.String()
}

func (r RunnerFence) name() string                    { return "runner/" + r.Runner }
func (r RunnerFence) executable() string              { return "" }
func (r RunnerFence) arguments(Plan) []string         { return nil }
func (r RunnerFence) loadRequest(Plan) map[string]any { return nil }

// Analyze asks the runner for its methods. An empty or malformed answer is a
// refusal — there is no fallback to dynamic dispatch.
func (r RunnerFence) Analyze(ctx context.Context, source string) ([]Export, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.Invoke == nil {
		return nil, fmt.Errorf("runner fence %s: no host to run %s", r.Type, r.Runner)
	}
	file, err := materializeText(textKey(r.Type, r.Runner, source), r.fileName(), source)
	if err != nil {
		return nil, err
	}
	answer, err := r.Invoke(ctx, []string{MethodsVerb, file})
	if err != nil {
		return nil, fmt.Errorf("runner %s: %s: %w", r.Runner, MethodsVerb, err)
	}
	return ParseMethods(r.Runner, answer)
}

func (r RunnerFence) fileName() string {
	if r.FileName != "" {
		return r.FileName
	}
	return TextFileName(r.Type)
}

// ParseMethods reads a runner's `methods` answer: one export per non-blank
// line, as JSON. Every method needs a name; a missing signature is
// [TextSignature].
func ParseMethods(runner, answer string) ([]Export, error) {
	var exports []Export
	seen := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(answer))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var raw struct {
			Name      string     `json:"name"`
			Signature *Signature `json:"signature"`
			Effect    string     `json:"effect"`
			Effects   []string   `json:"effects"`
		}
		if err := json.Unmarshal([]byte(text), &raw); err != nil {
			return nil, fmt.Errorf("runner %s: %s line %d is not a method object: %v", runner, MethodsVerb, line, err)
		}
		if raw.Name == "" {
			return nil, fmt.Errorf("runner %s: %s line %d declares a method without a name", runner, MethodsVerb, line)
		}
		if raw.Name == MethodsVerb {
			return nil, fmt.Errorf("runner %s: %q is the reserved method verb", runner, MethodsVerb)
		}
		if seen[raw.Name] {
			return nil, fmt.Errorf("runner %s: method %s declared twice", runner, raw.Name)
		}
		seen[raw.Name] = true
		sig := TextSignature()
		if raw.Signature != nil {
			sig = *raw.Signature
		}
		effects := raw.Effects
		if raw.Effect != "" {
			effects = append(effects, strings.Split(raw.Effect, ",")...)
		}
		for i, effect := range effects {
			effects[i] = strings.TrimSpace(effect)
		}
		exports = append(exports, Export{Name: raw.Name, Signature: sig, Effects: effects})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("runner %s: %s: %v", runner, MethodsVerb, err)
	}
	if len(exports) == 0 {
		return nil, fmt.Errorf("runner %s declared no methods", runner)
	}
	return exports, nil
}

func textKey(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// textRoot is where artifacts materialize: the user cache, or the temp
// directory where no user cache exists — never the checkout.
func textRoot() string {
	if dir, err := os.UserCacheDir(); err == nil {
		return filepath.Join(dir, "bashpp", "text")
	}
	return filepath.Join(os.TempDir(), "bashpp-text")
}

// materializeText writes the body under the cache keyed by its content and
// returns the file path. A body already materialized with the same bytes is
// reused; a changed body is rewritten, never appended to. The file name is
// the row's (a row may nest it: `skill/SKILL.md`), never the script's, and
// stays inside the fence's root.
func materializeText(key, fileName, source string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(fileName))
	if fileName == "" || clean == "." || strings.HasPrefix(clean, "../") || clean == ".." || strings.HasPrefix(clean, "/") || filepath.IsAbs(fileName) {
		return "", fmt.Errorf("text fence: invalid artifact file name %q", fileName)
	}
	dir := filepath.Join(textRoot(), key, filepath.Dir(filepath.FromSlash(clean)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	file := filepath.Join(textRoot(), key, filepath.FromSlash(clean))
	if existing, err := os.ReadFile(file); err == nil && string(existing) == source {
		return file, nil
	}
	if err := os.WriteFile(file, []byte(source), 0o644); err != nil {
		return "", err
	}
	return file, nil
}

// callText runs one verb of a built-in text row: materialize the body,
// resolve the tool, exec `tool <verb args with {file}/{dir}> <call args>`
// in the artifact's directory. Stdout is the result; a non-zero status is
// the call error, with stderr passed through.
func (m *Module) callText(ctx context.Context, text Text, name string, args []any, kwargs map[string]any) (CallResult, error) {
	if len(kwargs) != 0 {
		return CallResult{}, fmt.Errorf("text fence %s: %s does not accept named arguments", text.Type, name)
	}
	verb, ok := text.verb(name)
	if !ok {
		return CallResult{}, fmt.Errorf("text fence %s has no method %s", text.Type, name)
	}
	fileName := text.FileName
	if fileName == "" {
		fileName = TextFileName(text.Type)
	}
	file, err := materializeText(m.plan.ID, fileName, m.plan.Source)
	if err != nil {
		return CallResult{}, err
	}
	dir := filepath.Dir(file)
	root := filepath.Join(textRoot(), m.plan.ID)
	cwd := text.Dir
	if text.CwdFunc != nil {
		if dir := text.CwdFunc(); dir != "" {
			cwd = dir
		}
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	expand := func(arg string) string {
		arg = strings.ReplaceAll(arg, "{file}", file)
		arg = strings.ReplaceAll(arg, "{dir}", dir)
		arg = strings.ReplaceAll(arg, "{root}", root)
		return strings.ReplaceAll(arg, "{cwd}", cwd)
	}
	toolName := text.Tool
	if verb.Tool != "" {
		toolName = verb.Tool
	}
	environ := text.environ()
	env := envMap(environ)
	tool, _, err := resolveTool(env, toolName)
	if err != nil {
		return CallResult{}, fmt.Errorf("text fence %s: %s: %w", text.Type, toolName, err)
	}
	argv := append([]string(nil), tool...)
	for _, arg := range verb.Args {
		argv = append(argv, expand(arg))
	}
	for _, arg := range args {
		argv = append(argv, foreignText(arg))
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	if text.WorkDir != "" {
		cmd.Dir = expand(text.WorkDir)
	}
	cmd.Env = append(environOf(environ), "BASHPP_FENCE_TYPE="+text.Type, "BASHPP_FENCE_FILE="+file)
	for _, entry := range verb.Env {
		cmd.Env = append(cmd.Env, expand(entry))
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	// The processor's stdout is the value with its trailing newlines
	// removed, as a command substitution reads a command.
	result := CallResult{Value: strings.TrimRight(stdout.String(), "\n"), Stderr: stderr.String()}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return result, fmt.Errorf("text fence %s.%s: %s exited %d", text.Type, name, toolName, exit.ExitCode())
		}
		return result, fmt.Errorf("text fence %s.%s: %w", text.Type, name, err)
	}
	return result, nil
}

// callRunner runs one declared method through the fence's runner.
func (m *Module) callRunner(ctx context.Context, fence RunnerFence, name string, args []any, kwargs map[string]any) (CallResult, error) {
	if len(kwargs) != 0 {
		return CallResult{}, fmt.Errorf("runner fence %s: %s does not accept named arguments", fence.Type, name)
	}
	if fence.Invoke == nil {
		return CallResult{}, fmt.Errorf("runner fence %s: no host to run %s", fence.Type, fence.Runner)
	}
	declared := false
	for _, export := range m.plan.Exports {
		if export.Name == name {
			declared = true
		}
	}
	if !declared {
		return CallResult{}, fmt.Errorf("runner %s declares no method %s", fence.Runner, name)
	}
	file, err := materializeText(m.plan.ID, fence.fileName(), m.plan.Source)
	if err != nil {
		return CallResult{}, err
	}
	argv := []string{name, file}
	for _, arg := range args {
		argv = append(argv, foreignText(arg))
	}
	out, err := fence.Invoke(ctx, argv)
	if err != nil {
		return CallResult{Value: out}, fmt.Errorf("runner %s: %s: %w", fence.Runner, name, err)
	}
	return CallResult{Value: out}, nil
}

func foreignText(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case nil:
		return ""
	case []byte:
		return string(v)
	}
	return fmt.Sprint(value)
}

func environOf(environ []string) []string {
	if len(environ) == 0 {
		return os.Environ()
	}
	return append([]string(nil), environ...)
}
