package shellrt_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/lower/shellrt"
	"mvdan.cc/sh/v3/syntax"
)

// interpShell is a persistent [shellrt.ShellRunner] backed by one long-lived
// interp.Runner. It is the reference implementation of the seam: the shipped
// backend must live in a package of its own, because mvdan.cc/sh/v3/lower's
// artifact tests build generated programs in a module with no go.sum, so the
// package generated code imports has to stay free of interp's module graph.
//
// One runner per session is what makes shell state persistent: functions,
// traps, aliases, arrays and options set by a region are still there for the
// next one. Only the typed-visible projection travels through State.
type interpShell struct {
	runner *interp.Runner
	last   shellrt.State
	io     shellrt.Stdio
}

func newInterpShell(st shellrt.State, streams shellrt.Stdio) (*interpShell, error) {
	runner, err := interp.New(
		interp.Env(stateEnviron{vars: st.Vars}),
		interp.Dir(st.Dir),
		interp.StdIO(streams.In, streams.Out, streams.Err),
		interp.Params(optionParams(st.Options)...),
	)
	if err != nil {
		return nil, err
	}
	runner.Reset()
	runner.SetLastExitStatus(uint8(st.Status))
	return &interpShell{runner: runner, last: st.Clone(), io: streams}, nil
}

func optionParams(options map[string]bool) []string {
	var params []string
	for _, name := range slices.Sorted(maps.Keys(options)) {
		if options[name] {
			params = append(params, "-o", name)
		}
	}
	return params
}

// Clone gives a child task its own shell. interp.Runner.Subshell is the public
// snapshot primitive: the copy inherits variables, functions and traps under
// the shell's own inheritance rules, and neither side can write through to the
// other.
func (sh *interpShell) Clone(streams shellrt.Stdio) (shellrt.ShellRunner, error) {
	child := sh.runner.Subshell()
	if err := interp.StdIO(streams.In, streams.Out, streams.Err)(child); err != nil {
		return nil, err
	}
	return &interpShell{runner: child, last: sh.last.Clone(), io: streams}, nil
}

func (sh *interpShell) Close() error { return nil }

func (sh *interpShell) RunShell(ctx context.Context, st *shellrt.State, streams shellrt.Stdio, src string) error {
	if streams != sh.io {
		if err := sh.restoreStdio(streams); err != nil {
			return err
		}
	}
	if err := sh.applyTypedWrites(ctx, st); err != nil {
		return err
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(src), "shellrt")
	if err != nil {
		return err
	}
	// Statement by statement, not as a whole File: running a File implies an
	// exit, which would fire the EXIT trap at the end of every region.
	var runErr error
	for _, stmt := range file.Stmts {
		if runErr = sh.runner.Run(ctx, stmt); runErr != nil {
			break
		}
	}
	status, err := exitStatus(runErr)
	if err != nil {
		return err
	}
	return sh.project(ctx, st, status)
}

func exitStatus(runErr error) (int, error) {
	var status interp.ExitStatus
	switch {
	case runErr == nil:
		return 0, nil
	case errors.As(runErr, &status):
		return int(status), nil
	default:
		return 1, runErr
	}
}

