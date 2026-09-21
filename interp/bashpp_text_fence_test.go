// Copyright (c) 2026, the bashy authors.
// See LICENSE for licensing information.

package interp

import (
	"context"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/polyglot"
)

// A runner fence: the runner declares its methods, the alias exposes exactly
// those, an undeclared one is a prepare-time error, and PATH is never a
// runner.
func TestBashPPRunnerFenceShellFunction(t *testing.T) {
	dir := t.TempDir()
	src := `
tally() {
  case "$1" in
    methods) printf '%s\n' '{"name":"count"}' '{"name":"apply","effect":"world"}' ;;
    count) wc -l < "$2" | tr -d ' ' ;;
    apply) echo "applied:$3" ;;
  esac
}
~~~notes as n !tally
one
two
three
~~~
c := n.count()
echo "count=$c"
a := n.apply(prod)
echo "$a"
echo "file=${BASHPP_FENCE_FILE##*/} type=$BASHPP_FENCE_TYPE"
`
	stdout, stderr, err := runBashPPInDir(t, dir, src)
	if err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr)
	}
	if stdout != "count=3\napplied:prod\nfile=fence.notes type=notes\n" {
		t.Fatalf("stdout = %q (stderr %q)", stdout, stderr)
	}
}

func TestBashPPRunnerFenceBashSharpFunction(t *testing.T) {
	dir := t.TempDir()
	src := `
func handle(verb string, file string, args ...string) string {
  if verb == "methods" {
    return "{\"name\":\"size\"}"
  }
  return "size-of:" + file[len(file)-9:]
}
~~~cfg as c !handle
k = v
~~~
s := c.size()
echo "$s"
`
	stdout, stderr, err := runBashPPInDir(t, dir, src)
	if err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr)
	}
	if stdout != "size-of:fence.cfg\n" {
		t.Fatalf("stdout = %q (stderr %q)", stdout, stderr)
	}
}

func TestBashPPRunnerFenceRefusals(t *testing.T) {
	dir := t.TempDir()
	for name, tc := range map[string]struct{ src, want string }{
		"undeclared method": {`
r() { [ "$1" = methods ] && echo '{"name":"a"}'; }
~~~t as x !r
~~~
x.b()
`, "x"},
		"no methods": {`
r() { :; }
~~~t as x !r
~~~
`, "declared no methods"},
		"malformed": {`
r() { echo not-json; }
~~~t as x !r
~~~
`, "not a method object"},
		"path is never a runner": {`
~~~t as x !ls
~~~
`, "PATH is never consulted"},
		"alias required": {`
r() { echo '{"name":"a"}'; }
~~~t !r
~~~
`, "needs an alias"},
		"unknown text type without runner": {`
~~~yaml as y
a: 1
~~~
`, `unsupported language "yaml"`},
	} {
		_, stderr, err := runBashPPInDir(t, dir, tc.src)
		if err == nil && !strings.Contains(stderr, tc.want) {
			t.Errorf("%s: no error; stderr %q", name, stderr)
			continue
		}
		msg := stderr
		if err != nil {
			msg += err.Error()
		}
		if !strings.Contains(msg, tc.want) {
			t.Errorf("%s: %q does not mention %q", name, msg, tc.want)
		}
	}
}

// A built-in text row: a registered language whose runtime is polyglot.Text
// over a fake tool. The verb table is the export set and the tool receives
// the materialized artifact.
func TestBashPPTextRow(t *testing.T) {
	dir := t.TempDir()
	saved := polyglot.ToolResolver
	polyglot.ToolResolver = func(name string) ([]string, string, error) {
		if name == "fake-tool" {
			return []string{"/bin/sh", "-c", `printf '%s|%s|%s\n' "$0" "$(basename "$1")" "$(cat "$1" | tr -d '\n')"`}, "fake", nil
		}
		return nil, "", nil
	}
	polyglot.RegisterLanguage(polyglot.Language{
		Canonical: "fakecfg", Aliases: []string{"fc"},
		NewRuntime: func(cfg polyglot.RuntimeConfig) polyglot.LanguageRuntime {
			return polyglot.Text{Type: "fakecfg", FileName: "fake.cfg", Tool: "fake-tool", Dir: cfg.Dir, Environ: cfg.Environ,
				Verbs: []polyglot.Verb{{Name: "show", Args: []string{"show", "{file}"}}, {Name: "apply", Args: []string{"apply", "{file}"}, Effect: "world"}}}
		},
		LoweredRuntime: func(prefix, _ string) string { return prefix + "polyglot.Text{}" },
	})
	t.Cleanup(func() { polyglot.ToolResolver = saved })
	src := `
~~~fc as cfg
k=v
~~~
out := cfg.show()
echo "$out"
`
	stdout, stderr, err := runBashPPInDir(t, dir, src)
	if err != nil {
		t.Fatalf("run: %v\nstderr: %s", err, stderr)
	}
	if stdout != "show|fake.cfg|k=v\n" {
		t.Fatalf("stdout = %q (stderr %q)", stdout, stderr)
	}
	_, stderr, err = runBashPPInDir(t, dir, "~~~fc as cfg\nk=v\n~~~\ncfg.destroy()\n")
	if err == nil && !strings.Contains(stderr, "cfg") {
		t.Fatalf("undeclared verb accepted: %q", stderr)
	}
	// The alias is required for a text row too.
	_, stderr, err = runBashPPInDir(t, dir, "~~~fc\nk=v\n~~~\n")
	if err == nil && !strings.Contains(stderr, "alias") {
		t.Fatalf("unaliased text row accepted: %q", stderr)
	}
	_ = context.Background
}
