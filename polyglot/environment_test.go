package polyglot

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPythonEnvironmentLaunchIsFixedAndSanitized(t *testing.T) {
	env := launchEnvironment(map[string]string{
		"PATH":           "/safe",
		"PYTHONHOME":     "/unsafe",
		"PYTHONSAFEPATH": "1",
	}, nil)
	plan := EnvironmentPlan{Dir: t.TempDir(), Executable: "/runtime", Env: env}
	cmd := exec.Command("ignored")
	(Python{Environment: &plan}).configure(cmd)
	if cmd.Dir != plan.Dir || strings.Join(cmd.Env, "\n") != strings.Join(plan.Env, "\n") {
		t.Fatalf("cmd = dir %q env %q", cmd.Dir, cmd.Env)
	}
	if strings.Contains(strings.Join(cmd.Env, "\n"), "PYTHONHOME=") || strings.Contains(strings.Join(cmd.Env, "\n"), "PYTHONSAFEPATH=") || strings.Contains(strings.Join((Python{}).arguments(Plan{}), " "), "-P") {
		t.Fatalf("unsafe Python launch: env=%q args=%q", cmd.Env, (Python{}).arguments(Plan{}))
	}
}

func TestDiscoverEnvironmentManagerMetadata(t *testing.T) {
	root := t.TempDir()
	pythonFixture(t, root)
	writeEnvironmentFile(t, filepath.Join(root, "uv.lock"), "version = 1\n")
	writeEnvironmentFile(t, filepath.Join(root, ".python-version"), "3.10\n")
	plan, err := DiscoverEnvironment(EnvironmentRequest{Source: filepath.Join(root, "missing", "source.bpp")})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Manager != "uv" || plan.RuntimeConstraint != "3.10" || len(plan.Manifests) != 1 || filepath.Base(plan.Manifests[0]) != "pyproject.toml" || len(plan.Locks) != 1 || filepath.Base(plan.Locks[0]) != "uv.lock" {
		t.Fatalf("metadata plan = %#v", plan)
	}
	clone := plan.Clone()
	clone.Manifests[0] = "changed"
	clone.Locks[0] = "changed"
	if filepath.Base(plan.Manifests[0]) != "pyproject.toml" || filepath.Base(plan.Locks[0]) != "uv.lock" {
		t.Fatal("Clone shared metadata slices")
	}
}

func TestDiscoverEnvironmentRejectsConflictingManagerLocks(t *testing.T) {
	root := t.TempDir()
	pythonFixture(t, root)
	writeEnvironmentFile(t, filepath.Join(root, "uv.lock"), "version = 1\n")
	writeEnvironmentFile(t, filepath.Join(root, "poetry.lock"), "package = []\n")
	_, err := DiscoverEnvironment(EnvironmentRequest{Source: filepath.Join(root, "source.bpp")})
	if err == nil || !strings.Contains(err.Error(), "conflicting Python manager lockfiles") {
		t.Fatalf("conflicting lock error = %v", err)
	}
}

