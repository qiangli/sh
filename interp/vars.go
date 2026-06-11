// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"bytes"
	cryptorand "crypto/rand"
	"encoding/binary"
	"fmt"
	"maps"
	mathrand "math/rand/v2"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func newOverlayEnviron(parent expand.Environ, background bool) *overlayEnviron {
	oenv := &overlayEnviron{}
	if !background {
		oenv.parent = parent
	} else {
		// We could do better here if the parent is also an overlayEnviron;
		// measure with profiles or benchmarks before we choose to do so.
		for name, vr := range parent.Each {
			oenv.Set(name, vr)
		}
	}
	return oenv
}

// overlayEnviron is our main implementation of [expand.WriteEnviron].
type overlayEnviron struct {
	// parent is non-nil if [values] is an overlay over a parent environment
	// which we can safely reuse without data races, such as non-background subshells
	// or function calls.
	parent expand.Environ

	// values maps normalized variable names, per [overlayEnviron.normalize].
	values map[string]namedVariable

	// We need to know if the current scope is a function's scope, because
	// functions can modify global variables. When true, [parent] must not be nil.
	funcScope bool

	// funsubScope is set for bash 5.3 `${ cmd; }` / mksh `${|cmd;}` bodies.
	// Like funcScope it allows `return` and `local`; ordinary assignments
	// still write through to the caller, while local variables remain scoped
	// to the overlay. Implies funcScope.
	funsubScope bool
}

// namedVariable records the original name of a variable for platforms
// where variable names are matched in a case-insensitive way.
type namedVariable struct {
	// TODO(v4): consider adding this field to [expand.Variable],
	// as a general way for a variable to report its original name.
	// This can be useful for GOOS=windows with case insensitive env vars,
	// as otherwise it's not possible to Environ.Get a var
	// and know what was its original name without looping over Environ.Each.
	Name string
	expand.Variable
}

