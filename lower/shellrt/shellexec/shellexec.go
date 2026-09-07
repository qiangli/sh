// Package shellexec is the production dynamic-shell backend for
// [mvdan.cc/sh/v3/lower/shellrt]. It implements [shellrt.ShellRunner] on top
// of the public mvdan.cc/sh/v3/interp API.
//
// It lives in its own package so that shellrt itself stays free of the
// interpreter's module graph: a generated program with no dynamic shell region
// links neither this package nor interp. A program that does have one imports
// this package explicitly, and only regions the syntax tree identified as
// shell reach it — a typed program is never passed through it as a whole.
//
// One [interp.Runner] backs one session for its whole life, which is what
// makes shell state persistent: functions, traps, aliases, arrays and options
// set by a region are still in place for the next one.
package shellexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/lower/shellrt"
	"mvdan.cc/sh/v3/syntax"
)

// Option configures the backend.
type Option func(*config)

type config struct {
	lang    syntax.LangVariant
	runner  []interp.RunnerOption
	noTraps bool
}

// Dialect selects the language variant used to parse regions and to run them.
// The default is [syntax.LangBash].
func Dialect(lang syntax.LangVariant) Option {
	return func(c *config) { c.lang = lang }
}

// BashPP is Dialect([syntax.LangBashPP]), the dialect a lowered Bash++ program
// keeps for its dynamic regions.
func BashPP() Option { return Dialect(syntax.LangBashPP) }

// RunnerOptions passes interpreter options straight through, for embedders
// that need their own handlers. Options which the backend owns — Env, Dir,
// StdIO, Lang and the shell option flags — are applied first and can be
// overridden here, at the caller's risk.
func RunnerOptions(opts ...interp.RunnerOption) Option {
	return func(c *config) { c.runner = append(c.runner, opts...) }
}

// WithoutExitTraps stops [shellrt.Session.Close] from running the shell's EXIT
// trap. The default is to run it exactly once per session, which is what a
// shell does when it terminates.
func WithoutExitTraps() Option {
	return func(c *config) { c.noTraps = true }
}

// New returns a factory for [shellrt.WithShellFactory], so that the backend is
// built from the session's fully configured initial state and streams:
//
//	s, err := shellrt.NewSession(
//		shellrt.WithDir(dir),
//		shellrt.WithShellFactory(shellexec.New(shellexec.BashPP())),
//	)
func New(opts ...Option) shellrt.ShellFactory {
	return func(st shellrt.State, streams shellrt.Stdio) (shellrt.ShellRunner, error) {
		return NewRunner(st, streams, opts...)
	}
}

// NewRunner builds a backend directly, for callers that manage session
// construction themselves.
func NewRunner(st shellrt.State, streams shellrt.Stdio, opts ...Option) (shellrt.ShellRunner, error) {
	cfg := config{lang: syntax.LangBash}
	for _, opt := range opts {
		opt(&cfg)
	}
	base := []interp.RunnerOption{
		interp.Lang(cfg.lang),
		interp.Env(stateEnviron{vars: st.Vars}),
		interp.StdIO(streams.In, streams.Out, streams.Err),
		interp.Params(optionParams(st.Options)...),
	}
	if st.Dir != "" {
		base = append(base, interp.Dir(st.Dir))
	}
	runner, err := interp.New(append(base, cfg.runner...)...)
	if err != nil {
		return nil, err
	}
	runner.Reset()
	runner.SetLastExitStatus(clampStatus(st.Status))
	return &shell{cfg: cfg, runner: runner, last: st.Clone(), io: streams}, nil
}