func TestDiscoverEnvironmentPATHRequiresExecutableRegularFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses executable extensions rather than mode bits")
	}
	root := t.TempDir()
	bad, good := filepath.Join(root, "bad"), filepath.Join(root, "good")
	writeEnvironmentFile(t, filepath.Join(bad, "python3"), "not executable")
	if err := os.Chmod(filepath.Join(bad, "python3"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeEnvironmentFile(t, filepath.Join(good, "python3"), "executable")
	plan, err := DiscoverEnvironment(EnvironmentRequest{
		Source:  filepath.Join(root, "source.bpp"),
		Environ: []string{"PATH=" + bad + string(os.PathListSeparator) + good},
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(good, "python3"))
	if err != nil || plan.Executable != want {
		t.Fatalf("executable = %q, want %q (err %v)", plan.Executable, want, err)
	}
	directoryCandidate := filepath.Join(root, "directory", "python3")
	if err := os.MkdirAll(directoryCandidate, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := lookupPath(map[string]string{"PATH": filepath.Dir(directoryCandidate)}, "python3"); err == nil {
		t.Fatal("directory accepted as PATH runtime")
	}
}

func TestRuntimePathsRequireExecutableRegularFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses executable extensions rather than mode bits")
	}
	root := t.TempDir()
	runtimePath := filepath.Join(root, "python")
	writeEnvironmentFile(t, runtimePath, "not executable")
	if err := os.Chmod(runtimePath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveRuntime(root, "python", nil); err == nil || !strings.Contains(err.Error(), "not an executable file") {
		t.Fatalf("direct runtime error = %v", err)
	}

	writeEnvironmentFile(t, filepath.Join(root, "source.bpp"), "")
	writeEnvironmentFile(t, filepath.Join(root, "bashpp.yaml"), "runtime: python\n")
	if _, err := DiscoverEnvironment(EnvironmentRequest{Source: filepath.Join(root, "source.bpp")}); err == nil || !strings.Contains(err.Error(), "not an executable file") {
		t.Fatalf("overlay runtime error = %v", err)
	}

	venvRoot := t.TempDir()
	venvPython := pythonFixture(t, venvRoot)
	if err := os.Chmod(venvPython, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverEnvironment(EnvironmentRequest{Source: filepath.Join(venvRoot, "source.bpp")}); err == nil || !strings.Contains(err.Error(), "not an executable file") {
		t.Fatalf("project runtime error = %v", err)
	}

	overrideRoot := t.TempDir()
	pythonFixture(t, overrideRoot)
	override := filepath.Join(overrideRoot, "override")
	writeEnvironmentFile(t, override, "not executable")
	if err := os.Chmod(override, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DiscoverEnvironment(EnvironmentRequest{Source: filepath.Join(overrideRoot, "source.bpp"), Environ: []string{"BASHPP_PYTHON=" + override}}); err == nil || !strings.Contains(err.Error(), "not an executable file") {
		t.Fatalf("override runtime error = %v", err)
	}
}

func TestExecutableNamesWindowsPATHEXT(t *testing.T) {
	got := executableNames("python", "windows", ".COM;.EXE;.CMD")
	want := []string{"python.COM", "python.EXE", "python.CMD"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("names = %q, want %q", got, want)
	}
	if got := executableNames("python.exe", "windows", ".COM;.EXE"); len(got) != 1 || got[0] != "python.exe" {
		t.Fatalf("explicit extension names = %q", got)
	}
}

func TestExecutableFileModeWindowsPATHEXT(t *testing.T) {
	mode := os.FileMode(0o644)
	if !executableFileMode("python.ExE", mode, "windows", ".COM;.EXE;.CMD") {
		t.Fatal("case-insensitive PATHEXT executable rejected")
	}
	for _, name := range []string{"python", "python.txt", "python.EXE.txt"} {
		if executableFileMode(name, mode, "windows", ".COM;.EXE;.CMD") {
			t.Fatalf("non-PATHEXT file %q accepted", name)
		}
	}
	if executableFileMode("python.EXE", os.ModeDir|0o755, "windows", ".EXE") {
		t.Fatal("directory accepted as Windows executable")
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

func TestDiscoverEnvironmentIsSourceRelativeAndFingerprintsNestedOverlay(t *testing.T) {
	root := t.TempDir()
	pythonFixture(t, root)
	sourceDir := filepath.Join(root, "pkg")
	source := filepath.Join(sourceDir, "source.bpp")
	writeEnvironmentFile(t, source, "")
	runtime := filepath.Join(sourceDir, "runtime")
	writeEnvironmentFile(t, runtime, "runtime")
	overlay := filepath.Join(sourceDir, "bashpp.yaml")
	writeEnvironmentFile(t, overlay, "runtime: runtime\n")

	// Neither the caller's cwd nor HOME may contribute an implicit candidate.
	away := t.TempDir()
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(away); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	plan, err := DiscoverEnvironment(EnvironmentRequest{Source: source, Environ: []string{"HOME=" + away}})
	if err != nil {
		t.Fatal(err)
	}
	canonicalRuntime, err := filepath.EvalSymlinks(runtime)
	if err != nil || plan.Executable != canonicalRuntime {
		t.Fatalf("plan=%#v, canonical runtime=%q, err=%v", plan, canonicalRuntime, err)
	}
	first := plan.Fingerprint
	writeEnvironmentFile(t, overlay, "runtime: runtime\n# changed\n")
	changed, err := DiscoverEnvironment(EnvironmentRequest{Source: source, Environ: []string{"HOME=" + away}})
	if err != nil {
		t.Fatal(err)
	}
	if first == changed.Fingerprint {
		t.Fatal("nested overlay did not invalidate fingerprint")
	}
}