func (o *overlayEnviron) normalize(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

func (o *overlayEnviron) Get(name string) expand.Variable {
	normalized := o.normalize(name)
	if vr, ok := o.values[normalized]; ok {
		return vr.Variable
	}
	if o.parent != nil {
		return o.parent.Get(name)
	}
	return expand.Variable{}
}

func (o *overlayEnviron) Set(name string, vr expand.Variable) error {
	normalized := o.normalize(name)
	prev, inOverlay := o.values[normalized]
	// Manipulation of a global var inside a function or funsub. Local
	// variables stay in the overlay; ordinary assignments write through.
	if o.funcScope && !vr.Local && !prev.Local {
		// In a function, the parent environment is ours, so it's always read-write.
		return o.parent.(expand.WriteEnviron).Set(name, vr)
	}
	if !inOverlay && o.parent != nil {
		prev.Variable = o.parent.Get(name)
		// Bash only refuses `local` when it would shadow a readonly
		// *global* ("since I believe that this could be a security
		// hole" — variables.c); a readonly local belonging to a
		// calling function may be shadowed, and the shadow starts
		// fresh rather than inheriting the value or readonly bit.
		if vr.Local && prev.ReadOnly && prev.Local {
			prev.Variable = expand.Variable{}
			vr.ReadOnly = false
		}
	}

	if o.values == nil {
		o.values = make(map[string]namedVariable)
	}
	if vr.Kind == expand.KeepValue {
		vr.Kind = prev.Kind
		vr.Str = prev.Str
		vr.List = prev.List
		vr.ListSet = prev.ListSet
		vr.Map = prev.Map
	} else if prev.ReadOnly {
		return fmt.Errorf("readonly variable")
	}
	if !vr.IsSet() { // unsetting
		// Preserve the variable when it carries an attribute that
		// must outlive the assignment — local, integer, exported,
		// readonly, case-conversion — so `declare -u foo` (no
		// value) stays declared.
		if prev.Local || vr.Exported || vr.ReadOnly ||
			vr.Upper || vr.Lower || vr.Capitalize {
			vr.Local = prev.Local || vr.Local
			o.values[normalized] = namedVariable{name, vr}
			return nil
		}
		delete(o.values, normalized)
	}
	// modifying the entire variable
	vr.Local = prev.Local || vr.Local
	o.values[normalized] = namedVariable{name, vr}
	return nil
}

// holdsLocally reports whether this overlay itself has an entry for
// name, regardless of any parent environment.
func (o *overlayEnviron) holdsLocally(name string) bool {
	_, ok := o.values[o.normalize(name)]
	return ok
}

func (o *overlayEnviron) hasLocalVar(name string) bool {
	vr, ok := o.values[o.normalize(name)]
	return ok && vr.Local
}

// unsetLocalFromChild implements bash's default `unset` semantics for
// a variable that is local to a *previous* function scope: the local
// is removed entirely, uncovering the variable beneath (an outer local
// or the global). Returns false when no such local exists — e.g. the
// nearest binding is a global, or a readonly local that must keep
// reporting an error through the ordinary path. The walk stops at
// non-function boundaries (subshells) so it never mutates an
// environment shared across goroutines.
func (o *overlayEnviron) unsetLocalFromChild(name string) bool {
	normalized := o.normalize(name)
	if prev, ok := o.values[normalized]; ok {
		if prev.Local && !prev.ReadOnly {
			delete(o.values, normalized)
			return true
		}
		return false
	}
	if !o.funcScope {
		return false
	}
	p, ok := o.parent.(*overlayEnviron)
	return ok && p.unsetLocalFromChild(name)
}

func (o *overlayEnviron) Each(f func(name string, vr expand.Variable) bool) {
	if o.parent != nil {
		o.parent.Each(f)
	}
	for _, vr := range o.values {
		if !f(vr.Name, vr.Variable) {
			return
		}
	}
}

func execEnv(env expand.Environ) []string {
	list := make([]string, 0, 64)
	for name, vr := range env.Each {
		if name == BashyInheritedFdsEnv {
			continue
		}
		if !vr.IsSet() && !vr.Local {
			// If a variable is set globally but unset in the
			// runner, we need to ensure it's not part of the final
			// list. Seems like zeroing the element is enough.
			// This is a linear search, but this scenario should be
			// rare, and the number of variables shouldn't be large.
			// Unset *locals* are skipped: like bash's invisible
			// variables they merely shadow within the shell, so an
			// exported global beneath them still reaches children.
			for i, kv := range list {
				if strings.HasPrefix(kv, name+"=") {
					list[i] = ""
				}
			}
		}
		if vr.Exported && vr.Kind == expand.String {
			list = append(list, name+"="+vr.String())
		}
	}
	return list
}

// execEnvWithFuncs is like execEnv but also serialises any exported
// shell functions as bash's `BASH_FUNC_<name>%%=() { body; }` entries.
// Used when spawning a child process so a `${THIS_SH} -c '<name>'`
// invocation can re-import the function on startup.
func (r *Runner) execEnvWithFuncs() []string {
	list := execEnv(r.writeEnv)
	for _, name := range []string{"BASHOPTS", "SHELLOPTS"} {
		if !r.writeEnv.Get(name).Exported {
			continue
		}
		prefix := name + "="
		for i, kv := range list {
			if strings.HasPrefix(kv, prefix) {
				list[i] = ""
			}
		}
		list = append(list, prefix+r.lookupVar(name).String())
	}
	if len(r.exportedFuncs) == 0 {
		return list
	}
	for name := range r.exportedFuncs {
		body, ok := r.Funcs[name]
		if !ok {
			continue
		}
		list = append(list, "BASH_FUNC_"+name+"%%="+bashExportedFuncValue(name, body))
	}
	return list
}

func bashExportedFuncValue(name string, body *syntax.Stmt) string {
	block, ok := body.Cmd.(*syntax.Block)
	if !ok {
		var b strings.Builder
		syntax.NewPrinter(syntax.SpaceRedirects(true), syntax.BashCompatArith(true)).Print(&b, &syntax.FuncDecl{
			Name: &syntax.Lit{Value: name},
			Body: body,
		})
		s := strings.TrimSpace(b.String())
		if rest, ok := strings.CutPrefix(s, name); ok {
			s = strings.TrimSpace(rest)
		}
		return s
	}
	var b strings.Builder
	b.WriteString("() { \n")
	printer := syntax.NewPrinter(syntax.Indent(4), syntax.SpaceRedirects(true), syntax.BashCompatArith(true))
	for i, st := range block.Stmts {
		var stmt bytes.Buffer
		printer.Print(&stmt, st)
		rendered := bashDeclareFmt(strings.TrimRight(stmt.String(), "\n"), i == len(block.Stmts)-1)
		b.WriteString(rendered)
		b.WriteByte('\n')
		if endsWithHeredocTerminator(stmt.String()) {
			b.WriteByte('\n')
		}
	}
	b.WriteString("}")
	for _, rd := range body.Redirs {
		text := formatRedirect(rd)
		if strings.HasPrefix(text, ">&") {
			text = "1" + text
		} else if strings.HasPrefix(text, "<&") {
			text = "0" + text
		}
		b.WriteByte(' ')
		b.WriteString(text)
	}
	return b.String()
}

func (r *Runner) lookupVar(name string) expand.Variable {
	if name == "" {
		// A nameref whose target is the empty string (or other
		// invalid identifier) can reach here via Variable.Resolve.
		// Bash treats this as "unset"; return the zero value
		// instead of crashing.
		return expand.Variable{}
	}
	var vr expand.Variable
	switch name {
	case "#":
		vr.Kind, vr.Str = expand.String, strconv.Itoa(len(r.Params))
	case "@", "*":
		vr.Kind = expand.Indexed
		if r.Params == nil {
			// r.Params may be nil but positional parameters always exist
			vr.List = []string{}
		} else {
			vr.List = r.Params
		}
		vr.Set = len(vr.List) > 0
	case "!":
		// Prefer the real OS PID of the last backgrounded statement's
		// spawned process so the `PID=$!; kill $PID` idiom works
		// against the kernel. Wait briefly for the bg goroutine to
		// either Start an exec.Cmd (typical) or finish without one
		// (`(true) &`); pidReady is closed in either case so this
		// can't hang. Falls back to the legacy "g<N>" sentinel when
		// no real exec ever happened — `wait g<N>` still works against
		// that for pure-builtin backgrounds.
		if n := len(r.bgProcs); n > 0 {
			bg := r.bgProcs[n-1]
			<-bg.pidReady
			if pid := bg.pid.Load(); pid > 0 {
				vr.Kind, vr.Str = expand.String, strconv.FormatInt(pid, 10)
			} else {
				vr.Kind, vr.Str = expand.String, "g"+strconv.Itoa(n)
			}
		}
	case "?":
		vr.Kind, vr.Str = expand.String, strconv.Itoa(int(r.lastExit.code))
	case "$":
		if r.deterministic {
			vr.Kind, vr.Str = expand.String, strconv.Itoa(int(r.deterministicSeed&0x7fff))
		} else {
			vr.Kind, vr.Str = expand.String, strconv.Itoa(os.Getpid())
		}
	case "PPID":
		vr.Kind, vr.Str = expand.String, strconv.Itoa(os.Getppid())
	case "-":
		// Bash's $- expands to the single-letter forms of all
		// currently-set option flags. Always-on defaults (`h` —
		// hashall, `B` — braceexpand) are surfaced too so the
		// output is non-empty even on a fresh shell.
		var sb strings.Builder
		if r.commandString {
			sb.WriteByte('c')
		}
		if r.opts[optAllExport] {
			sb.WriteByte('a')
		}
		if r.opts[optErrExit] {
			sb.WriteByte('e')
		}
		if r.opts[optNoGlob] {
			sb.WriteByte('f')
		}
		if r.opts[optNoExec] {
			sb.WriteByte('n')
		}
		if r.opts[optNoUnset] {
			sb.WriteByte('u')
		}
		if r.opts[optXTrace] {
			sb.WriteByte('x')
		}
		if r.opts[optPipeFail] {
			// pipefail has no single-letter form; bash 5.3
			// emits nothing for it in $-.
		}
		sb.WriteByte('h') // hashall (always on)
		sb.WriteByte('B') // braceexpand (always on)
		if r.noOpSetState["physical"] {
			// `set -P` / `set -o physical` is accept-and-ignore, but
			// bash surfaces it in $- once toggled on.
			sb.WriteByte('P')
		}
		vr.Kind, vr.Str = expand.String, sb.String()
	case "RANDOM": // not for cryptographic use
		if r.randomSeeded {
			vr.Kind, vr.Str = expand.String, strconv.Itoa(r.bashRandom())
		} else if r.deterministic && r.deterministicRng != nil {
			vr.Kind, vr.Str = expand.String, strconv.Itoa(int(r.deterministicRng.Uint64()&0x7fff))
		} else {
			vr.Kind, vr.Str = expand.String, strconv.Itoa(mathrand.IntN(32767))
		}
	case "SRANDOM": // pseudo-random generator from the system
		if r.deterministic && r.deterministicRng != nil {
			vr.Kind, vr.Str = expand.String, strconv.FormatUint(r.deterministicRng.Uint64()&0xffffffff, 10)
		} else {
			var p [4]byte
			cryptorand.Read(p[:])
			n := binary.NativeEndian.Uint32(p[:])
			vr.Kind, vr.Str = expand.String, strconv.FormatUint(uint64(n), 10)
		}
	case "BASHPID":
		if r.deterministic {
			vr.Kind, vr.Str = expand.String, strconv.Itoa(int(r.deterministicSeed&0x7fff))
		} else {
			// Real bash returns the OS PID, which differs in a forked
			// subshell. Our subshells are goroutines (same OS PID), so
			// shift by subshellLevel to make BASHPID differ per
			// subshell layer — scripts that compare $BASHPID across
			// boundaries (the canonical use case) keep working.
			vr.Kind, vr.Str = expand.String, strconv.Itoa(os.Getpid()+r.subshellLevel)
		}
	case "SECONDS":
		if r.deterministic {
			vr.Kind, vr.Str = expand.String, "0"
		} else {
			vr.Kind, vr.Str = expand.String, strconv.FormatInt(int64(time.Since(r.startTime).Seconds()), 10)
		}
	case "EPOCHSECONDS":
		if r.deterministic {
			vr.Kind, vr.Str = expand.String, strconv.FormatInt(r.startTime.Unix(), 10)
		} else {
			vr.Kind, vr.Str = expand.String, strconv.FormatInt(time.Now().Unix(), 10)
		}
	case "EPOCHREALTIME":
		if r.deterministic {
			vr.Kind, vr.Str = expand.String, fmt.Sprintf("%d.000000", r.startTime.Unix())
		} else {
			now := time.Now()
			vr.Kind, vr.Str = expand.String, fmt.Sprintf("%d.%06d", now.Unix(), now.Nanosecond()/1000)
		}
	case "BASH_MONOSECONDS":
		// Bash 5.3 monotonic clock — seconds since an unspecified point,
		// unaffected by wall-clock adjustments. Reusing the shell's
		// monotonic start time keeps the value stable across calls and
		// deterministic mode.
		vr.Kind, vr.Str = expand.String, strconv.FormatInt(int64(time.Since(r.startTime).Seconds()), 10)
	case "BASH_SUBSHELL":
		vr.Kind, vr.Str = expand.String, strconv.Itoa(r.subshellLevel)
	case "BASH_ARGV0":
		vr.Kind = expand.String
		switch {
		case r.argv0 != "":
			vr.Str = r.argv0
		case r.filename != "":
			vr.Str = r.filename
		default:
			vr.Str = "bashy"
		}
	case "GROUPS":
		gid := os.Getgid()
		vr.Kind = expand.Indexed
		vr.ReadOnly = true
		vr.List = []string{strconv.Itoa(gid)}
	case "HOSTNAME":
		h, _ := os.Hostname()
		vr.Kind, vr.Str = expand.String, h
	case "COLUMNS":
		// Bash exposes the terminal width via $COLUMNS. We query the
		// controlling stdin/stdout/stderr for a TTY size; if none of
		// them is a terminal, the variable stays empty so scripts can
		// detect "no TTY" via [[ -z $COLUMNS ]].
		if w := terminalWidth(); w > 0 {
			vr.Kind, vr.Str = expand.String, strconv.Itoa(w)
		}
	case "LINES":
		if _, h := terminalSize(); h > 0 {
			vr.Kind, vr.Str = expand.String, strconv.Itoa(h)
		}
	case "HOSTTYPE":
		vr.Kind, vr.Str = expand.String, runtime.GOARCH
	case "MACHTYPE":
		vr.Kind, vr.Str = expand.String, runtime.GOARCH+"-unknown-"+runtime.GOOS
	case "OSTYPE":
		vr.Kind, vr.Str = expand.String, runtime.GOOS
	case "SHELLOPTS":
		var opts []string
		// Append the long names of POSIX-table options that are
		// currently enabled.
		for i, opt := range &posixOptsTable {
			if r.opts[i] && opt.name != "" {
				opts = append(opts, opt.name)
			}
		}
		// Bash 5.3 also includes a curated subset of the
		// default-on no-op options — braceexpand, hashall,
		// interactive-comments — so scripts can read
		// SHELLOPTS without having to know which options are
		// "real" vs. compat-only.
		for _, name := range []string{"braceexpand", "hashall", "interactive-comments"} {
			if enabled, ok := r.noOpSetState[name]; ok && !enabled {
				continue
			}
			opts = append(opts, name)
		}
		// Accept-and-ignore options that bash nonetheless reflects in
		// SHELLOPTS once toggled on (`shopt -so physical`).
		if r.noOpSetState["physical"] {
			opts = append(opts, "physical")
		}
		slices.Sort(opts)
		vr.Kind, vr.Str = expand.String, strings.Join(opts, ":")
		vr.ReadOnly = true
	case "BASHOPTS":
		var opts []string
		for i, opt := range bashOptsTable {
			if r.opts[len(posixOptsTable)+i] {
				opts = append(opts, opt.name)
			}
		}
		vr.Kind, vr.Str = expand.String, strings.Join(opts, ":")
		vr.ReadOnly = true
	case "BASH_VERSINFO":
		vr.Kind = expand.Indexed
		vr.ReadOnly = true
		vr.List = []string{"5", "3", "0", "1", "release", "bashy"}
	case "FUNCNAME":
		vr.Kind = expand.Indexed
		// Bash appends "main" as the outermost frame in FUNCNAME so
		// that scripts can reliably introspect the call chain back
		// to the top-level. Match that — but only when there's at
		// least one function on the stack; an empty FUNCNAME stays
		// unset, like bash.
		if len(r.callStack) > 0 {
			names := make([]string, len(r.callStack)+1)
			for i, f := range r.callStack {
				names[len(r.callStack)-1-i] = f.funcName
			}
			names[len(r.callStack)] = "main"
			vr.List = names
		}
	case "BASH_SOURCE":
		vr.Kind = expand.Indexed
		sources := make([]string, len(r.callStack))
		for i, f := range r.callStack {
			sources[len(r.callStack)-1-i] = f.source
		}
		vr.List = sources
	case "BASH_LINENO":
		vr.Kind = expand.Indexed
		lines := make([]string, len(r.callStack))
		for i, f := range r.callStack {
			lines[len(r.callStack)-1-i] = strconv.FormatUint(uint64(f.line), 10)
		}
		vr.List = lines
	case "PIPESTATUS":
		vr.Kind = expand.Indexed
		if r.pipeStatus != nil {
			vr.List = r.pipeStatus
		} else {
			vr.List = []string{strconv.Itoa(int(r.lastExit.code))}
		}
	case "DIRSTACK":
		// Bash exposes DIRSTACK with the top-of-stack at index 0,
		// matching `dirs` output. r.dirStack stores the stack with
		// the top at the end, so reverse it.
		vr.Kind = expand.Indexed
		vr.List = make([]string, len(r.dirStack))
		for i, d := range r.dirStack {
			vr.List[len(r.dirStack)-1-i] = d
		}
	case "BASH_ALIASES":
		vr.Kind = expand.Associative
		vr.Map = make(map[string]string, len(r.alias))
		for k, als := range r.alias {
			vr.Map[k] = aliasValue(als)
		}
	case "BASH_CMDS":
		vr.Kind = expand.Associative
		vr.Map = make(map[string]string, len(r.cmdHashTable))
		for name, entry := range r.cmdHashTable {
			vr.Map[name] = entry.path
		}
	case "0":
		vr.Kind = expand.String
		switch {
		case r.argv0 != "":
			vr.Str = r.argv0
		case r.filename != "":
			vr.Str = r.filename
		default:
			vr.Str = "bashy"
		}
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if i := int(name[0] - '1'); i < len(r.Params) {
			vr.Kind = expand.String
			vr.Str = r.Params[i]
		}
	default:
		// Handle multi-digit positional parameters: ${10}, ${11}, etc.
		if len(name) > 1 && name[0] >= '1' && name[0] <= '9' {
			allDigits := true
			for _, c := range name {
				if c < '0' || c > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				n, _ := strconv.Atoi(name)
				if n > 0 && n <= len(r.Params) {
					vr.Kind = expand.String
					vr.Str = r.Params[n-1]
				}
			}
		}
	}
	if vr.Kind != expand.Unknown {
		if name == "@" || name == "*" {
			return vr
		}
		vr.Set = true
		return vr
	}
	if vr := r.writeEnv.Get(name); vr.Declared() {
		return vr
	}
	return expand.Variable{}
}

func (r *Runner) envGet(name string) string {
	return r.lookupVar(name).String()
}

func (r *Runner) bashRandom() int {
	seed := r.randomSeed
	if seed == 0 {
		seed = 123459876
	}
	h := seed / 127773
	l := seed % 127773
	t := int32(16807*l) - int32(2836*h)
	if t < 0 {
		seed = uint32(t + 0x7fffffff)
	} else {
		seed = uint32(t)
	}
	r.randomSeed = seed
	return int(((seed >> 16) ^ (seed & 0xffff)) & 0x7fff)
}

// printLocalVars writes the local variables of the current function
// scope to stdout in bash's `local` listing format:
//
//	name=value                                  (scalar)
//	name=([0]="x" [1]="y")                      (indexed array)
//	name=([k]="v")                              (associative array)
//
// Iteration order is sorted by name to match bash. Only the current
// overlay is enumerated; vars inherited from the parent scope are
// skipped — those aren't local.
func (r *Runner) printLocalVars() {
	overlay, ok := r.writeEnv.(*overlayEnviron)
	if !ok || overlay == nil {
		return
	}
	names := make([]string, 0, len(overlay.values))
	for k := range overlay.values {
		names = append(names, k)
	}
	slices.Sort(names)
	for _, name := range names {
		nv := overlay.values[name]
		if !nv.Variable.IsSet() {
			continue
		}
		r.outf("%s\n", formatLocalVar(name, nv.Variable))
	}
}

// printArrayVars writes every variable of the requested array kind
// (`-A` associative, `-a` indexed) to stdout in declare -p format,
// sorted by name. Built-in bash arrays (BASH_ALIASES, BASH_CMDS,
// BASH_ARGC, …) appear alongside user-declared ones so scripts
// that probe `declare -A` see them.
func (r *Runner) printArrayVars(kind string) {
	want := expand.Associative
	if kind == "-a" {
		want = expand.Indexed
	}
	seen := map[string]bool{}
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
		}
	}
	// Built-in arrays that bash always exposes — pull them so they
	// list even when the user hasn't touched the variable.
	if want == expand.Associative {
		for _, n := range []string{"BASH_ALIASES", "BASH_CMDS"} {
			add(n)
		}
	} else {
		for _, n := range []string{"BASH_ARGC", "BASH_ARGV", "BASH_LINENO", "BASH_SOURCE", "DIRSTACK", "FUNCNAME"} {
			add(n)
		}
	}
	r.writeEnv.Each(func(name string, vr expand.Variable) bool {
		if vr.Kind == want {
			add(name)
		}
		return true
	})
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, name := range names {
		vr := r.lookupVar(name)
		if want == expand.Indexed {
			if builtin, ok := bashBuiltinArrayDeclVar(name, r); ok {
				vr = builtin
			}
		}
		if vr.Kind != want {
			continue
		}
		isBuiltin := false
		switch name {
		case "BASH_ALIASES", "BASH_CMDS", "BASH_ARGC", "BASH_ARGV", "BASH_LINENO", "BASH_SOURCE", "DIRSTACK", "FUNCNAME":
			isBuiltin = true
		}
		if name == "FUNCNAME" && !vr.Set && len(vr.List) == 0 {
			isBuiltin = false
		}
		r.outf("%s\n", formatDeclareVar(name, vr, isBuiltin))
	}
}

