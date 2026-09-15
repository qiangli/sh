package polyglot

// This file deliberately contains no subprocess calls. Environment selection is
// part of planning: callers may safely use it while parsing or type checking.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// EnvironmentRequest describes the source being planned. Source must be a file
// name (it need not exist). Name selects a named bashpp environment. Environ is
// an optional os.Environ-style snapshot; an empty slice means os.Environ().
// Supplying a snapshot makes planning independent of ambient process state.
type EnvironmentRequest struct {
	Source   string
	Language string
	Name     string
	Environ  []string
}

// EnvironmentPlan is the immutable, language-neutral resolution record. Its
// fields are values, rather than handles to discovery state, so it can be
// fingerprinted, cached, and handed from checking to execution unchanged.
// PythonPath and Env are copies owned by the plan and must be treated read-only.
type EnvironmentPlan struct {
	Language    string
	Name        string
	Root        string // project or VCS boundary
	Dir         string // fixed child-process working directory
	Executable  string // absolute selected runtime executable
	PythonPath  []string
	Env         []string // normalized launch environment, sorted by key
	Explanation []string // deterministic and redacted; no ambient values
	Fingerprint string
}

// Clone returns an independently owned copy of p.
func (p EnvironmentPlan) Clone() EnvironmentPlan {
	p.PythonPath = append([]string(nil), p.PythonPath...)
	p.Env = append([]string(nil), p.Env...)
	p.Explanation = append([]string(nil), p.Explanation...)
	return p
}

// DiscoverEnvironment performs source-relative, read-only environment
// discovery. It never executes a runtime, imports a module, invokes a package
// manager, or writes a file.
func DiscoverEnvironment(request EnvironmentRequest) (EnvironmentPlan, error) {
	lang := strings.ToLower(strings.TrimSpace(request.Language))
	if lang == "" {
		lang = "python"
	}
	if lang != "python" {
		return EnvironmentPlan{}, fmt.Errorf("polyglot: no environment metadata reader for %q", lang)
	}
	if request.Source == "" {
		return EnvironmentPlan{}, fmt.Errorf("polyglot: environment source is required")
	}
	source, err := filepath.Abs(request.Source)
	if err != nil {
		return EnvironmentPlan{}, err
	}
	dir := filepath.Dir(source)
	env := envMap(request.Environ)
	root, dirs, err := environmentDirs(dir)
	if err != nil {
		return EnvironmentPlan{}, err
	}

	// A pair of formats at one level is intentionally ambiguous. Silently
	// preferring YAML made adding a JSON override change a build unexpectedly.
	var overlays []environmentOverlay
	for _, d := range dirs {
		yaml, yerr := readOverlay(filepath.Join(d, "bashpp.yaml"))
		jsonOverlay, jerr := readOverlay(filepath.Join(d, "bashpp.json"))
		if yerr != nil {
			return EnvironmentPlan{}, yerr
		}
		if jerr != nil {
			return EnvironmentPlan{}, jerr
		}
		if yaml != nil && jsonOverlay != nil {
			return EnvironmentPlan{}, fmt.Errorf("polyglot: ambiguous environment overlays in %s", d)
		}
		if yaml != nil {
			overlays = append(overlays, yaml...)
		}
		if jsonOverlay != nil {
			overlays = append(overlays, jsonOverlay...)
		}
	}
	matching := make([]environmentOverlay, 0, len(overlays))
	for _, o := range overlays {
		if o.Language == "" || o.Language == lang {
			matching = append(matching, o)
		}
	}
	var selected *environmentOverlay
	if request.Name != "" {
		for i := range matching {
			if matching[i].Name == request.Name {
				if selected != nil {
					return EnvironmentPlan{}, fmt.Errorf("polyglot: ambiguous environment %q", request.Name)
				}
				selected = &matching[i]
			}
		}
		if selected == nil {
			return EnvironmentPlan{}, fmt.Errorf("polyglot: environment %q not found", request.Name)
		}
	} else if len(matching) == 1 {
		selected = &matching[0]
	} else if len(matching) > 1 {
		return EnvironmentPlan{}, fmt.Errorf("polyglot: ambiguous matching environment overlays")
	}

	plan := EnvironmentPlan{Language: lang, Name: request.Name, Root: root, Dir: root}
	var executable string
	if selected != nil {
		plan.Explanation = append(plan.Explanation, "selected bashpp overlay")
		if selected.Name != "" {
			plan.Name = selected.Name
		}
		executable, err = resolveRuntime(root, selected.Runtime)
		if err != nil {
			return EnvironmentPlan{}, err
		}
	} else if projectRuntime, why := projectPythonRuntime(root, env); projectRuntime != "" {
		plan.Explanation = append(plan.Explanation, why)
		executable = projectRuntime
	} else if active := activePythonRuntime(env); active != "" {
		plan.Explanation = append(plan.Explanation, "selected inherited active environment")
		executable = active
	} else {
		plan.Explanation = append(plan.Explanation, "selected PATH runtime")
		executable, err = lookupPath(env, "python3")
		if err != nil {
			return EnvironmentPlan{}, fmt.Errorf("polyglot: Python runtime unavailable: %w", err)
		}
	}
	// The override changes only the executable, never which environment won.
	if override := env["BASHPP_PYTHON"]; override != "" {
		executable, err = resolveRuntime(root, override)
		if err != nil {
			return EnvironmentPlan{}, err
		}
		plan.Explanation = append(plan.Explanation, "runtime executable overridden")
	}
	plan.Executable, err = canonicalFile(executable)
	if err != nil {
		return EnvironmentPlan{}, err
	}
	plan.PythonPath, err = canonicalPythonPath(env["PYTHONPATH"], root)
	if err != nil {
		return EnvironmentPlan{}, err
	}
	plan.Env = launchEnvironment(env, plan.PythonPath)
	plan.Fingerprint, err = environmentFingerprint(plan)
	if err != nil {
		return EnvironmentPlan{}, err
	}
	return plan.Clone(), nil
}

