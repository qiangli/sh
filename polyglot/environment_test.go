package polyglot

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPythonEnvironmentLaunchIsFixedAndSanitized(t *testing.T) {
	plan := EnvironmentPlan{Dir: t.TempDir(), Executable: "/runtime", Env: []string{"PATH=/safe", "PYTHONNOUSERSITE=1", "PYTHONSAFEPATH=1"}}
	cmd := exec.Command("ignored")
	(Python{Environment: &plan}).configure(cmd)
	if cmd.Dir != plan.Dir || strings.Join(cmd.Env, "\n") != strings.Join(plan.Env, "\n") {
		t.Fatalf("cmd = dir %q env %q", cmd.Dir, cmd.Env)
	}
	if strings.Contains(strings.Join(cmd.Env, "\n"), "PYTHONHOME=") || !strings.Contains(strings.Join((Python{}).arguments(Plan{}), " "), "-P") {
		t.Fatalf("unsafe Python launch: env=%q args=%q", cmd.Env, (Python{}).arguments(Plan{}))
	}
}

func writeEnvironmentFile(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func pythonFixture(t *testing.T, root string) string {
	t.Helper()
	python := filepath.Join(root, ".venv", "bin", "python3")
	writeEnvironmentFile(t, python, "fixture runtime")
	writeEnvironmentFile(t, filepath.Join(root, ".venv", "pyvenv.cfg"), "home = /redacted\n")
	writeEnvironmentFile(t, filepath.Join(root, "pyproject.toml"), "[project]\nname = 'fixture'\n")
	return python
}

func TestDiscoverEnvironmentProjectAndFingerprint(t *testing.T) {
	root := t.TempDir()
	python := pythonFixture(t, root)
	source := filepath.Join(root, "pkg", "source.bpp")
	writeEnvironmentFile(t, source, "")
	path1, path2 := filepath.Join(root, "a"), filepath.Join(root, "b")
	if err := os.MkdirAll(path1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path2, 0o755); err != nil {
		t.Fatal(err)
	}
	plan, err := DiscoverEnvironment(EnvironmentRequest{Source: source, Environ: []string{"PYTHONPATH=" + path1 + string(os.PathListSeparator) + path2}})
	if err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	canonicalPython, err := filepath.EvalSymlinks(python)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Executable != canonicalPython || plan.Root != canonicalRoot || len(plan.PythonPath) != 2 || !strings.Contains(strings.Join(plan.Explanation, " "), "nearest Python project") {
		t.Fatalf("plan = %#v", plan)
	}
	first := plan.Fingerprint
	writeEnvironmentFile(t, filepath.Join(root, ".venv", "pyvenv.cfg"), "home = changed\n")
	changed, err := DiscoverEnvironment(EnvironmentRequest{Source: source})
	if err != nil {
		t.Fatal(err)
	}
	if first == changed.Fingerprint {
		t.Fatal("pyvenv.cfg did not invalidate fingerprint")
	}
	changedPath, err := DiscoverEnvironment(EnvironmentRequest{Source: source, Environ: []string{"PYTHONPATH=" + path2 + string(os.PathListSeparator) + path1}})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Fingerprint == changedPath.Fingerprint {
		t.Fatal("PYTHONPATH order did not invalidate fingerprint")
	}
}

func TestDiscoverEnvironmentOverlaySelectionAndAmbiguity(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "x.bpp")
	writeEnvironmentFile(t, source, "")
	runtime := filepath.Join(root, "runtime")
	writeEnvironmentFile(t, runtime, "runtime")
	writeEnvironmentFile(t, filepath.Join(root, "bashpp.json"), `{"environments":{"one":{"language":"python","runtime":"runtime"}}}`)
	plan, err := DiscoverEnvironment(EnvironmentRequest{Source: source, Name: "one"})
	canonicalRuntime, canonicalErr := filepath.EvalSymlinks(runtime)
	if err != nil || canonicalErr != nil || plan.Executable != canonicalRuntime {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
	if _, err := DiscoverEnvironment(EnvironmentRequest{Source: source, Name: "zero"}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("zero candidate error = %v", err)
	}
	writeEnvironmentFile(t, filepath.Join(root, "bashpp.yaml"), "runtime: runtime\n")
	if _, err := DiscoverEnvironment(EnvironmentRequest{Source: source}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("format ambiguity error = %v", err)
	}
}

func TestDiscoverEnvironmentRejectsMultipleMatchingOverlays(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "x.bpp")
	writeEnvironmentFile(t, source, "")
	writeEnvironmentFile(t, filepath.Join(root, "one"), "runtime")
	writeEnvironmentFile(t, filepath.Join(root, "two"), "runtime")
	writeEnvironmentFile(t, filepath.Join(root, "bashpp.json"), `{"environments":{"one":{"language":"python","runtime":"one"},"two":{"language":"python","runtime":"two"}}}`)
	if _, err := DiscoverEnvironment(EnvironmentRequest{Source: source}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("multiple candidate error = %v", err)
	}
}

func TestDiscoverEnvironmentOverrideOnlyChangesRuntime(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "x.bpp")
	writeEnvironmentFile(t, source, "")
	base, override := filepath.Join(root, "base"), filepath.Join(root, "override")
	writeEnvironmentFile(t, base, "base")
	writeEnvironmentFile(t, override, "override")
	writeEnvironmentFile(t, filepath.Join(root, "bashpp.json"), `{"name":"chosen","language":"python","runtime":"base"}`)
	plan, err := DiscoverEnvironment(EnvironmentRequest{Source: source, Environ: []string{"BASHPP_PYTHON=" + override}})
	if err != nil {
		t.Fatal(err)
	}
	canonicalOverride, canonicalErr := filepath.EvalSymlinks(override)
	if canonicalErr != nil || plan.Name != "chosen" || plan.Executable != canonicalOverride || !strings.Contains(strings.Join(plan.Explanation, " "), "overridden") {
		t.Fatalf("plan=%#v", plan)
	}
}