// shell is one session's persistent interpreter plus the last projection it
// published, which is how it tells a typed-side write apart from shell state
// it already reported.
type shell struct {
	cfg    config
	runner *interp.Runner
	last   shellrt.State
	io     shellrt.Stdio

	closeOnce sync.Once
	closeErr  error
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

func clampStatus(code int) uint8 {
	if code < 0 || code > 255 {
		return 1
	}
	return uint8(code)
}

// Clone gives a child task its own shell. [interp.Runner.Subshell] is the
// public snapshot primitive: the copy inherits variables, functions and traps
// under the shell's own inheritance rules — ERR only under errtrace,
// DEBUG/RETURN only under functrace — and neither side writes through to the
// other. The session serializes this against the parent's other backend calls,
// which Subshell requires.
func (sh *shell) Clone(streams shellrt.Stdio) (shellrt.ShellRunner, error) {
	child := sh.runner.Subshell()
	if err := interp.StdIO(streams.In, streams.Out, streams.Err)(child); err != nil {
		return nil, err
	}
	return &shell{cfg: sh.cfg, runner: child, last: sh.last.Clone(), io: streams}, nil
}

// Close terminates the shell: it runs the EXIT trap exactly once and waits for
// the interpreter's own background work. Running an empty [syntax.File] is the
// interpreter's documented way to drive an EXIT trap without executing
// anything else.
//
// ctx is the shutdown context, which the session derives with
// [context.WithoutCancel] so that a cancelled program still runs its EXIT trap
// and still releases the interpreter's resources.
func (sh *shell) Close(ctx context.Context) error {
	sh.closeOnce.Do(func() {
		if sh.cfg.noTraps {
			return
		}
		if err := sh.runner.Run(ctx, &syntax.File{}); err != nil {
			if _, convErr := exitStatus(err); convErr != nil {
				sh.closeErr = convErr
			}
		}
	})
	return sh.closeErr
}

// RunShell applies the typed side's changes, runs one region, and refreshes
// the typed-visible projection from the live shell.
func (sh *shell) RunShell(ctx context.Context, st *shellrt.State, streams shellrt.Stdio, src string) error {
	if streams != sh.io {
		if err := sh.restoreStdio(streams); err != nil {
			return err
		}
	}
	if err := sh.applyTypedWrites(ctx, st); err != nil {
		return err
	}
	file, err := sh.parse(src, "shellrt")
	if err != nil {
		return err
	}
	// Statement by statement, not as a whole File: running a File implies an
	// exit, which would fire the EXIT trap at the end of every region. Close
	// is what ends the shell.
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

func (sh *shell) parse(src, name string) (*syntax.File, error) {
	return syntax.NewParser(syntax.Variant(sh.cfg.lang)).Parse(strings.NewReader(src), name)
}

func (sh *shell) restoreStdio(streams shellrt.Stdio) error {
	sh.io = streams
	return interp.StdIO(streams.In, streams.Out, streams.Err)(sh.runner)
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
// projection. Re-applying the whole projection would flatten shell-only
// attributes — declare -i, namerefs — on variables the typed program never
// touched.
func (sh *shell) applyTypedWrites(ctx context.Context, st *shellrt.State) error {
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
		file, err := sh.parse(script.String(), "shellrt-apply")
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
	// the status the region is meant to start from, whether it came from the
	// previous region or from a typed-side write.
	sh.runner.SetLastExitStatus(clampStatus(st.Status))
	return nil
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
func (sh *shell) project(ctx context.Context, st *shellrt.State, status int) error {
	st.Dir = sh.runner.Dir
	st.Status = status

	managed := managedNames(sh.cfg.lang)
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
		// Namerefs and live Go objects have no projection yet; they stay in
		// the shell rather than being flattened into a wrong scalar.
		return shellrt.Var{}, false
	}
	return out, true
}

// projectOptions reads the live option state back through `set +o`, since the
// interpreter exposes no public getter for it.
func (sh *shell) projectOptions(ctx context.Context) (map[string]bool, error) {
	var buf bytes.Buffer
	if err := interp.StdIO(nil, &buf, &buf)(sh.runner); err != nil {
		return nil, err
	}
	file, err := sh.parse("set +o", "shellrt-options")
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

// managedNames is the set of variables a fresh runner defines by itself. It is
// computed from the interpreter so it stays correct as the engine's defaults
// change, instead of freezing a hand-written list here.
var (
	managedMu    sync.Mutex
	managedCache = map[syntax.LangVariant]map[string]bool{}
)

func managedNames(lang syntax.LangVariant) map[string]bool {
	managedMu.Lock()
	defer managedMu.Unlock()
	if names, ok := managedCache[lang]; ok {
		return names
	}
	names := map[string]bool{}
	runner, err := interp.New(
		interp.Lang(lang),
		interp.Env(expand.ListEnviron()),
		interp.StdIO(nil, io.Discard, io.Discard),
	)
	if err == nil && runner.Run(context.Background(), &syntax.File{}) == nil {
		for name := range runner.Vars {
			names[name] = true
		}
	}
	managedCache[lang] = names
	return names
}

// stateEnviron seeds a runner from a projection, preserving each variable's
// kind and export attribute so that arrays arrive as arrays and only exported
// names reach launched processes.
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