type environmentOverlay struct{ Name, Language, Runtime string }

func environmentDirs(start string) (string, []string, error) {
	start, err := filepath.EvalSymlinks(start)
	if err != nil {
		return "", nil, err
	}
	var dirs []string
	project := ""
	for d := start; ; d = filepath.Dir(d) {
		dirs = append(dirs, d)
		if recognizedPythonProject(d) {
			if project == "" {
				project = d
			}
			break
		}
		if exists(filepath.Join(d, ".git")) {
			if project == "" {
				project = d
			}
			break
		}
		if parent := filepath.Dir(d); parent == d {
			if project == "" {
				project = start
			}
			break
		}
	}
	// closest overlays are considered first, and discovery cannot escape root.
	return project, dirs, nil
}

func recognizedPythonProject(dir string) bool {
	for _, n := range pythonProjectMetadata {
		if exists(filepath.Join(dir, n)) {
			return true
		}
	}
	return false
}
func exists(name string) bool { _, err := os.Lstat(name); return err == nil }

func projectPythonRuntime(root string, env map[string]string) (string, string) {
	for _, n := range []string{".venv", "venv", "env", ".env"} {
		d := filepath.Join(root, n)
		if exists(filepath.Join(d, "pyvenv.cfg")) {
			for _, exe := range []string{filepath.Join(d, "bin", "python3"), filepath.Join(d, "bin", "python"), filepath.Join(d, "Scripts", "python.exe")} {
				if exists(exe) {
					return exe, "selected nearest Python project runtime"
				}
			}
		}
	}
	for _, name := range []string{".python-version", "runtime.txt"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			continue
		}
		version := strings.Fields(string(data))
		if len(version) == 0 {
			continue
		}
		version[0] = strings.TrimPrefix(version[0], "python-")
		version[0] = strings.TrimPrefix(version[0], "python")
		if executable, err := lookupPath(env, "python"+version[0]); err == nil {
			return executable, "selected nearest Python runtime metadata"
		}
	}
	return "", ""
}
func activePythonRuntime(env map[string]string) string {
	for _, key := range []string{"VIRTUAL_ENV", "CONDA_PREFIX"} {
		if d := env[key]; d != "" {
			for _, n := range []string{"bin/python3", "bin/python", "Scripts/python.exe"} {
				if exists(filepath.Join(d, n)) {
					return filepath.Join(d, n)
				}
			}
		}
	}
	return ""
}
func resolveRuntime(root, value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("polyglot: empty Python runtime")
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(root, value)
	}
	return canonicalFile(value)
}
func canonicalFile(name string) (string, error) {
	name, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	name, err = filepath.EvalSymlinks(name)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(name)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("polyglot: runtime %s is a directory", name)
	}
	return name, nil
}