// applyTypedWrites pushes only what the typed side changed since the last
// projection. Re-applying everything would flatten shell-only attributes on
// variables the typed side never touched.
func (sh *interpShell) applyTypedWrites(ctx context.Context, st *shellrt.State) error {
	var script strings.Builder
	if st.Dir != sh.last.Dir {
		fmt.Fprintf(&script, "cd -- %s\n", quote(st.Dir))
	}
	for _, name := range shellrt.KnownOptions() {
		if st.Options[name] != sh.last.Options[name] {
			flag := "+o"
			if st.Options[name] {
				flag = "-o"
			}
			fmt.Fprintf(&script, "set %s %s\n", flag, name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(sh.last.Vars)) {
		if _, ok := st.Vars[name]; !ok {
			fmt.Fprintf(&script, "unset %s\n", name)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(st.Vars)) {
		v := st.Vars[name]
		if old, ok := sh.last.Vars[name]; ok && old.Equal(v) {
			continue
		}
		script.WriteString(assignment(name, v))
	}
	if script.Len() > 0 {
		file, err := syntax.NewParser().Parse(strings.NewReader(script.String()), "shellrt-apply")
		if err != nil {
			return err
		}
		for _, stmt := range file.Stmts {
			if err := sh.runner.Run(ctx, stmt); err != nil {
				if _, convErr := exitStatus(err); convErr != nil {
					return convErr
				}
			}
		}
	}
	// The applied statements are bookkeeping, not program commands: restore
	// the status the region is supposed to start from, whether it came from
	// the previous region or from a typed-side write.
	sh.runner.SetLastExitStatus(clampStatus(st.Status))
	return nil
}

func clampStatus(code int) uint8 {
	if code < 0 || code > 255 {
		return 1
	}
	return uint8(code)
}

func assignment(name string, v shellrt.Var) string {
	var b strings.Builder
	switch v.Kind {
	case shellrt.Indexed:
		fmt.Fprintf(&b, "%s=(", name)
		for i, elem := range v.List {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(quote(elem))
		}
		b.WriteString(")\n")
	case shellrt.Associative:
		fmt.Fprintf(&b, "declare -A %s=(", name)
		for _, key := range slices.Sorted(maps.Keys(v.Map)) {
			fmt.Fprintf(&b, "[%s]=%s ", quote(key), quote(v.Map[key]))
		}
		b.WriteString(")\n")
	default:
		fmt.Fprintf(&b, "%s=%s\n", name, quote(v.Str))
	}
	if v.Exported {
		fmt.Fprintf(&b, "export %s\n", name)
	}
	if v.ReadOnly {
		fmt.Fprintf(&b, "readonly %s\n", name)
	}
	return b.String()
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// project refreshes the typed-visible view from the live shell.
func (sh *interpShell) project(ctx context.Context, st *shellrt.State, status int) error {
	st.Dir = sh.runner.Dir
	st.Status = status

	managed := interpManagedNames()
	names := map[string]bool{}
	for name := range sh.last.Vars {
		names[name] = true
	}
	for name := range sh.runner.Vars {
		if !managed[name] {
			names[name] = true
		}
	}
	vars := make(map[string]shellrt.Var, len(names))
	for name := range names {
		if v, ok := projectVar(sh.runner.LiveVar(name)); ok {
			vars[name] = v
		}
	}
	st.Vars = vars

	options, err := sh.projectOptions(ctx)
	if err != nil {
		return err
	}
	st.Options = options
	// The option probe is bookkeeping too; $? must still report the region.
	sh.runner.SetLastExitStatus(clampStatus(status))
	sh.last = st.Clone()
	return nil
}

func projectVar(v expand.Variable) (shellrt.Var, bool) {
	if !v.IsSet() {
		return shellrt.Var{}, false
	}
	out := shellrt.Var{Exported: v.Exported, ReadOnly: v.ReadOnly}
	switch v.Kind {
	case expand.String:
		out.Str = v.Str
	case expand.Indexed:
		out.Kind = shellrt.Indexed
		out.List = v.IndexedValues()
	case expand.Associative:
		out.Kind = shellrt.Associative
		out.Map = maps.Clone(v.Map)
	default:
		// Namerefs and live Go objects have no projection yet.
		return shellrt.Var{}, false
	}
	return out, true
}

// projectOptions reads the live option state back through `set +o`, since the
// interpreter exposes no public getter for it.
func (sh *interpShell) projectOptions(ctx context.Context) (map[string]bool, error) {
	var buf bytes.Buffer
	saved := sh.runner.Dir
	if err := interp.StdIO(nil, &buf, &buf)(sh.runner); err != nil {
		return nil, err
	}
	file, err := syntax.NewParser().Parse(strings.NewReader("set +o"), "shellrt-options")
	if err != nil {
		return nil, err
	}
	runErr := sh.runner.Run(ctx, file.Stmts[0])
	if err := sh.restoreStdio(sh.io); err != nil {
		return nil, err
	}
	if runErr != nil {
		if _, convErr := exitStatus(runErr); convErr != nil {
			return nil, convErr
		}
	}
	if sh.runner.Dir != saved {
		return nil, fmt.Errorf("shellrt: option probe moved the working directory")
	}
	options := map[string]bool{}
	known := shellrt.KnownOptions()
	for line := range strings.Lines(buf.String()) {
		fields := strings.Fields(line)
		if len(fields) != 3 || fields[0] != "set" {
			continue
		}
		if slices.Contains(known, fields[2]) {
			options[fields[2]] = fields[1] == "-o"
		}
	}
	return options, nil
}

// restoreStdio points the runner back at the session's streams, which the
// option probe temporarily borrows.
func (sh *interpShell) restoreStdio(streams shellrt.Stdio) error {
	sh.io = streams
	return interp.StdIO(streams.In, streams.Out, streams.Err)(sh.runner)
}

// interpManagedNames is the set of variables a fresh runner defines by itself,
// computed from the interpreter so it stays correct as the engine's defaults
// change instead of freezing a hand-written list.
var interpManagedNames = sync.OnceValue(func() map[string]bool {
	names := map[string]bool{}
	runner, err := interp.New(interp.Env(expand.ListEnviron()), interp.StdIO(nil, io.Discard, io.Discard))
	if err != nil {
		return names
	}
	if err := runner.Run(context.Background(), &syntax.File{}); err != nil {
		return names
	}
	for name := range runner.Vars {
		names[name] = true
	}
	return names
})

// stateEnviron seeds a runner from a projection, preserving each variable's
// kind and export attribute.
type stateEnviron struct{ vars map[string]shellrt.Var }

func (e stateEnviron) Get(name string) expand.Variable {
	v, ok := e.vars[name]
	if !ok {
		return expand.Variable{}
	}
	vr := expand.Variable{Set: true, Exported: v.Exported, ReadOnly: v.ReadOnly}
	switch v.Kind {
	case shellrt.Indexed:
		vr.Kind, vr.List = expand.Indexed, slices.Clone(v.List)
	case shellrt.Associative:
		vr.Kind, vr.Map = expand.Associative, maps.Clone(v.Map)
	default:
		vr.Kind, vr.Str = expand.String, v.Str
	}
	return vr
}

func (e stateEnviron) Each(fn func(name string, vr expand.Variable) bool) {
	for _, name := range slices.Sorted(maps.Keys(e.vars)) {
		if !fn(name, e.Get(name)) {
			return
		}
	}
}

// newSession builds a session with a persistent interp backend, buffered
// output and no inherited process environment, so assertions see only what the
// test put there.
func newSession(t *testing.T, opts ...shellrt.SessionOption) (*shellrt.Session, *lockedBuffer) {
	t.Helper()
	out := &lockedBuffer{}
	// A real file for stdin: the interpreter copies any other reader through a
	// background goroutine per run, which is not safe to share across regions.
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { devNull.Close() })

	base := []shellrt.SessionOption{
		shellrt.WithStdio(devNull, out, out),
		shellrt.WithDir(t.TempDir()),
	}
	s, err := shellrt.NewSession(append(base, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range s.Environ() {
		name, _, _ := strings.Cut(pair, "=")
		s.Unset(name)
	}
	shell, err := newInterpShell(s.Snapshot(), s.Stdio())
	if err != nil {
		t.Fatal(err)
	}
	if err := shellrt.WithShell(shell)(s); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, out
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// TestShellStatePersistsAcrossRegions is the core of the boundary: one region
// defines a function, an array, an option and a status, and the next region
// still sees all four.
func TestShellStatePersistsAcrossRegions(t *testing.T) {
	t.Parallel()
	s, out := newSession(t)
	ctx := t.Context()

	if err := s.Shell(ctx, "f() { echo yes; }; arr=(a b); set -u; false"); err != nil {
		t.Fatal(err)
	}
	if got := s.Status(); got != 1 {
		t.Fatalf("Status() = %d, want 1", got)
	}
	if !s.Option("nounset") {
		t.Fatal("nounset was not projected after set -u")
	}
	v, ok := s.Get("arr")
	if !ok || v.Kind != shellrt.Indexed {
		t.Fatalf("arr = %+v, ok=%v; want an indexed array", v, ok)
	}
	if got, want := v.List, []string{"a", "b"}; !slices.Equal(got, want) {
		t.Fatalf("arr = %q, want %q", got, want)
	}

	// The later region observes the function, the array, the option and the
	// previous status.
	if err := s.Shell(ctx, `echo "prev=$?"; f; echo "${arr[1]}"; echo "${#arr[@]}"`); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "prev=1\nyes\nb\n2\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
	// nounset is still live, so an unset expansion still fails.
	if err := s.Shell(ctx, "echo ${missing}"); err != nil {
		t.Fatal(err)
	}
	if s.Status() == 0 {
		t.Fatal("nounset did not survive into the third region")
	}
}

func TestArrayValuesAreNotDropped(t *testing.T) {
	t.Parallel()
	s, out := newSession(t)
	ctx := t.Context()

	// Round trip an array through the typed projection untouched.
	if err := s.Shell(ctx, "arr=(one 'two three' four)"); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Get("arr")
	if got, want := v.List, []string{"one", "two three", "four"}; !slices.Equal(got, want) {
		t.Fatalf("arr = %q, want %q", got, want)
	}
	if got, want := v.String(), "one"; got != want {
		t.Fatalf("scalar view = %q, want %q", got, want)
	}
	if err := s.Shell(ctx, `printf '%s|' "${arr[@]}"; echo`); err != nil {
		t.Fatal(err)
	}

	// A typed-side array write reaches the shell whole.
	s.Set("built", shellrt.Var{Kind: shellrt.Indexed, List: []string{"x y", "z'q"}})
	if err := s.Shell(ctx, `printf '%s|' "${built[@]}"; echo`); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "one|two three|four|\nx y|z'q|\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}

	// Associative arrays survive the same round trip.
	if err := s.Shell(ctx, `declare -A m=([k]=v [k2]='v 2')`); err != nil {
		t.Fatal(err)
	}
	m, ok := s.Get("m")
	if !ok || m.Kind != shellrt.Associative {
		t.Fatalf("m = %+v, ok=%v", m, ok)
	}
	if want := map[string]string{"k": "v", "k2": "v 2"}; !maps.Equal(m.Map, want) {
		t.Fatalf("m = %v, want %v", m.Map, want)
	}
}

func TestTypedWritesReachTheShellWithoutClobberingIt(t *testing.T) {
	t.Parallel()
	s, out := newSession(t)
	ctx := t.Context()

	if err := s.Shell(ctx, "g() { echo from-g; }; keep=untouched; declare -i n=2"); err != nil {
		t.Fatal(err)
	}
	// Only `typed` changed on the typed side; `keep`, `n` and `g` must not be
	// re-applied and must keep their shell attributes.
	s.SetString("typed", "hello")
	if err := s.Shell(ctx, `echo "$typed $keep"; n=3+4; echo "$n"; g`); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "hello untouched\n7\nfrom-g\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
}

func TestExportProjection(t *testing.T) {
	t.Parallel()
	s, _ := newSession(t)
	ctx := t.Context()
	if err := s.Shell(ctx, "plain=1; shipped=2; export shipped"); err != nil {
		t.Fatal(err)
	}
	if got, want := s.Environ(), []string{"shipped=2"}; !slices.Equal(got, want) {
		t.Fatalf("Environ() = %q, want %q", got, want)
	}
	if v, _ := s.Get("plain"); v.Exported {
		t.Fatal("a plain assignment must not be exported")
	}
}

func TestTrapsPersistAndFollowShellInheritance(t *testing.T) {
	t.Parallel()
	s, out := newSession(t)
	ctx := t.Context()

	// A trap set in one region is still installed in the next: running a
	// region must not imply an exit that fires and clears EXIT traps.
	if err := s.Shell(ctx, "trap 'echo caught' USR1; trap 'echo bye' EXIT"); err != nil {
		t.Fatal(err)
	}
	if err := s.Shell(ctx, "trap -p USR1"); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "USR1") || strings.Contains(got, "bye") {
		t.Fatalf("output %q: the trap did not persist, or EXIT fired per region", got)
	}
}

func TestChildTaskShellIsIsolated(t *testing.T) {
	t.Parallel()
	s, out := newSession(t)

	if err := s.Shell(t.Context(), "shared=parent; base() { echo base; }"); err != nil {
		t.Fatal(err)
	}
	task := s.Go(func(ctx context.Context, child *shellrt.Session) error {
		// The clone inherited the parent's functions and variables.
		if err := child.Shell(ctx, `base; echo "$shared"`); err != nil {
			return err
		}
		// Mutations here must not reach the parent's shell.
		return child.Shell(ctx, "shared=child; base() { echo overridden; }; only=here")
	})
	if err := task.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := s.Join(); err != nil {
		t.Fatal(err)
	}
	if v, _ := task.Session().Get("shared"); v.Str != "child" {
		t.Fatalf("child shared = %q, want child", v.Str)
	}
	if err := s.Shell(t.Context(), `base; echo "$shared"; echo "only=${only-unset}"`); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "base\nparent\nbase\nparent\nonly=unset\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
	if _, ok := s.Get("only"); ok {
		t.Fatal("a child's variable reached the parent projection")
	}
}

func TestChildTaskTrapInheritanceAndRestoration(t *testing.T) {
	t.Parallel()
	s, out := newSession(t)
	// Without errtrace, a subshell does not inherit ERR; the DEBUG trap is
	// likewise gated on functrace. A child must observe the shell's own
	// inheritance rules rather than a blanket copy.
	if err := s.Shell(t.Context(), "trap 'echo err' ERR; trap 'echo term' TERM"); err != nil {
		t.Fatal(err)
	}
	task := s.Go(func(ctx context.Context, child *shellrt.Session) error {
		return child.Shell(ctx, "trap -p ERR; trap -p TERM; trap - TERM")
	})
	if err := task.Wait(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Contains(got, "ERR") {
		t.Fatalf("output %q: ERR must not be inherited without errtrace", got)
	}
	if !strings.Contains(got, "TERM") {
		t.Fatalf("output %q: TERM should be inherited", got)
	}
	// The child cleared TERM; the parent's is untouched.
	out.mu.Lock()
	out.buf.Reset()
	out.mu.Unlock()
	if err := s.Shell(t.Context(), "trap -p TERM"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "TERM") {
		t.Fatalf("output %q: the child cleared the parent's trap", out.String())
	}
}

func TestSessionCwdPersists(t *testing.T) {
	t.Parallel()
	s, _ := newSession(t)
	start := s.Dir()
	if err := os.Mkdir(filepath.Join(start, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Shell(t.Context(), "cd sub"); err != nil {
		t.Fatal(err)
	}
	if got, want := s.Dir(), filepath.Join(start, "sub"); got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
	// A typed-side Chdir reaches the shell too.
	if err := s.Chdir(start); err != nil {
		t.Fatal(err)
	}
	if err := s.Shell(t.Context(), "true"); err != nil {
		t.Fatal(err)
	}
	if got := s.Dir(); got != start {
		t.Fatalf("Dir() = %q, want %q", got, start)
	}
}

func TestSessionStatusIsNotAnError(t *testing.T) {
	t.Parallel()
	s, _ := newSession(t)
	if err := s.Shell(t.Context(), "false"); err != nil {
		t.Fatalf("non-zero status must not be an error: %v", err)
	}
	if got := s.Status(); got != 1 {
		t.Fatalf("Status() = %d, want 1", got)
	}
	if err := s.Shell(t.Context(), "exit 3"); err != nil {
		t.Fatal(err)
	}
	if got := s.Status(); got != 3 {
		t.Fatalf("Status() = %d, want 3", got)
	}
	if err := s.Shell(t.Context(), "true"); err != nil {
		t.Fatal(err)
	}
	if got := s.Status(); got != 0 {
		t.Fatalf("Status() = %d, want 0", got)
	}
	// A typed-side status write is what the next region's $? reads.
	s.SetStatus(42)
	if err := s.Shell(t.Context(), `echo "$?"`); err != nil {
		t.Fatal(err)
	}
}

func TestSessionShellParseErrorIsReported(t *testing.T) {
	t.Parallel()
	s, _ := newSession(t)
	if err := s.Shell(t.Context(), "if"); err == nil {
		t.Fatal("want a parse error")
	}
}

func TestSessionSnapshotIsIndependent(t *testing.T) {
	t.Parallel()
	s, _ := newSession(t)
	s.Set("a", shellrt.Var{Kind: shellrt.Indexed, List: []string{"1"}, Exported: true})
	if err := s.SetOption("xtrace", true); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()

	s.Set("a", shellrt.Var{Str: "2"})
	s.Set("b", shellrt.Var{Str: "3"})
	if err := s.SetOption("xtrace", false); err != nil {
		t.Fatal(err)
	}

	if got := snap.Vars["a"].List; !slices.Equal(got, []string{"1"}) {
		t.Fatalf("snapshot a = %q, want [1]", got)
	}
	if _, ok := snap.Vars["b"]; ok {
		t.Fatal("snapshot must not see later writes")
	}
	if !snap.Options["xtrace"] {
		t.Fatal("snapshot options must not track later writes")
	}
	// Mutating the snapshot's array must not reach the session.
	snap.Vars["a"].List[0] = "mutated"
	if v, _ := s.Get("a"); v.Str != "2" {
		t.Fatalf("session a = %+v", v)
	}
}

func TestSessionChdirValidates(t *testing.T) {
	t.Parallel()
	s, _ := newSession(t)
	start := s.Dir()
	if err := s.Chdir("nope"); err == nil {
		t.Fatal("want an error for a missing directory")
	}
	if got := s.Dir(); got != start {
		t.Fatalf("Dir() = %q after a failed Chdir, want %q", got, start)
	}
	if err := os.Mkdir(filepath.Join(start, "kid"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Chdir("kid"); err != nil {
		t.Fatal(err)
	}
	if got, want := s.Dir(), filepath.Join(start, "kid"); got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
}

func TestSessionWithoutShellBackend(t *testing.T) {
	t.Parallel()
	s, err := shellrt.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Shell(t.Context(), "echo hi"); !errors.Is(err, shellrt.ErrNoShell) {
		t.Fatalf("err = %v, want ErrNoShell", err)
	}
}

func TestSessionUnknownOptionRejected(t *testing.T) {
	t.Parallel()
	s, _ := newSession(t)
	if err := s.SetOption("nosuchoption", true); err == nil {
		t.Fatal("want an error for an unknown option")
	}
}

// recordingShell proves the dynamic-shell seam is the only route to a shell,
// and that it receives exactly the region the caller passed.
type recordingShell struct {
	mu     sync.Mutex
	src    []string
	clones int
	closed int
}

func (r *recordingShell) RunShell(ctx context.Context, st *shellrt.State, io shellrt.Stdio, src string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.src = append(r.src, src)
	st.Vars["seen"] = shellrt.Var{Str: src}
	st.Status = 7
	return nil
}

func (r *recordingShell) Clone(io shellrt.Stdio) (shellrt.ShellRunner, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clones++
	return &recordingShell{}, nil
}

func (r *recordingShell) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed++
	return nil
}

func TestSessionShellSeamIsReplaceable(t *testing.T) {
	t.Parallel()
	rec := &recordingShell{}
	s, err := shellrt.NewSession(shellrt.WithShell(rec))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Shell(t.Context(), "anything at all"); err != nil {
		t.Fatal(err)
	}
	if got, want := rec.src, []string{"anything at all"}; !slices.Equal(got, want) {
		t.Fatalf("regions = %q, want %q", got, want)
	}
	if v, _ := s.Get("seen"); v.Str != "anything at all" {
		t.Fatalf("seen = %q", v.Str)
	}
	if got := s.Status(); got != 7 {
		t.Fatalf("Status() = %d, want 7", got)
	}
	// Every child gets its own backend, and Close releases the session's.
	s.Go(func(ctx context.Context, child *shellrt.Session) error { return nil })
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.clones != 1 {
		t.Fatalf("clones = %d, want 1", rec.clones)
	}
	if rec.closed != 1 {
		t.Fatalf("closed = %d, want 1", rec.closed)
	}
}

// failingCloneShell makes the child snapshot fail, which must surface as that
// task's failure rather than as a stuck launcher.
type failingCloneShell struct{ recordingShell }

func (f *failingCloneShell) Clone(io shellrt.Stdio) (shellrt.ShellRunner, error) {
	return nil, errors.New("no snapshot for you")
}

func TestTaskCloneFailureIsTheTaskFailure(t *testing.T) {
	t.Parallel()
	s, err := shellrt.NewSession(shellrt.WithShell(&failingCloneShell{}))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	task := s.Go(func(ctx context.Context, child *shellrt.Session) error {
		t.Error("the body must not run when the snapshot failed")
		return nil
	})
	if err := task.Wait(); err == nil || !strings.Contains(err.Error(), "task snapshot") {
		t.Fatalf("err = %v, want a task snapshot failure", err)
	}
	if task.Session() != nil {
		t.Fatal("a task that never started must have no session")
	}
	var terr *shellrt.TaskError
	if err := s.Join(); !errors.As(err, &terr) || terr.Ordinal != 0 {
		t.Fatalf("Join() = %v, want task 0's failure", err)
	}
}