func bashBuiltinArrayDeclVar(name string, r *Runner) (expand.Variable, bool) {
	switch name {
	case "BASH_ARGC", "BASH_ARGV":
		return expand.Variable{Kind: expand.Indexed, Set: true, List: []string{}}, true
	case "BASH_LINENO":
		vr := r.lookupVar(name)
		if len(vr.List) == 0 {
			vr.Set = true
			vr.List = []string{"0"}
		}
		return vr, true
	case "BASH_SOURCE":
		vr := r.lookupVar(name)
		if len(vr.List) == 0 && r.filename != "" {
			vr.Set = true
			vr.List = []string{r.filename}
		}
		return vr, true
	case "DIRSTACK":
		return expand.Variable{Kind: expand.Indexed, Set: true, List: []string{}}, true
	case "FUNCNAME":
		vr := r.lookupVar(name)
		if len(vr.List) == 0 {
			vr.Set = false
			vr.List = nil
		}
		return vr, true
	}
	return expand.Variable{}, false
}

// printNamerefVars writes every nameref-typed variable to stdout in
// bash's `declare -n NAME="target"` format, sorted by name. Used
// by `typeset -n` / `declare -n` with no arguments.
func (r *Runner) printNamerefVars() {
	var names []string
	r.writeEnv.Each(func(name string, vr expand.Variable) bool {
		if vr.Kind == expand.NameRef {
			names = append(names, name)
		}
		return true
	})
	slices.Sort(names)
	for _, name := range names {
		vr := r.lookupVar(name)
		r.outf("declare -n %s=%s\n", name, bashDeclareQuote(vr.Str))
	}
}

func (r *Runner) printReadonlyVars() {
	seen := map[string]bool{}
	r.writeEnv.Each(func(name string, vr expand.Variable) bool {
		if vr.ReadOnly {
			seen[name] = true
		}
		return true
	})
	for _, name := range []string{"BASHOPTS", "BASH_VERSINFO", "GROUPS", "SHELLOPTS"} {
		if r.lookupVar(name).ReadOnly {
			seen[name] = true
		}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		vr := r.lookupVar(name)
		if vr.ReadOnly {
			r.outf("%s\n", formatDeclareVar(name, vr, false))
		}
	}
}