func envMap(entries []string) map[string]string {
	if len(entries) == 0 {
		entries = os.Environ()
	}
	out := map[string]string{}
	for _, e := range entries {
		if k, v, ok := strings.Cut(e, "="); ok {
			out[k] = v
		}
	}
	return out
}
func lookupPath(env map[string]string, file string) (string, error) {
	for _, d := range filepath.SplitList(env["PATH"]) {
		if d == "" {
			continue
		}
		n := filepath.Join(d, file)
		if exists(n) {
			return n, nil
		}
	}
	return "", os.ErrNotExist
}
func canonicalPythonPath(value, root string) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	var out []string
	for _, p := range filepath.SplitList(value) {
		if p == "" {
			return nil, fmt.Errorf("polyglot: empty PYTHONPATH entry is not allowed")
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		p, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		p, err = filepath.EvalSymlinks(p)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}
func launchEnvironment(env map[string]string, paths []string) []string {
	keep := map[string]string{"PYTHONNOUSERSITE": "1", "PYTHONSAFEPATH": "1"}
	if len(paths) > 0 {
		keep["PYTHONPATH"] = strings.Join(paths, string(os.PathListSeparator))
	}
	for _, k := range []string{"PATH", "VIRTUAL_ENV", "CONDA_PREFIX"} {
		if env[k] != "" {
			keep[k] = env[k]
		}
	}
	keys := make([]string, 0, len(keep))
	for k := range keep {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+keep[k])
	}
	return out
}