// formatLocalVar renders a single variable in bash 5.3's `local`
// listing shape: `declare <flags> name=value`. `<flags>` covers
// `-a` (indexed), `-A` (associative), `-i`/`-r`/`-x`/etc., or `--`
// when no other flag is set. Values use the same quoting rules as
// `declare -p`.
func formatLocalVar(name string, vr expand.Variable) string {
	return formatDeclareVar(name, vr, false)
}

func formatDeclareVar(name string, vr expand.Variable, forceEmptyArrayValue bool) string {
	flags := vr.Flags()
	if flags == "" {
		flags = "-"
	}
	var b strings.Builder
	b.WriteString("declare -")
	b.WriteString(flags)
	b.WriteByte(' ')
	b.WriteString(name)
	switch vr.Kind {
	case expand.Indexed:
		if !vr.Set && !forceEmptyArrayValue && len(vr.List) == 0 {
			return b.String()
		}
		b.WriteByte('=')
		b.WriteByte('(')
		first := true
		for _, i := range vr.IndexedIndexes() {
			if !first {
				b.WriteByte(' ')
			}
			first = false
			fmt.Fprintf(&b, "[%d]=%s", i, bashDeclareQuote(vr.List[i]))
		}
		b.WriteByte(')')
	case expand.Associative:
		if !vr.Set && !forceEmptyArrayValue && len(vr.Map) == 0 {
			return b.String()
		}
		b.WriteByte('=')
		b.WriteByte('(')
		first := true
		for _, k := range vr.AssocKeysForDeclare() {
			if !first {
				b.WriteByte(' ')
			}
			first = false
			fmt.Fprintf(&b, "[%s]=%s", bashAssocKeyQuote(k), bashDeclareQuote(vr.Map[k]))
		}
		if !first {
			b.WriteByte(' ')
		}
		b.WriteByte(')')
	default:
		if !vr.Set {
			return b.String()
		}
		b.WriteByte('=')
		b.WriteString(bashDeclareQuote(vr.Str))
	}
	return b.String()
}

func bashAssocKeyQuote(s string) string {
	if s == "" {
		return bashDeclareQuote(s)
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z',
			'A' <= c && c <= 'Z',
			'0' <= c && c <= '9',
			c == '_', c == '.', c == '%', c == '-':
			continue
		default:
			return bashDeclareQuote(s)
		}
	}
	return s
}

func variableJSON(name string, vr expand.Variable) map[string]any {
	flags := vr.Flags()
	if flags == "" {
		flags = "-"
	}
	obj := map[string]any{
		"name":        name,
		"flags":       flags,
		"set":         vr.Set,
		"exported":    vr.Exported,
		"readonly":    vr.ReadOnly,
		"integer":     vr.Integer,
		"local":       vr.Local,
		"uppercase":   vr.Upper,
		"lowercase":   vr.Lower,
		"capitalized": vr.Capitalize,
	}
	switch vr.Kind {
	case expand.Indexed:
		obj["kind"] = "indexed"
		values := make(map[string]string, vr.IndexedCount())
		for _, i := range vr.IndexedIndexes() {
			values[strconv.Itoa(i)] = vr.List[i]
		}
		obj["value"] = values
	case expand.Associative:
		obj["kind"] = "associative"
		m := make(map[string]string, len(vr.Map))
		for k, v := range vr.Map {
			m[k] = v
		}
		obj["value"] = m
	case expand.NameRef:
		obj["kind"] = "nameref"
		obj["value"] = vr.Str
	case expand.String:
		obj["kind"] = "string"
		obj["value"] = vr.Str
	default:
		obj["kind"] = "unknown"
	}
	return obj
}

func (r *Runner) variablesJSON(setOnly bool) []map[string]any {
	var names []string
	r.writeEnv.Each(func(name string, vr expand.Variable) bool {
		if setOnly && !vr.IsSet() {
			return true
		}
		if !setOnly && !vr.Declared() {
			return true
		}
		names = append(names, name)
		return true
	})
	slices.Sort(names)
	var vars []map[string]any
	for _, name := range names {
		vars = append(vars, variableJSON(name, r.writeEnv.Get(name)))
	}
	return vars
}

func (r *Runner) functionJSON(name string) map[string]any {
	obj := map[string]any{
		"name":     name,
		"readonly": r.readonlyFuncs[name],
		"exported": r.exportedFuncs[name],
	}
	if body := r.Funcs[name]; body != nil {
		var b strings.Builder
		syntax.NewPrinter().Print(&b, body)
		obj["body"] = strings.TrimRight(b.String(), "\n")
		obj["line"] = int(body.Pos().Line())
	}
	return obj
}