func environmentFingerprint(p EnvironmentPlan) (string, error) {
	h := sha256.New()
	write := func(s string) { _, _ = io.WriteString(h, s+"\x00") }
	write(p.Language)
	write(p.Root)
	write(p.Dir)
	write(runtime.GOOS)
	write(runtime.GOARCH)
	for _, s := range p.PythonPath {
		write(s)
	}
	for _, s := range p.Env {
		write(s)
	}
	if err := fingerprintFile(h, p.Executable); err != nil {
		return "", err
	}
	// Project and overlay contents are resolution inputs too. Recording their
	// bytes makes a cached plan expire when a lock file or declared runtime is
	// edited, without exposing their contents in Explanation.
	for _, n := range append(pythonProjectMetadata, "bashpp.yaml", "bashpp.json") {
		if file := filepath.Join(p.Root, n); exists(file) {
			if err := fingerprintFile(h, file); err != nil {
				return "", err
			}
		}
	}
	for d := filepath.Dir(p.Executable); ; d = filepath.Dir(d) {
		cfg := filepath.Join(d, "pyvenv.cfg")
		if exists(cfg) {
			if err := fingerprintFile(h, cfg); err != nil {
				return "", err
			}
			break
		}
		if parent := filepath.Dir(d); parent == d {
			break
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func fingerprintFile(h io.Writer, name string) error {
	info, err := os.Stat(name)
	if err != nil {
		return err
	}
	_, _ = io.WriteString(h, name+"\x00"+fmt.Sprintf("%d:%d:%d", info.Size(), info.ModTime().UnixNano(), info.Mode())+"\x00")
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(h, f)
	return err
}

var pythonProjectMetadata = []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt", "requirements-dev.txt", "Pipfile", "poetry.lock", "uv.lock", "pdm.lock", "Pipfile.lock", ".python-version", "runtime.txt", ".tool-versions"}

func readOverlay(name string) ([]environmentOverlay, error) {
	data, err := os.ReadFile(name)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(name, ".json") {
		return parseOverlayJSON(data, name)
	}
	return parseOverlayYAML(data, name)
}
func parseOverlayJSON(data []byte, file string) ([]environmentOverlay, error) {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("polyglot: parse %s: %w", file, err)
	}
	return overlayObjects(raw, file)
}

// parseOverlayYAML accepts the small mapping subset needed for declarative
// environment files. JSON remains available for nested or generated config.
func parseOverlayYAML(data []byte, file string) ([]environmentOverlay, error) {
	lines := strings.Split(string(data), "\n")
	// Named mappings are the usual convenient YAML spelling:
	// environments:\n  dev:\n    language: python\n    runtime: .venv/bin/python
	var named []environmentOverlay
	inEnvironments, currentIndent := false, -1
	var current *environmentOverlay
	for _, raw := range lines {
		withoutComment := strings.SplitN(raw, "#", 2)[0]
		indent := len(withoutComment) - len(strings.TrimLeft(withoutComment, " \t"))
		line := strings.TrimSpace(withoutComment)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("polyglot: parse %s: expected key: value", file)
		}
		key, value = strings.TrimSpace(key), strings.Trim(strings.TrimSpace(value), "\"'")
		if key == "environments" && value == "" {
			inEnvironments = true
			current = nil
			continue
		}
		if !inEnvironments {
			continue
		}
		if value == "" && (current == nil || indent <= currentIndent) {
			named = append(named, environmentOverlay{Name: key})
			current = &named[len(named)-1]
			currentIndent = indent
			continue
		}
		if current != nil && indent > currentIndent {
			switch key {
			case "language":
				current.Language = value
			case "runtime", "python", "environment":
				current.Runtime = value
			case "name":
				current.Name = value
			}
			continue
		}
	}
	if len(named) > 0 {
		for _, o := range named {
			if o.Runtime == "" {
				return nil, fmt.Errorf("polyglot: environment %q in %s has no runtime", o.Name, file)
			}
		}
		return named, nil
	}
	values := map[string]string{}
	section := ""
	for _, line := range lines {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("polyglot: parse %s: expected key: value", file)
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), "\"'")
		if v == "" {
			section = k
			continue
		}
		if section != "" && k != "environment" && k != "runtime" && k != "language" && k != "name" {
			section = ""
		}
		values[k] = v
	}
	if len(values) == 0 {
		return nil, nil
	}
	return []environmentOverlay{{Name: values["name"], Language: values["language"], Runtime: first(values["runtime"], values["python"], values["environment"])}}, nil
}
func first(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
func overlayObjects(raw any, file string) ([]environmentOverlay, error) {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("polyglot: %s must be an object", file)
	}
	var objects []any
	if v, ok := m["environments"]; ok {
		switch x := v.(type) {
		case []any:
			objects = x
		case map[string]any:
			for name, val := range x {
				o, ok := val.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("polyglot: invalid environment in %s", file)
				}
				o["name"] = name
				objects = append(objects, o)
			}
		default:
			return nil, fmt.Errorf("polyglot: invalid environments in %s", file)
		}
	} else {
		objects = []any{m}
	}
	var out []environmentOverlay
	for _, raw := range objects {
		o, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("polyglot: invalid environment in %s", file)
		}
		str := func(k string) string { v, _ := o[k].(string); return v }
		runtime := first(str("runtime"), str("python"), str("environment"))
		if runtime == "" {
			return nil, fmt.Errorf("polyglot: environment in %s has no runtime", file)
		}
		out = append(out, environmentOverlay{Name: str("name"), Language: str("language"), Runtime: runtime})
	}
	return out, nil
}