func (r *Runner) functionsJSON(readonlyOnly, exportedOnly bool) []map[string]any {
	names := make([]string, 0, len(r.Funcs))
	for name := range r.Funcs {
		if readonlyOnly && !r.readonlyFuncs[name] {
			continue
		}
		if exportedOnly && !r.exportedFuncs[name] {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	funcs := make([]map[string]any, 0, len(names))
	for _, name := range names {
		funcs = append(funcs, r.functionJSON(name))
	}
	return funcs
}

func (r *Runner) globalWriteEnv() expand.WriteEnviron {
	env := r.writeEnv
	for {
		ol, ok := env.(*overlayEnviron)
		if !ok || ol.parent == nil {
			break
		}
		nextWE, ok := ol.parent.(*overlayEnviron)
		if !ok {
			break
		}
		env = nextWE
	}
	wenv, _ := env.(expand.WriteEnviron)
	return wenv
}

func (r *Runner) lookupGlobalVar(name string) expand.Variable {
	wenv := r.globalWriteEnv()
	if wenv == nil {
		return r.lookupVar(name)
	}
	if vr := wenv.Get(name); vr.Declared() {
		return vr
	}
	return expand.Variable{}
}

// setGlobalVarString assigns name=value at the outermost (global)
// scope, bypassing any in-flight function overlays. Used for bash
// 5.3 `{var}` redirections, which set the captured fd globally even
// when the redirect is inside a function body.
func (r *Runner) setGlobalVarString(name, value string) {
	if n, _ := r.lookupVar(name).Resolve(r.writeEnv); n != "" {
		name = n
	}
	vr := expand.Variable{Set: true, Kind: expand.String, Str: value}
	r.setGlobalVar(name, vr)
}

// bashShoptEnabled reports the state of a bash `shopt` option by name.
func (r *Runner) bashShoptEnabled(name string) bool {
	for i, opt := range bashOptsTable {
		if opt.name == name {
			return r.opts[len(posixOptsTable)+i]
		}
	}
	return false
}

func (r *Runner) delVar(name string) {
	// Unsetting a variable that is local to a *previous* function scope
	// removes that local entirely, uncovering the variable beneath
	// (bash's default dynamic-unset behavior). With shopt
	// localvar_unset, the local is instead marked unset and keeps
	// shadowing, which is what the ordinary path below implements.
	if !r.bashShoptEnabled("localvar_unset") {
		if o, ok := r.writeEnv.(*overlayEnviron); ok && o.funcScope && !o.holdsLocally(name) {
			if p, ok := o.parent.(*overlayEnviron); ok && p.unsetLocalFromChild(name) {
				return
			}
		}
	}
	if err := r.writeEnv.Set(name, expand.Variable{}); err != nil {
		r.errf("%s%s: %v\n", r.bashErrPrefix(r.curStmtPos), name, err)
		r.exit.code = 1
		return
	}
}

// splitArrayRef recognises `name[index]` references used by builtins
// like `unset` to address a single array element. Returns the bare
// variable name and the (unparsed) index expression; ok is false if
// the input is not in the `NAME[INDEX]` form.
func splitArrayRef(s string) (name, idx string, ok bool) {
	lb := strings.IndexByte(s, '[')
	if lb <= 0 || !strings.HasSuffix(s, "]") {
		return "", "", false
	}
	return s[:lb], s[lb+1 : len(s)-1], true
}

// unsetArrayElem removes a single element from an indexed or
// associative array. For indexed arrays the index is arithmetic;
// for associative arrays it's a literal key. `name[*]` / `name[@]`
// unset the entire variable (matching bash).
func (r *Runner) unsetArrayElem(name, idx string) bool {
	if idx == "*" || idx == "@" {
		vr := r.lookupVar(name)
		if vr.Kind == expand.Indexed && vr.IndexedCount() > 0 {
			vr.Set = true
			vr.List = []string{}
			vr.ListSet = nil
			r.setVar(name, vr)
		} else {
			r.delVar(name)
		}
		return true
	}
	vr := r.lookupVar(name)
	switch vr.Kind {
	case expand.Indexed:
		n, err := strconv.Atoi(idx)
		if err != nil || n < 0 {
			r.errf("%sunset: [%s]: bad array subscript\n", r.bashErrPrefix(r.curStmtPos), idx)
			return false
		}
		if n >= len(vr.List) {
			return true
		}
		set := vr.DenseListSet()
		delete(set, n)
		vr.List[n] = ""
		for len(vr.List) > 0 && !set[len(vr.List)-1] {
			vr.List = vr.List[:len(vr.List)-1]
		}
		vr.ListSet = set
		if len(vr.List) == 0 {
			vr.ListSet = nil
		}
	case expand.Associative:
		if _, ok := vr.Map[idx]; ok {
			delete(vr.Map, idx)
		}
	default:
		return true
	}
	r.setVar(name, vr)
	return true
}

func (r *Runner) setVarString(name, value string) {
	if base, idx, ok := splitArrayRef(name); ok && syntax.ValidName(base) {
		w := &syntax.Word{Parts: []syntax.WordPart{
			&syntax.Lit{Value: idx},
		}}
		r.setVarWithIndex(r.lookupVar(base), base, w, expand.Variable{Set: true, Kind: expand.String, Str: value})
		return
	}
	r.setVar(name, expand.Variable{Set: true, Kind: expand.String, Str: value})
}

func validAssignName(name string) bool {
	if syntax.ValidName(name) {
		return true
	}
	base, _, ok := splitArrayRef(name)
	return ok && syntax.ValidName(base)
}

// bashDeclareQuote formats v the way bash's `declare -p` does: bare
// when safe, double-quoted otherwise, falling back to ANSI-C $'...'
// when v contains characters that double quotes can't represent
// (control bytes, NULs, non-UTF-8 bytes, etc.).
// bashSetQuote formats `v` the way bash's `set` (no args) builtin
// does: no quotes for fully-safe strings, backslash-escape for a
// single shell-special character, single-quotes for everything else
// (with `'` itself rendered as `'\”`). Differs from
// [bashDeclareQuote] (which always double-quotes) and from
// [syntax.Quote] (which always quotes `#`).
func bashSetQuote(v string) string {
	if v == "" {
		return "''"
	}
	if !bashSetNeedsQuoting(v) {
		return v
	}
	// Bash 5.3's `set` falls back to backslash-escape only for the
	// single-quote character itself — everything else uses single
	// quotes. Empty result from this if-block falls through to the
	// general single-quoting case below.
	if v == "'" {
		return `\'`
	}
	// General case: single-quote, with embedded `'` → `'\''`.
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

// bashSetNeedsQuoting reports whether a value contains any character
// bash's `set` would treat specially. The leading-only chars `#` and
// `~` are special only at position 0.
func bashSetNeedsQuoting(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch c {
		case ' ', '\t', '\n', '\r', '\v', '\f',
			'"', '\'', '`', '\\', '$',
			';', '|', '&', '<', '>', '(', ')',
			'{', '}', '[', ']', '*', '?', '!':
			return true
		case '#', '~', '=':
			if i == 0 {
				return true
			}
		}
		if c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

func bashDeclareQuote(v string) string {
	if hasNonPrintable(v) {
		q, err := syntax.Quote(v, syntax.LangBash)
		if err == nil {
			return q
		}
	}
	// Mirror bash's "always double-quote inside the array literal".
	// Escape the four characters that have special meaning in double-
	// quoted bash: ", \, $, ` — leaving everything else literal so the
	// output matches `declare -p` byte-for-byte.
	var b strings.Builder
	b.Grow(len(v) + 2)
	b.WriteByte('"')
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c == '"' || c == '\\' || c == '$' || c == '`' {
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')
	return b.String()
}

func hasNonPrintable(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f {
			return true
		}
	}
	return false
}

// aliasValue returns the textual form of an alias as bash stores it in
// BASH_ALIASES: the args reprinted as shell source, plus a trailing
// space when the original definition ended in whitespace.
func aliasValue(als alias) string {
	var buf strings.Builder
	if als.raw != "" {
		buf.WriteString(als.raw)
	} else if len(als.args) > 0 {
		syntax.NewPrinter().Print(&buf, &syntax.CallExpr{Args: als.args})
	}
	if als.blank {
		buf.WriteByte(' ')
	}
	return buf.String()
}

func validAliasName(name string) bool {
	return name != "" && !strings.ContainsAny(name, "\\$")
}

// terminalSize probes stdin/stdout/stderr for a terminal and returns
// the first valid (cols, rows). Returns (0, 0) if none of them is a
// terminal — callers use that to leave $COLUMNS / $LINES empty.
func terminalSize() (cols, rows int) {
	for _, fd := range []int{
		int(os.Stdin.Fd()),
		int(os.Stdout.Fd()),
		int(os.Stderr.Fd()),
	} {
		if c, r, err := term.GetSize(fd); err == nil {
			return c, r
		}
	}
	return 0, 0
}

func terminalWidth() int {
	c, _ := terminalSize()
	return c
}

func (r *Runner) setVar(name string, vr expand.Variable) {
	if r.opts[optRestricted] && (name == "PATH" || name == "SHELL") {
		r.errf("%s%s: readonly variable\n", r.bashErrPrefix(r.curStmtPos), name)
		return
	}
	if name == "RANDOM" && vr.IsSet() {
		if seed, err := strconv.ParseInt(vr.String(), 10, 64); err == nil {
			r.randomSeeded = true
			r.randomSeed = uint32(seed)
		}
		return
	}
	if r.opts[optAllExport] {
		vr.Exported = true
	}
	// Carry forward case-conversion attributes from any prior
	// declaration of the same name (`declare -u foo` followed by
	// `foo=value`), and fold the value through them so the stored
	// value already reflects the attribute.
	prev := r.lookupVar(name)
	if prev.Upper || prev.Lower || prev.Capitalize {
		vr.Upper, vr.Lower, vr.Capitalize = prev.Upper, prev.Lower, prev.Capitalize
	}
	applyCaseAttr(&vr)
	// BASH_ARGV0 is writable. Update r.argv0 so subsequent reads of
	// $0 and $BASH_ARGV0 see the new value. r.filename stays as-is so
	// error messages keep referring to the original script.
	if name == "BASH_ARGV0" && vr.IsSet() {
		r.argv0 = vr.Str
	}
	// POSIXLY_CORRECT switches bash into POSIX mode whenever it is
	// set (and back out when unset). Mirror that here so subsequent
	// POSIX-gated behaviour (special-builtin assignment persistence,
	// etc.) kicks in without an explicit `set -o posix`.
	if name == "POSIXLY_CORRECT" {
		r.setPosixMode(vr.IsSet() && vr.Str != "")
	}
	// BASH_XTRACEFD: when assigned, validate that the file
	// descriptor is open and writable; bash 5.3 emits
	// "BASH_XTRACEFD: <fd>: invalid value for trace file
	// descriptor" otherwise and refuses to use it.
	if name == "BASH_XTRACEFD" && vr.IsSet() && vr.Str != "" {
		if fd, err := strconv.Atoi(vr.Str); err == nil && fd >= 0 {
			if _, ok := r.fdTable[fd]; !ok {
				r.errf("%sBASH_XTRACEFD: %s: invalid value for trace file descriptor\n",
					r.bashErrPrefix(r.curStmtPos), vr.Str)
				return
			}
		}
	}
	// Bash refuses to convert between indexed and associative arrays
	// via the declare family; the variable keeps its prior kind. The
	// failure is attributed to the builtin, with an extra
	// function-attributed line when it came from an array literal
	// assignment inside a function (matching bash 5.3's double report).
	if r.declAssignContext && prev.Declared() {
		var convErr string
		switch {
		case prev.Kind == expand.Indexed && vr.Kind == expand.Associative:
			convErr = "cannot convert indexed to associative array"
		case prev.Kind == expand.Associative && vr.Kind == expand.Indexed:
			convErr = "cannot convert associative to indexed array"
		}
		if convErr != "" {
			if r.setVarArrayLiteral && len(r.callStack) > 0 {
				r.errf("%s%s: %s: %s\n", r.bashErrPrefix(r.curStmtPos),
					r.callStack[len(r.callStack)-1].funcName, name, convErr)
			}
			builtin := r.setVarFromBuiltin
			if builtin == "" {
				builtin = "declare"
			}
			r.errf("%s%s: %s: %s\n", r.bashErrPrefix(r.curStmtPos), builtin, name, convErr)
			r.exit.code = 1
			return
		}
		// `declare -A name` (no value) on an existing scalar converts
		// it like bash's convert_var_to_assoc: the old value becomes
		// element "0", and the resulting table has 128 hash buckets
		// (assoc_create(0)) rather than a fresh declare -A's 1024,
		// which `declare -p` key ordering reflects.
		if vr.Kind == expand.Associative && vr.Map == nil && vr.Set && prev.Kind == expand.String {
			vr.Map = map[string]string{"0": vr.Str}
			vr.Str = ""
			vr.AssocBuckets = 128
		}
	}
	// `local x` (no value) creates a fresh unset local; bash only
	// inherits the previous scope's value with shopt localvar_inherit.
	// Explicitly-requested attributes like `-i`/`-u` are kept, but the
	// parent's value is not.
	if vr.Local && vr.Kind == expand.KeepValue &&
		!r.bashShoptEnabled("localvar_inherit") {
		if ol, ok := r.writeEnv.(*overlayEnviron); ok && ol.funcScope && !ol.holdsLocally(name) {
			vr = expand.Variable{
				Local:      true,
				Integer:    vr.Integer,
				Upper:      vr.Upper,
				Lower:      vr.Lower,
				Capitalize: vr.Capitalize,
			}
		}
	}
	if err := r.writeEnv.Set(name, vr); err != nil {
		// Bash 5.3 attribution: syntax-level assignment failures
		// inside a function are prefixed with the function name
		// (`<file>: line N: <fn>: <var>: readonly variable`);
		// failures from a declare-family builtin's array-conversion
		// path (`readonly -a 'name=value'`) are prefixed with the
		// builtin name. Plain string-form assignments via the same
		// builtins get no extra prefix.
		var inner string
		switch {
		case r.setVarFromBuiltin != "":
			inner = r.setVarFromBuiltin + ": "
		case r.setVarStringParsed:
			// String-parsed assignment without array flag: no extra
			// prefix beyond `<file>: line N: <var>:`.
		case r.setVarArrayLiteral && len(r.callStack) > 0:
			// Syntax-level array-literal assignment inside a
			// function: bash attributes to the function name.
			inner = r.callStack[len(r.callStack)-1].funcName + ": "
		}
		// An array-literal assignment failing on a readonly variable
		// through `local` is reported twice by bash: once for the
		// failed assignment, once from the builtin. `readonly`/`export`
		// and declare-family forms report only once (see attr.tests).
		if r.setVarFromBuiltin == "local" &&
			(vr.Kind == expand.Indexed || vr.Kind == expand.Associative) &&
			strings.Contains(err.Error(), "readonly") {
			r.errf("%s%s: %v\n", r.bashErrPrefix(r.curStmtPos), name, err)
		}
		r.errf("%s%s%s: %v\n", r.bashErrPrefix(r.curStmtPos), inner, name, err)
		r.exit.code = 1
		return
	}
}

func (r *Runner) rejectDeclareConversion(name string, prev, vr expand.Variable) bool {
	if !r.declAssignContext || !prev.Declared() {
		return false
	}
	var convErr string
	switch {
	case prev.Kind == expand.Indexed && vr.Kind == expand.Associative:
		convErr = "cannot convert indexed to associative array"
	case prev.Kind == expand.Associative && vr.Kind == expand.Indexed:
		convErr = "cannot convert associative to indexed array"
	}
	if convErr == "" {
		return false
	}
	if r.setVarArrayLiteral && len(r.callStack) > 0 {
		r.errf("%s%s: %s: %s\n", r.bashErrPrefix(r.curStmtPos),
			r.callStack[len(r.callStack)-1].funcName, name, convErr)
	}
	builtin := r.setVarFromBuiltin
	if builtin == "" {
		builtin = "declare"
	}
	r.errf("%s%s: %s: %s\n", r.bashErrPrefix(r.curStmtPos), builtin, name, convErr)
	r.exit.code = 1
	return true
}

func (r *Runner) setVarWithIndex(prev expand.Variable, name string, index syntax.ArithmExpr, vr expand.Variable) {
	if vr.Kind == expand.String && index == nil {
		// When assigning a string to an array, fall back to the
		// zero value for the index.
		switch prev.Kind {
		case expand.Indexed:
			index = &syntax.Word{Parts: []syntax.WordPart{
				&syntax.Lit{Value: "0"},
			}}
		case expand.Associative:
			index = &syntax.Word{Parts: []syntax.WordPart{
				&syntax.Lit{Value: "0"},
			}}
		}
	}
	if index == nil {
		r.setVar(name, vr)
		return
	}

	// from the syntax package, we know that value must be a string if index
	// is non-nil; nested arrays are forbidden.
	valStr := vr.Str

	var list []string
	var listSet map[int]bool
	switch prev.Kind {
	case expand.String:
		list = append(list, prev.Str)
	case expand.Indexed:
		// TODO: only clone when inside a subshell and getting a var from outside for the first time
		list = slices.Clone(prev.List)
		listSet = prev.CloneListSet()
	case expand.Associative:
		// if the existing variable is already an AssocArray, try our
		// best to convert the key to a string
		w, ok := index.(*syntax.Word)
		if !ok {
			return
		}
		k := r.assocAssignKey(w)
		if name == "BASH_ALIASES" || name == "BASH_CMDS" {
			k = r.literal(w)
		}
		if name == "BASH_ALIASES" && !validAliasName(k) {
			r.errf("%s`%s': invalid alias name\n", r.bashErrPrefix(r.curStmtPos), k)
			r.exit.code = 1
			return
		}
		if name == "BASH_CMDS" {
			if info, err := os.Stat(valStr); err == nil && info.IsDir() {
				r.errf("%s%s: Is a directory\n", r.bashErrPrefix(r.curStmtPos), valStr)
				r.exit.code = 1
				return
			}
			if r.opts[optRestricted] {
				if strings.Contains(valStr, "/") {
					r.errf("%s%s: restricted\n", r.bashErrPrefix(r.curStmtPos), valStr)
					r.exit.code = 1
					return
				}
				if _, err := LookPathDir(r.Dir, r.writeEnv, valStr); err != nil {
					r.errf("%s%s: not found\n", r.bashErrPrefix(r.curStmtPos), valStr)
					r.exit.code = 1
					return
				}
			}
			if r.cmdHashTable == nil {
				r.cmdHashTable = make(map[string]cmdHashEntry)
			}
			r.cmdHashTable[k] = cmdHashEntry{path: valStr, restricted: r.opts[optRestricted]}
			return
		}

		// TODO: only clone when inside a subshell and getting a var from outside for the first time
		prev.Map = maps.Clone(prev.Map)
		if prev.Map == nil {
			prev.Map = make(map[string]string)
		}
		if prev.Integer {
			valStr = r.integerArrayValue(valStr)
		}
		prev.Map[k] = valStr
		r.setVar(name, prev)
		return
	}
	k := r.arithm(index)
	if k < 0 {
		// Bash 5.3 accepts negative indices as offsets from the
		// end of the array: `a[-1]` targets the last element.
		k = len(list) + k
		if k < 0 {
			r.errf("%s%s: bad array subscript\n", r.bashErrPrefix(r.curStmtPos), name)
			r.exit.code = 1
			return
		}
	}
	if name == "DIRSTACK" {
		if k >= len(r.dirStack) {
			return
		}
		r.dirStack[len(r.dirStack)-1-k] = valStr
		return
	}
	for len(list) < k+1 {
		list = append(list, "")
	}
	list[k] = valStr
	if listSet != nil {
		listSet[k] = true
	} else if k >= len(prev.List) {
		listSet = prev.DenseListSet()
		if listSet == nil {
			listSet = make(map[int]bool)
			if prev.Kind == expand.String {
				listSet[0] = true
			}
		}
		listSet[k] = true
	}
	prev.Kind = expand.Indexed
	prev.List = list
	prev.ListSet = listSet
	r.setVar(name, prev)
}

func (r *Runner) integerArrayValue(s string) string {
	if s == "" {
		return "0"
	}
	expr, perr := syntax.NewParser().Arithmetic(strings.NewReader(s))
	if perr != nil || expr == nil {
		return "0"
	}
	v, _ := expand.Arithm(r.ecfg, expr)
	return strconv.Itoa(v)
}

func (r *Runner) setFunc(name string, body *syntax.Stmt) {
	if r.Funcs == nil {
		r.Funcs = make(map[string]*syntax.Stmt, 4)
	}
	r.Funcs[name] = body
}

func stringIndex(index syntax.ArithmExpr) bool {
	w, ok := index.(*syntax.Word)
	if !ok || len(w.Parts) != 1 {
		return false
	}
	switch w.Parts[0].(type) {
	case *syntax.DblQuoted, *syntax.SglQuoted:
		return true
	}
	return false
}

// wordLooksLikeAssign mirrors bash's W_ASSIGNMENT check for the first
// word of a compound assoc assignment: `name=...` (or `name+=...`)
// words keep the subscript-required error path rather than enabling
// key/value pair mode.
func wordLooksLikeAssign(w *syntax.Word) bool {
	if w == nil || len(w.Parts) == 0 {
		return false
	}
	lit, ok := w.Parts[0].(*syntax.Lit)
	if !ok {
		return false
	}
	eq := strings.IndexByte(lit.Value, '=')
	if eq <= 0 {
		return false
	}
	return syntax.ValidName(strings.TrimSuffix(lit.Value[:eq], "+"))
}

func bashAssocAssignKey(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case '"', '\'', '$', '`', '\\', ']', '[':
				i++
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func (r *Runner) assocAssignKey(w *syntax.Word) string {
	if len(w.Parts) == 1 {
		if lit, ok := w.Parts[0].(*syntax.Lit); ok {
			return bashAssocAssignKey(lit.Value)
		}
	}
	if key, ok := r.assocAssignKeyLiteral(w.Parts); ok {
		return bashAssocAssignKey(key)
	}
	return r.literal(w)
}

func (r *Runner) assocAssignKeyLiteral(parts []syntax.WordPart) (string, bool) {
	var b strings.Builder
	for _, part := range parts {
		switch part := part.(type) {
		case *syntax.Lit:
			b.WriteString(part.Value)
		case *syntax.SglQuoted:
			b.WriteString(part.Value)
		case *syntax.DblQuoted:
			s, ok := r.assocAssignKeyLiteral(part.Parts)
			if !ok {
				return "", false
			}
			b.WriteString(s)
		case *syntax.ParamExp:
			if part.Param == nil || part.NestedParam != nil || part.Index != nil ||
				part.Slice != nil || part.Repl != nil || part.Exp != nil ||
				part.Length || part.Width || part.Excl || part.Names != 0 {
				return "", false
			}
			b.WriteString(r.envGet(part.Param.Value))
		default:
			return "", false
		}
	}
	return b.String(), true
}

// TODO: make assignVal and [setVar] consistent with the [expand.WriteEnviron] interface

func (r *Runner) assignVal(name string, prev expand.Variable, as *syntax.Assign, valType string) (string, expand.Variable) {
	// `declare -n NAME=target` retargets the nameref itself —
	// don't dereference the existing nameref first or we'd
	// overwrite the *target* (e.g. `typeset -n fee=flow` followed
	// by `typeset -n fee=flip` should leave fee→flip, not
	// flow=flip).
	if valType != "-n" {
		if n, v := prev.Resolve(r.writeEnv); n != "" {
			name, prev = n, v
		}
	}
	prevWasSet := prev.Set
	prev.Set = true
	if as.Value != nil {
		// Use the assignment-context literal expansion so `var=foo:~/bin`
		// expands the post-colon tilde to $HOME, matching bash.
		s, err := expand.LiteralForAssign(r.ecfg, as.Value)
		r.expandErr(err)
		// Integer attribute (declare -i): parse the RHS as an
		// arithmetic expression and evaluate it. For =, the result
		// replaces the value; for +=, it's added to the current
		// numeric value (also re-parsed as arithmetic — bash 5.3
		// honours integer-attribute math even when the prior value
		// was stored as a literal expression like `4+1`).
		if prev.Integer && valType != "-a" && valType != "-A" {
			arithEval := func(s string) (int, error) {
				if s == "" {
					return 0, nil
				}
				expr, perr := syntax.NewParser().Arithmetic(strings.NewReader(s))
				if perr != nil || expr == nil {
					return 0, perr
				}
				return expand.Arithm(r.ecfg, expr)
			}
			arithAssignErr := func(s string, err error) {
				if err == nil {
					return
				}
				prefix := r.filename
				if prefix == "" {
					prefix = "bashy"
				}
				r.expandErr(fmt.Errorf("%s: line %d: %s: %s",
					prefix, as.Value.Pos().Line(), s, err))
			}
			rhs, err := arithEval(s)
			if err != nil {
				arithAssignErr(s, err)
				return name, prev
			}
			if as.Append {
				curStr := prev.Str
				// For indexed arrays the integer-flag bumps each
				// element through the same arithmetic-evaluate
				// path; pull the element's prior text from
				// prev.List so `a[i]+=N` reads N from there.
				// Bare `a+=N` (no index) on an integer indexed
				// array targets element 0 — same rule bash uses
				// for non-integer indexed arrays.
				switch {
				case as.Index != nil && prev.Kind == expand.Indexed:
					k := r.arithm(as.Index)
					if prev.IndexedSet(k) {
						curStr = prev.List[k]
					} else {
						curStr = ""
					}
				case prev.Kind == expand.Indexed:
					if len(prev.List) > 0 {
						curStr = prev.List[0]
					} else {
						curStr = ""
					}
				case as.Index != nil && prev.Kind == expand.Associative:
					if w, ok := as.Index.(*syntax.Word); ok {
						curStr = prev.Map[r.assocAssignKey(w)]
					}
				}
				cur, err := arithEval(curStr)
				if err != nil {
					arithAssignErr(curStr, err)
					return name, prev
				}
				rhs = cur + rhs
			}
			prev.Kind = expand.String
			prev.Str = strconv.Itoa(rhs)
			return name, prev
		}
		if !as.Append {
			// The array-kind-preserving logic only applies to
			// whole-variable assignments (`a=v`) in a declare-
			// family context (declare/readonly/local/export), not
			// to indexed forms or inline `v=foo cmd` calls.
			if as.Index == nil && r.declAssignContext {
				// `declare -a name=(elem1 elem2 …)` reaching here
				// via string-form flattening hands us the raw
				// `(elem1 …)` text; re-parse it as an array literal
				// so the array kind sticks and parens get stripped.
				if (valType == "-a" || valType == "-A") && strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
					inner := s[1 : len(s)-1]
					if valType == "-a" {
						prev.Kind = expand.Indexed
						prev.List = expand.ReadFields(r.ecfg, inner, -1, false)
						if prev.List == nil {
							prev.List = []string{}
						}
					} else { // "-A"
						prev.Kind = expand.Associative
						if prev.Map == nil {
							prev.Map = map[string]string{}
						}
						// TODO: full key=value parsing for assoc string-form.
						prev.Map["0"] = inner
					}
					return name, prev
				}
				// Bash: when the existing variable is an indexed or
				// associative array, a scalar assignment (`a=v`) in
				// declare-family context sets element [0] (or key
				// "0") rather than collapsing to a scalar.
				switch prev.Kind {
				case expand.Indexed:
					if valType != "-n" {
						newList := slices.Clone(prev.List)
						if len(newList) == 0 {
							newList = []string{s}
						} else {
							newList[0] = s
						}
						prev.List = newList
						if prev.ListSet != nil {
							prev.ListSet = prev.CloneListSet()
							prev.ListSet[0] = true
						}
						return name, prev
					}
				case expand.Associative:
					if valType != "-n" {
						newMap := make(map[string]string, len(prev.Map)+1)
						for k, v := range prev.Map {
							newMap[k] = v
						}
						newMap["0"] = s
						prev.Map = newMap
						return name, prev
					}
				}
			}
			prev.Kind = expand.String
			if valType == "-n" {
				prev.Kind = expand.NameRef
			}
			prev.Str = s
			return name, prev
		}
		switch prev.Kind {
		case expand.String, expand.Unknown:
			// `declare -a s+=Y` on a scalar converts it to an indexed
			// array first; the append then targets element 0.
			if valType == "-a" && r.declAssignContext {
				prev.Kind = expand.Indexed
				prev.List = []string{prev.Str + s}
				prev.ListSet = nil
				prev.Str = ""
				return name, prev
			}
			prev.Kind = expand.String
			prev.Str += s
		case expand.Indexed:
			// `arr[i]+=s` appends `s` onto the existing element
			// at index `i`. setVarWithIndex receives vr.Str and
			// writes it into list[k] for us, so seed the scalar
			// with the prior element's value here.
			if as.Index != nil {
				k := r.arithm(as.Index)
				var cur string
				if prev.IndexedSet(k) {
					cur = prev.List[k]
				}
				prev.Kind = expand.String
				prev.Str = cur + s
				return name, prev
			}
			// Bare `arr+=s` (no index) targets element 0, per
			// bash's "treat as `arr[0]+=s`" rule.
			if len(prev.List) == 0 {
				prev.List = append(prev.List, "")
			}
			prev.List[0] += s
			if prev.ListSet != nil {
				prev.ListSet = prev.CloneListSet()
				prev.ListSet[0] = true
			}
		case expand.Associative:
			// `arr[k]+=s` for associative arrays appends `s`
			// onto the existing value at key `k`. setVarWithIndex
			// receives vr.Str and writes it to map[k] for us.
			if as.Index != nil {
				w, ok := as.Index.(*syntax.Word)
				if ok {
					k := r.assocAssignKey(w)
					prev.Kind = expand.String
					if prev.Integer {
						cur, _ := strconv.Atoi(r.integerArrayValue(prev.Map[k]))
						rhs, _ := strconv.Atoi(r.integerArrayValue(s))
						prev.Str = strconv.Itoa(cur + rhs)
					} else {
						prev.Str = prev.Map[k] + s
					}
					return name, prev
				}
				break
			}
			// `arr+=value` (no index) on an assoc array sets key
			// "0" — bash treats the bare-scalar append on an
			// assoc array as `arr[0]+=value`.
			newMap := maps.Clone(prev.Map)
			if newMap == nil {
				newMap = make(map[string]string)
			}
			if prev.Integer {
				cur, _ := strconv.Atoi(r.integerArrayValue(newMap["0"]))
				rhs, _ := strconv.Atoi(r.integerArrayValue(s))
				newMap["0"] = strconv.Itoa(cur + rhs)
			} else {
				newMap["0"] = newMap["0"] + s
			}
			prev.Map = newMap
			return name, prev
		}
		return name, prev
	}
	if as.Array == nil {
		// don't return the zero value, as that's an unset variable
		if valType == "-n" {
			// `typeset -n NAME` (no value) converts an existing
			// scalar to a nameref pointing at whatever its
			// current value names (bash 5.3 behavior). Preserve
			// the existing Str — `foo=bar; typeset -n foo` keeps
			// foo's "bar" and dereferences it on read.
			prev.Kind = expand.NameRef
			return name, prev
		}
		// Plain `declare NAME` (no value, no -n): start fresh as
		// an empty string so `declare x; echo "<$x>"` shows `<>`.
		prev.Kind = expand.String
		prev.Str = ""
		return name, prev
	}
	// Array assignment.
	elems := as.Array.Elems
	if valType == "" {
		valType = "-a" // indexed
		switch {
		case as.Append && prev.Kind == expand.Associative:
			// `assoc+=([key]=value)` on an already-associative
			// var preserves the assoc kind even when the index
			// is a bare literal, not a quoted string. Plain
			// `assoc=(…)` (no `+=`) still falls through to the
			// inference below for back-compat.
			valType = "-A"
		case prev.Kind == expand.Associative:
			valType = "-A"
		case len(elems) > 0 && stringIndex(elems[0].Index):
			valType = "-A" // associative
		}
	}
	if valType == "-A" {
		amap := make(map[string]string, len(elems))
		assocBuckets := prev.AssocBuckets
		// Converting an existing scalar mirrors bash's
		// convert_var_to_assoc: a 128-bucket hash table (which
		// `declare -p` key ordering reflects), with the old value
		// surviving at key "0" only for `+=`-style appends.
		if prev.Kind == expand.String && prev.Declared() {
			assocBuckets = 128
			if as.Append && prev.Set {
				amap["0"] = prev.Str
			}
		}
		// Inherit prev's map when this is a `+=`-style append at the
		// outer assignment level; per-element `[k]+=v` appends to the
		// previous value of that key.
		if as.Append && prev.Map != nil {
			for k, v := range prev.Map {
				amap[k] = v
			}
		}
		// bash 5.1+: a compound assignment to an associative array
		// whose first word is neither `[key]=value` nor assignment-
		// shaped is a list of alternating keys and values
		// (kvpair_assignment_p); a missing trailing value is empty.
		if len(elems) > 0 && elems[0].Index == nil && !wordLooksLikeAssign(elems[0].Value) {
			var words []string
			for _, elem := range elems {
				if elem.Index != nil {
					// A later [k]=v word in kvpair mode is plain
					// key/value text in bash; reassemble it.
					if w, ok := elem.Index.(*syntax.Word); ok && w != nil {
						words = append(words, "["+r.literal(w)+"]="+r.literalForAssign(elem.Value))
						continue
					}
				}
				words = append(words, r.literalForAssign(elem.Value))
			}
			for i := 0; i < len(words); i += 2 {
				v := ""
				if i+1 < len(words) {
					v = words[i+1]
				}
				if prev.Integer {
					v = r.integerArrayValue(v)
				}
				amap[words[i]] = v
			}
			prev.Kind = expand.Associative
			prev.Map = amap
			prev.AssocBuckets = assocBuckets
			return name, prev
		}
		for _, elem := range elems {
			// `declare -A foo=([k]=v value)` — non-keyed element in an
			// associative-array context with a subscripted first word.
			// Bash 5.3 emits `<file>: line N: foo: <value>: must use
			// subscript when assigning associative array` and stops
			// processing the remaining elements.
			w, ok := elem.Index.(*syntax.Word)
			if !ok || w == nil {
				if elem.Value != nil {
					r.errf("%s%s: %s: must use subscript when assigning associative array\n",
						r.bashErrPrefix(r.curStmtPos), name, r.literalForAssign(elem.Value))
					r.exit.code = 1
				}
				break
			}
			k := r.assocAssignKey(w)
			v := r.literalForAssign(elem.Value)
			if elem.Append {
				if prev.Integer {
					cur, _ := strconv.Atoi(r.integerArrayValue(amap[k]))
					rhs, _ := strconv.Atoi(r.integerArrayValue(v))
					v = strconv.Itoa(cur + rhs)
				} else {
					v = amap[k] + v
				}
			} else if prev.Integer {
				v = r.integerArrayValue(v)
			}
			amap[k] = v
		}
		if !as.Append {
			prev.Kind = expand.Associative
			prev.Map = amap
			prev.AssocBuckets = assocBuckets
			return name, prev
		}
		prev.Map = amap
		prev.AssocBuckets = assocBuckets
		// `declare -A name+=(…)` in a declare-family context records
		// the associative kind so a previously-indexed variable trips
		// the cannot-convert check in setVar, like bash.
		if r.declAssignContext {
			prev.Kind = expand.Associative
		}
		return name, prev
	}
	// Evaluate values for each array element.
	elemValues := make([]struct {
		index  int
		values []string
		append bool // [idx]+=value
	}, len(elems))
	var index, maxIndex int
	// Prev's list grows the working buffer when this is a +=-style
	// outer assignment OR any element uses [idx]+=value (we need to
	// read the previous element's value before appending).
	// Inherit prev.List only for an outer-`+=` assignment. Per-element
	// `[i]+=value` inside a fresh `x=(...)` appends onto whatever the
	// new array has accumulated so far, not onto the previous value
	// (bash 5.3 behavior — confirmed against `x=(1 2 [2]+=7 4 5)`).
	needPrev := as.Append
	// `arr+=( … )` starts implicit indexes at the existing length —
	// bash appends new elements to the tail rather than overlaying
	// position 0. Explicit `[i]=…` still overrides this baseline.
	if as.Append && prev.Kind == expand.Indexed {
		index = len(prev.List)
	}
	nextSet := make(map[int]bool)
	if as.Append && prev.Kind == expand.Indexed {
		nextSet = prev.DenseListSet()
	}
	for i, elem := range elems {
		if elem.Index != nil {
			// Index resets our index with a literal value.
			index = r.arithm(elem.Index)
			if index < 0 {
				// Bash 5.3 treats `arr[-1]=v` as an offset
				// from the end of the existing list.
				index = len(prev.List) + index
				if index < 0 {
					index = 0
				}
			}
			elemValues[i].values = []string{r.literalForAssign(elem.Value)}
			elemValues[i].append = elem.Append
		} else {
			// Implicit index, advancing for every word.
			elemValues[i].values = r.fields(elem.Value)
		}
		elemValues[i].index = index
		index += len(elemValues[i].values)
		maxIndex = max(maxIndex, index)
	}
	// Integer attribute on an array (`typeset -i arr; arr=(1+2 3+4)`)
	// evaluates each element value as an arithmetic expression. Apply
	// the same to `arr=([0]=7+11)` literals and to per-element `+=`
	// appends.
	if prev.Integer {
		arithEval := func(s string) string {
			if s == "" {
				return "0"
			}
			expr, perr := syntax.NewParser().Arithmetic(strings.NewReader(s))
			if perr != nil || expr == nil {
				return "0"
			}
			v, _ := expand.Arithm(r.ecfg, expr)
			return strconv.Itoa(v)
		}
		for i := range elemValues {
			for j, v := range elemValues[i].values {
				elemValues[i].values[j] = arithEval(v)
			}
		}
	}
	if needPrev {
		maxIndex = max(maxIndex, len(prev.List))
	}
	// Flatten down the values.
	strs := make([]string, maxIndex)
	if needPrev {
		copy(strs, prev.List)
	}
	for _, ev := range elemValues {
		for i, str := range ev.values {
			elemIndex := ev.index + i
			if ev.append && i == 0 {
				if prev.Integer {
					// Integer-attribute arrays: `[k]+=N` adds
					// arithmetically rather than concatenating.
					cur, _ := strconv.Atoi(strs[elemIndex])
					rhs, _ := strconv.Atoi(str)
					str = strconv.Itoa(cur + rhs)
				} else {
					str = strs[elemIndex] + str
				}
			}
			strs[elemIndex] = str
			nextSet[elemIndex] = true
		}
	}
	if !as.Append {
		prev.Kind = expand.Indexed
		prev.List = strs
		prev.ListSet = nextSet
		return name, prev
	}
	switch prev.Kind {
	case expand.Unknown:
		prev.Kind = expand.Indexed
		prev.List = strs
		prev.ListSet = nextSet
	case expand.String:
		prev.Kind = expand.Indexed
		// String → Indexed: keep a set prior scalar at index 0 and
		// shift the new elements above it. A declared-but-unset
		// scalar (`declare a`) has no element to preserve.
		if prevWasSet {
			prev.List = append([]string{prev.Str}, strs...)
			shifted := make(map[int]bool, len(nextSet)+1)
			shifted[0] = true
			for i := range nextSet {
				shifted[i+1] = true
			}
			prev.ListSet = shifted
		} else {
			prev.List = strs
			prev.ListSet = nextSet
		}
	case expand.Indexed:
		// strs was sized to include prev.List (needPrev=true) and
		// already contains its values via the initial copy, so we
		// replace prev.List with strs rather than concatenating.
		prev.List = strs
		prev.ListSet = nextSet
	case expand.Associative:
		// TODO
	default:
		// Should only happen if we forgot a case above.
		panic(fmt.Sprintf("unexpected conversion of kind %d", prev.Kind))
	}
	return name, prev
}
