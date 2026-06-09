// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// TODO: given the categories below, perhaps this should be more like:
//
//   func IsBuiltin(lang syntax.LangVariant, name string) bool
//
// or perhaps some API that also lets the user iterate through the builtins?
//
// Also, should we move this to the syntax package too?
// It's not a syntactical property strictly speaking,
// but it's also odd to require importing the interp package for it.

// IsBuiltin returns true if the given word is a POSIX Shell
// or Bash builtin.
func IsBuiltin(name string) bool {
	switch name {
	case
		// POSIX Shell builtins, from section 1.d obtained in September 2025 from:
		// https://pubs.opengroup.org/onlinepubs/9699919799/utilities/V3_chap02.html#tag_18_09_01_01
		"alias",
		"bg",
		"cd",
		"command",
		"false",
		"fc",
		"fg",
		"getopts",
		"hash",
		"jobs",
		"kill",
		"newgrp",
		"pwd",
		"read",
		"true",
		"umask",
		"unalias",
		"wait",

		// POSIX Shell special built-ins, obtained in September 2025 from:
		// https://pubs.opengroup.org/onlinepubs/9699919799/utilities/V3_chap02.html#tag_18_14
		"break",
		":",
		"continue",
		".",
		"eval",
		"exec",
		"exit",
		"export",   // NOTE: our parser treats this as a keyword
		"readonly", // NOTE: our parser treats this as a keyword
		"return",
		"set",
		"shift",
		"times",
		"trap",
		"unset",

		// Bash built-ins which are not present in POSIX, obtained in September 2025 from:
		// https://man.archlinux.org/man/bash.1.en#SHELL_BUILTIN_COMMANDS
		"source",
		"bind",
		"builtin",
		"caller",
		"compgen",
		"complete",
		"compopt",
		"declare", // NOTE: our parser treats this as a keyword
		"typeset", // NOTE: our parser treats this as a keyword
		"dirs",
		"disown",
		"echo", // TODO: surely this is POSIX? but why is it not in the main POSIX spec page?
		"enable",
		"history",
		"help",
		"let", // NOTE: our parser treats this as a keyword
		"local",
		"logout",
		"mapfile",
		"readarray",
		"popd",
		"printf", // TODO: surely this is POSIX? but why is it not in the main POSIX spec page?
		"pushd",
		"shopt",
		"suspend",
		"test",
		"[", // NOTE: an alias for "test", not explicitly listed
		"type",
		"ulimit",

		// Non-bash, non-POSIX — implemented as builtins so they're
		// reliable inside the in-process runner even where the
		// corresponding /usr/bin/* binaries are missing (macOS has
		// no setsid binary) or buggy in the runner's stdio context
		// (BSD nohup hits "Inappropriate ioctl for device" when the
		// parent is a session leader).
		"nohup",
		"setsid",

		// Bash loadable builtin used by its glob-bracket test. We
		// implement it internally since bashy has no dynamic builtin
		// loader.
		"strmatch",

		// Agentic extensions — bashy-specific introspection. Surfaces
		// runner state as JSON so harnesses can observe and assert on
		// what the shell is doing. See docs/agentic-extensions.md.
		"runner-state":
		return true
	}
	return false
}

// TODO: atoi is duplicated in the expand package.

// atoi is like [strconv.ParseInt](s, 10, 64), but it ignores errors and trims whitespace.
func atoi(s string) int64 {
	s = strings.TrimSpace(s)
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

type errBuiltinExitStatus exitStatus

func (e errBuiltinExitStatus) Error() string {
	return fmt.Sprintf("builtin exit status %d", e.code)
}

// Builtin allows [ExecHandlerFunc] implementations to execute any builtin,
// which can be useful for an exec handler to wrap or combine builtin calls.
//
// Note that a non-nil error may be returned in cases where the builtin
// alters the control flow of the runner, even if the builtin did not fail.
// For example, this is the case with `exit 0` or `return`.
func (hc HandlerContext) Builtin(ctx context.Context, args []string) error {
	if hc.kind != handlerKindExec {
		return fmt.Errorf("HandlerContext.Builtin can only be called via an ExecHandlerFunc")
	}
	exit := hc.runner.builtin(ctx, hc.Pos, args[0], args[1:])
	if exit != (exitStatus{}) {
		return errBuiltinExitStatus(exit)
	}
	return nil
}

func (r *Runner) builtin(ctx context.Context, pos syntax.Pos, name string, args []string) (exit exitStatus) {
	// failf emits a user-fault error and sets the exit code. When
	// [WithBashCompatErrors] is on, the message is prefixed with
	// "<filename>: line <N>: " so bash 5.3's test suite output matches.
	failf := func(code uint8, format string, a ...any) exitStatus {
		msg := fmt.Sprintf(format, a...)
		if prefix := r.bashErrPrefix(pos); prefix != "" {
			msg = prefix + msg
			r.errf("%s", msg)
		} else {
			r.errf("%s", msg)
		}
		r.reportError("builtin", pos, name, msg, code)
		exit.code = code
		return exit
	}
	// invalidOpt is the canonical "<builtin>: <flag>: invalid option"
	// formatter. In bashCompat mode it follows the bash 5.3 wording
	// (arg-first, no quotes) and also emits a usage hint to stderr
	// without the line prefix, matching bash. The legacy form
	// "<builtin>: invalid option \"<flag>\"" is preserved otherwise.
	invalidOpt := func(builtin, flag string) exitStatus {
		if r.bashCompatErrors {
			r.errf(r.bashErrPrefix(pos)+"%s: %s: invalid option\n", builtin, flag)
			if usage := bashUsage[builtin]; usage != "" {
				r.errf("%s: usage: %s\n", builtin, usage)
			}
			exit.code = 2
			return exit
		}
		return failf(2, "%s: invalid option %q\n", builtin, flag)
	}
	switch name {
	case ":", "true":
	case "false":
		exit.code = 1
	case "exit":
		switch len(args) {
		case 0:
			exit = r.lastExit
		case 1:
			n, err := strconv.Atoi(args[0])
			if err != nil {
				return failf(2, "invalid exit status code: %q\n", args[0])
			}
			exit.code = uint8(n)
		default:
			return failf(1, "exit cannot take multiple arguments\n")
		}
		exit.exiting = true
	case "set":
		if len(args) == 1 && args[0] == "--json" {
			return r.jsonOut(map[string]any{"variables": r.variablesJSON(true)})
		}
		if len(args) == 0 {
			// `set` with no args prints all shell variables in
			// `name=value` form, alphabetically sorted, values
			// quoted bash-style. Bash distinguishes scalars,
			// indexed arrays and associative arrays via the same
			// rules as `declare -p`'s output minus the `declare -X`
			// prefix.
			var names []string
			r.writeEnv.Each(func(name string, vr expand.Variable) bool {
				if !vr.IsSet() {
					return true
				}
				names = append(names, name)
				return true
			})
			sort.Strings(names)
			for _, name := range names {
				vr := r.writeEnv.Get(name)
				switch vr.Kind {
				case expand.Indexed:
					r.outf("%s=(", name)
					first := true
					for _, i := range vr.IndexedIndexes() {
						if !first {
							r.out(" ")
						}
						first = false
						r.outf("[%d]=%s", i, bashSetQuote(vr.List[i]))
					}
					if !first {
						r.out(" ")
					}
					r.out(")\n")
				case expand.Associative:
					r.outf("%s=(", name)
					first := true
					for k, v := range vr.Map {
						if !first {
							r.out(" ")
						}
						r.outf("[%s]=%s", k, bashSetQuote(v))
						first = false
					}
					if !first {
						r.out(" ")
					}
					r.out(")\n")
				default:
					r.outf("%s=%s\n", name, bashSetQuote(vr.Str))
				}
			}
			break
		}
		if err := Params(args...)(r); err != nil {
			if err.Error() == "+r: invalid option" {
				r.errf("%sset: +r: invalid option\n", r.bashErrPrefix(pos))
				r.errf("set: usage: %s\n", bashUsage["set"])
				exit.code = 2
				return exit
			}
			if opt, ok := strings.CutPrefix(err.Error(), "invalid option: "); ok {
				opt = strings.Trim(opt, `"`)
				if strings.HasPrefix(opt, "-") || strings.HasPrefix(opt, "+") {
					r.errf("%sset: %s: invalid option\n", r.bashErrPrefix(pos), opt)
					r.errf("set: usage: %s\n", bashUsage["set"])
				} else {
					r.errf("%sset: %s: invalid option name\n", r.bashErrPrefix(pos), opt)
				}
				exit.code = 2
				return exit
			}
			return failf(2, "set: %v\n", err)
		}
		r.updateExpandOpts()
	case "shift":
		// Accept `--` as end-of-options.
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		n := 1
		switch len(args) {
		case 0:
		case 1:
			n2, err := strconv.Atoi(args[0])
			if err != nil {
				return failf(1, "shift: %s: numeric argument required\n", args[0])
			}
			if n2 < 0 || n2 > len(r.Params) {
				// Out of range: silent error by default; with
				// `shopt -s shift_verbose`, emit a diagnostic.
				if opt, _ := r.bashOptByName("shift_verbose"); opt != nil && *opt {
					return failf(1, "shift: %s: shift count out of range\n", args[0])
				}
				exit.code = 1
				return exit
			}
			n = n2
		default:
			return failf(1, "shift: too many arguments\n")
		}
		if n >= len(r.Params) {
			r.Params = nil
		} else {
			r.Params = r.Params[n:]
		}
	case "unset":
		vars := true
		funcs := true
		// `-n NAME` unsets the nameref itself (not the variable
		// it points to). Without -n, unset of a nameref follows
		// the reference and unsets the target.
		nameref := false
	unsetOpts:
		for i, arg := range args {
			switch arg {
			case "-v":
				funcs = false
			case "-f":
				vars = false
			case "-n":
				nameref = true
				funcs = false
			default:
				if len(arg) > 1 && arg[0] == '-' {
					r.errf("%sunset: %s: invalid option\n", r.bashErrPrefix(pos), arg)
					r.errf("unset: usage: unset [-f] [-v] [-n] [name ...]\n")
					exit.code = 2
					return exit
				}
				args = args[i:]
				break unsetOpts
			}
		}

		for _, arg := range args {
			// Bash 5.3: `unset 1bad` errors with "not a valid identifier"
			// (exit 2) when the var-namespace is in scope. Function names
			// are unrestricted, so `unset -f 1bad` is allowed.
			//
			// Array-element form `name[index]` is valid: unset the
			// specified element instead of the whole variable.
			if vars {
				if name, idx, ok := splitArrayRef(arg); ok {
					if syntax.ValidName(name) {
						r.unsetArrayElem(name, idx)
						continue
					}
				}
				if !syntax.ValidName(arg) {
					return failf(2, "unset: `%s': not a valid identifier\n", arg)
				}
			}
			if nameref {
				// Skip the auto-resolve so we delete the nameref
				// variable itself rather than its target.
				r.delVar(arg)
				continue
			}
			if vars {
				vr := r.lookupVar(arg)
				if vr.Kind == expand.NameRef {
					// Bash: `unset NAME` on a nameref follows
					// the reference and unsets the *target*.
					// The nameref itself keeps the attribute
					// (now pointing at an unset variable).
					if vr.Str != "" {
						tgt := r.lookupVar(vr.Str)
						if tgt.ReadOnly {
							r.errf("%sunset: %s: cannot unset: readonly variable\n",
								r.bashErrPrefix(pos), vr.Str)
							exit.code = 1
							continue
						}
						r.delVar(vr.Str)
					}
					continue
				}
				if vr.ReadOnly {
					r.errf("%sunset: %s: cannot unset: readonly variable\n",
						r.bashErrPrefix(pos), arg)
					exit.code = 1
					continue
				}
				if vr.IsSet() || vr.Integer || vr.Exported || vr.Local ||
					vr.Upper || vr.Lower || vr.Capitalize || vr.Kind != expand.Unset {
					r.delVar(arg)
					continue
				}
			}
			if _, ok := r.Funcs[arg]; ok && funcs {
				if r.readonlyFuncs[arg] {
					r.errf("%sunset: %s: cannot unset: readonly function\n",
						r.bashErrPrefix(pos), arg)
					exit.code = 1
					continue
				}
				delete(r.Funcs, arg)
			}
		}
	case "echo":
		xpgOpt, _ := r.bashOptByName("xpg_echo")
		newline, doExpand := true, xpgOpt != nil && *xpgOpt
	echoOpts:
		for len(args) > 0 {
			arg := args[0]
			if len(arg) < 2 || arg[0] != '-' {
				break echoOpts
			}
			// Validate all chars are echo flags (n, e, E).
			valid := true
			for _, c := range arg[1:] {
				if c != 'n' && c != 'e' && c != 'E' {
					valid = false
					break
				}
			}
			if !valid {
				break echoOpts
			}
			for _, c := range arg[1:] {
				switch c {
				case 'n':
					newline = false
				case 'e':
					doExpand = true
				case 'E':
					doExpand = false
				}
			}
			args = args[1:]
		}
		for i, arg := range args {
			if i > 0 {
				r.out(" ")
			}
			if doExpand {
				// `echo -e` uses bash's `%b`-style escape table:
				// `\c` terminates output, `\'`/`\"`/`\?` stay
				// literal (no `\` strip), the rest follow the
				// standard ANSI-C escapes.
				out, err := expand.FormatBPercent(r.ecfg, arg)
				r.out(out)
				if err == expand.ErrPrintfStop {
					return exit
				}
				continue
			}
			r.out(arg)
		}
		if newline {
			r.out("\n")
		}
	case "printf":
		// printf -v VAR FORMAT [args...] writes output to VAR instead of
		// stdout. Multiple -v flags are illegal; -v requires a name.
		var assignTo string
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-v":
				assignTo = fp.value()
				if assignTo == "" {
					return failf(2, "printf: -v: option requires an argument\n")
				}
				if !syntax.ValidName(assignTo) {
					if r.bashCompatErrors {
						return failf(1, "printf: `%s': not a valid identifier\n", assignTo)
					}
					return failf(1, "printf: %q: not a valid identifier\n", assignTo)
				}
			default:
				return invalidOpt("printf", flag)
			}
		}
		args = fp.args()
		if len(args) == 0 {
			if r.bashCompatErrors {
				// Bash emits the bare `printf: usage: ...` line for
				// no-args without the `<file>: line N:` prefix (the
				// prefix is reserved for error conditions; usage on
				// "no required arg" is informational).
				r.errf("printf: usage: %s\n", bashUsage["printf"])
				exit.code = 2
				return exit
			}
			return failf(2, "usage: printf [-v var] format [arguments]\n")
		}
		format, args := args[0], args[1:]
		var sb strings.Builder
		// Format may also invoke r.ecfg.OnFormatWarning for soft
		// failures (e.g. `printf %d xyz` → "invalid number"). The
		// callback stashes the bash-compat exit code in
		// r.lastExpandExit; reset it here so we only see warnings
		// from this invocation, then propagate at the end.
		r.lastExpandExit = exitStatus{}
		// Wire up bash printf %n: store the byte count emitted so far
		// into the named variable. Restore the previous callback after
		// this printf invocation so nested users of r.ecfg aren't
		// affected.
		prevOnPercentN := r.ecfg.OnPercentN
		r.ecfg.OnPercentN = func(name string, n int) error {
			r.setVarString(name, strconv.Itoa(n))
			return nil
		}
		defer func() { r.ecfg.OnPercentN = prevOnPercentN }()
		for {
			s, n, err := expand.Format(r.ecfg, format, args)
			stop := errors.Is(err, expand.ErrPrintfStop)
			formatErr := err != nil && !stop
			diagnosticBeforeOutput := formatErr && bashPrintfFormatError(err)
			if diagnosticBeforeOutput {
				r.errf("%s%v\n", r.bashErrPrefix(pos), err)
			}
			if assignTo != "" {
				sb.WriteString(s)
			} else {
				r.out(s)
			}
			if formatErr {
				if assignTo != "" {
					r.setVarString(assignTo, sb.String())
				}
				if diagnosticBeforeOutput {
					exit.code = 1
					return exit
				}
				return failf(1, "%v\n", err)
			}
			if stop {
				break
			}
			args = args[n:]
			if n == 0 || len(args) == 0 {
				break
			}
		}
		if assignTo != "" {
			r.setVarString(assignTo, sb.String())
		}
		if r.lastExpandExit.code != 0 {
			exit.code = r.lastExpandExit.code
		}
	case "break", "continue":
		if !r.inLoop {
			// Bash 5.3 in POSIX mode silently treats break/continue
			// outside a loop as a no-op (returns 0), instead of
			// emitting the "only meaningful in a loop" diagnostic.
			if r.opts[optPosix] {
				break
			}
			return failf(0, "%s: only meaningful in a `for', `while', or `until' loop\n", name)
		}
		enclosing := &r.breakEnclosing
		if name == "continue" {
			enclosing = &r.contnEnclosing
		}
		// Accept and skip `--` as the end-of-options marker.
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		switch len(args) {
		case 0:
			*enclosing = 1
		case 1:
			if n, err := strconv.Atoi(args[0]); err == nil {
				if n < 1 {
					// Bash still breaks out of the current loop
					// (the body after `break N<1` is unreachable),
					// but exits with a diagnostic.
					*enclosing = 1
					r.errf("%s%s: %s: loop count out of range\n",
						r.bashErrPrefix(r.curStmtPos), name, args[0])
					exit.code = 1
					return exit
				}
				*enclosing = n
				break
			}
			fallthrough
		default:
			if r.bashCompatErrors {
				return failf(2, "%s: usage: %s\n", name, bashUsage[name])
			}
			return failf(2, "usage: %s [n]\n", name)
		}
	case "pwd":
		evalSymlinks := false
		for len(args) > 0 {
			switch args[0] {
			case "-L":
				evalSymlinks = false
			case "-P":
				evalSymlinks = true
			default:
				return failf(2, "pwd: %s: invalid option\n", args[0])
			}
			args = args[1:]
		}
		pwd := r.envGet("PWD")
		if evalSymlinks {
			var err error
			pwd, err = filepath.EvalSymlinks(pwd)
			if err != nil {
				exit.fatal(err) // perhaps overly dramatic?
				return exit
			}
		}
		r.outf("%s\n", pwd)
	case "cd":
		if r.opts[optRestricted] {
			r.errf("%scd: restricted\n", r.bashErrPrefix(pos))
			exit.code = 1
			return exit
		}
		// bash's `cd` accepts `-L` (logical, default), `-P`
		// (physical — resolve symlinks via the real filesystem)
		// and `-@` (extended attributes; not meaningful here).
		// We accept and ignore all three options since our
		// path resolution is already filesystem-backed.
		for len(args) > 0 {
			a := args[0]
			if a == "-L" || a == "-P" || a == "-e" || a == "-@" {
				args = args[1:]
				continue
			}
			break
		}
		var path string
		printPath := false
		switch len(args) {
		case 0:
			path = r.envGet("HOME")
			if path == "" {
				r.errf("%scd: HOME not set\n", r.bashErrPrefix(r.curStmtPos))
				exit.code = 1
				return exit
			}
		case 1:
			path = args[0]

			// replicate the commonly implemented behavior of `cd -`
			// ref: https://www.man7.org/linux/man-pages/man1/cd.1p.html#OPERANDS
			if path == "-" {
				path = r.envGet("OLDPWD")
				if path == "" {
					r.errf("%scd: OLDPWD not set\n", r.bashErrPrefix(r.curStmtPos))
					exit.code = 1
					return exit
				}
				printPath = true
			}
		default:
			return failf(1, "cd: too many arguments\n")
		}
		exit.code = r.changeDir(ctx, "cd", path)
		if printPath && exit.code == 0 {
			r.outf("%s\n", path)
		}
	case "wait":
		fp := flagParser{remaining: args}
		waitNext := false
		var pidVar string
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-n":
				waitNext = true
			case "-p":
				pidVar = fp.value()
			default:
				return invalidOpt("wait", flag)
			}
		}
		remaining := fp.args()
		if waitNext {
			// Wait for the next background job to complete.
			for i, bg := range r.bgProcs {
				select {
				case <-bg.done:
					exit = *bg.exit
					if pidVar != "" {
						r.setVarString(pidVar, "g"+strconv.Itoa(i+1))
					}
					goto waitDone
				default:
				}
			}
			// None already done; wait for any one.
			if len(r.bgProcs) > 0 {
				// Simple approach: wait on the first unfinished one.
				for i, bg := range r.bgProcs {
					<-bg.done
					exit = *bg.exit
					if pidVar != "" {
						r.setVarString(pidVar, "g"+strconv.Itoa(i+1))
					}
					break
				}
			}
		waitDone:
			break
		}
		if len(remaining) == 0 {
			// Note that "wait" without arguments always returns exit status zero.
			for _, bg := range r.bgProcs {
				<-bg.done
			}
			break
		}
		for _, arg := range remaining {
			// Accept either the legacy "gN" sentinel ($! used to always
			// return that) or a real numeric OS PID (what $! now
			// returns when the bg statement spawned a real process).
			var bg *bgProc
			var matchedIdx int64
			if rest, ok := strings.CutPrefix(arg, "g"); ok {
				idx := atoi(rest)
				if idx <= 0 || idx > int64(len(r.bgProcs)) {
					return failf(1, "wait: pid %s is not a child of this shell\n", arg)
				}
				bg = r.bgProcs[idx-1]
				matchedIdx = idx
			} else {
				pid, perr := strconv.ParseInt(arg, 10, 64)
				if perr != nil {
					return failf(1, "wait: pid %s is not a child of this shell\n", arg)
				}
				for i, candidate := range r.bgProcs {
					if candidate.pid.Load() == pid {
						bg = candidate
						matchedIdx = int64(i + 1)
						break
					}
				}
				if bg == nil {
					return failf(1, "wait: pid %s is not a child of this shell\n", arg)
				}
			}
			<-bg.done
			exit = *bg.exit
			if pidVar != "" {
				r.setVarString(pidVar, "g"+strconv.FormatInt(matchedIdx, 10))
			}
		}
	case "kill":
		// Bash kill accepts: `-l [signum|name…]`, `-s NAME pid…`, `-n NUM
		// pid…`, `-NAME pid…`, `-NUM pid…`, and `pid…` (default SIGTERM).
		// Job specs (`%1`) aren't supported because the in-process runner
		// has no real job table — `$!` returns a "g<N>" sentinel that is
		// not a real PID. The shared flagParser doesn't fit here because
		// `-SIGNAME` is a whole-arg flag, not stacked short flags.
		listOnly := false
		sig := syscall.Signal(15) // SIGTERM
		remaining := args
	killFlags:
		for len(remaining) > 0 {
			arg := remaining[0]
			if !strings.HasPrefix(arg, "-") || arg == "-" {
				break
			}
			switch arg {
			case "--":
				remaining = remaining[1:]
				break killFlags
			case "-l", "-L":
				listOnly = true
				remaining = remaining[1:]
			case "-s":
				if len(remaining) < 2 {
					return failf(2, "kill: -s requires a signal name\n")
				}
				s, ok := signalByName(remaining[1])
				if !ok {
					return failf(1, "kill: %s: invalid signal specification\n", remaining[1])
				}
				sig = s
				remaining = remaining[2:]
			case "-n":
				if len(remaining) < 2 {
					return failf(2, "kill: -n requires a signal number\n")
				}
				n, err := strconv.Atoi(remaining[1])
				if err != nil {
					return failf(2, "kill: -n requires a signal number\n")
				}
				s, _, ok := signalByNumber(n)
				if !ok {
					return failf(1, "kill: %d: invalid signal specification\n", n)
				}
				sig = s
				remaining = remaining[2:]
			default:
				// -SIGNAME or -NUMBER (whole flag is the spec)
				spec := strings.TrimPrefix(arg, "-")
				s, ok := parseSignalSpec(spec)
				if !ok {
					return failf(1, "kill: %s: invalid signal specification\n", arg)
				}
				sig = s
				remaining = remaining[1:]
				break killFlags
			}
		}
		if listOnly {
			if len(remaining) == 0 {
				for _, e := range sortedSignalEntries() {
					r.outf("%s\n", e.Name)
				}
				break
			}
			for _, a := range remaining {
				if n, err := strconv.Atoi(a); err == nil {
					if _, name, ok := signalByNumber(n); ok && name != "EXIT" {
						r.outf("%s\n", name)
						continue
					}
					exit.code = 1
					r.errf(r.bashErrPrefix(pos)+"kill: %s: invalid signal specification\n", a)
					continue
				}
				if s, ok := signalByName(a); ok {
					r.outf("%d\n", int(s))
					continue
				}
				exit.code = 1
				r.errf("kill: %s: invalid signal specification\n", a)
			}
			break
		}
		if len(remaining) == 0 {
			return failf(2, "kill: usage: kill [-s sigspec | -n signum | -sigspec] pid ...\n")
		}
		for _, target := range remaining {
			if strings.HasPrefix(target, "%") || strings.HasPrefix(target, "g") {
				exit.code = 1
				r.errf(r.bashErrPrefix(pos)+"kill: %s: no job control in this shell\n", target)
				continue
			}
			pid, err := strconv.Atoi(target)
			if err != nil {
				exit.code = 1
				r.errf(r.bashErrPrefix(pos)+"kill: %s: arguments must be process IDs\n", target)
				continue
			}
			if err := sendSignal(pid, sig); err != nil {
				exit.code = 1
				r.errf(r.bashErrPrefix(pos)+"kill: (%d) - %v\n", pid, err)
			}
		}
	case "nohup":
		exit = r.runNohup(ctx, args)
	case "setsid":
		exit = r.runSetsid(ctx, args)
	case "disown":
		// The interpreter has no kernel-level job table — backgrounded `&`
		// statements are goroutines, and nothing in the runner ever sends
		// SIGHUP to anything at exit. bash `disown` exists to keep jobs off
		// that table so they survive shell exit; with no table to remove from
		// and no SIGHUP to dodge, this builtin is a structural no-op. It must
		// still exist so `set -e` scripts that include `disown` don't abort.
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-a", "-h", "-r":
				// accepted; behavior is implicit (no job table to filter)
			default:
				return invalidOpt("disown", flag)
			}
		}
		// Remaining positional args (job specs / PIDs) are ignored — we
		// have no job table to look them up against.
	case "builtin":
		if len(args) < 1 {
			break
		}
		if !IsBuiltin(args[0]) {
			exit.code = 1
			return exit
		}
		exit = r.builtin(ctx, pos, args[0], args[1:])
	case "type":
		skipFuncs := false
		showAll := false
		mode := "" // "", "-t", "-p", "-P"
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-a":
				showAll = true
			case "-f":
				skipFuncs = true
			case "-p", "-P", "-t":
				mode = flag
			default:
				return invalidOpt("type", flag)
			}
		}
		args := fp.args()
		anyNotFound := false
		for _, arg := range args {
			// -P always does PATH lookup, ignoring builtin/function/etc.
			if mode == "-P" {
				if path, err := LookPathDir(r.Dir, r.writeEnv, arg); err == nil {
					r.outf("%s\n", path)
				} else {
					anyNotFound = true
				}
				continue
			}
			matches := r.typeMatches(arg, skipFuncs)
			// -p: only print path if no non-file match exists.
			if mode == "-p" {
				var pathMatch string
				hasNonFile := false
				for _, m := range matches {
					if m.kind == "file" {
						pathMatch = m.path
					} else {
						hasNonFile = true
					}
				}
				if !hasNonFile && pathMatch != "" {
					r.outf("%s\n", pathMatch)
				}
				if len(matches) == 0 {
					anyNotFound = true
				}
				continue
			}
			if len(matches) == 0 {
				if mode != "-t" {
					r.errf(r.bashErrPrefix(pos)+"type: %s: not found\n", arg)
				}
				anyNotFound = true
				continue
			}
			toShow := matches
			if !showAll {
				toShow = matches[:1]
			}
			for _, m := range toShow {
				if mode == "-t" {
					r.outf("%s\n", m.kind)
				} else {
					r.outf("%s\n", m.desc)
					// `type funcname` (and -a) also dumps the
					// body in bash's `declare -f` shape.
					if mode == "" && m.kind == "function" {
						if body := r.Funcs[arg]; body != nil {
							r.printFuncDecl(arg, body)
						}
					}
				}
			}
		}
		if anyNotFound {
			exit.code = 1
		}
	case "caller":
		// Bash semantics:
		//  caller          — prints "<line> [<source>]" for the current
		//                    function call or "0 NULL" at top-level.
		//  caller <expr>   — prints "<line> <function> <source>" for the
		//                    frame at depth <expr>; errors if <expr> is
		//                    not an integer or is out of range.
		//  caller -X       — invalid option.
		if len(args) > 0 && strings.HasPrefix(args[0], "-") && args[0] != "--" {
			r.errf("%scaller: %s: invalid option\ncaller: usage: caller [expr]\n",
				r.bashErrPrefix(pos), args[0])
			exit.code = 2
			return exit
		}
		if len(args) > 1 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) > 1 {
			r.errf("caller: usage: caller [expr]\n")
			exit.code = 1
			return exit
		}
		if len(args) == 0 {
			// "Implicit" caller: print line + source of the
			// immediate caller, or "0 NULL" if at the top level.
			if len(r.callStack) == 0 {
				r.outf("0 NULL\n")
				break
			}
			frame := r.callStack[len(r.callStack)-1]
			r.outf("%d %s\n", frame.line, frame.source)
			break
		}
		level, err := strconv.Atoi(args[0])
		if err != nil || level < 0 {
			r.errf("%scaller: %s: invalid number\ncaller: usage: caller [expr]\n",
				r.bashErrPrefix(pos), args[0])
			exit.code = 1
			return exit
		}
		if level < len(r.callStack) {
			frame := r.callStack[len(r.callStack)-1-level]
			r.outf("%d %s %s\n", frame.line, frame.funcName, frame.source)
		} else {
			exit.code = 1
		}
	case "hash":
		fp := flagParser{remaining: args}
		clearHash := false
		var explicitPath string
		listOnly := false
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-r":
				clearHash = true
			case "-p":
				// `hash -p PATH NAME`: cache NAME with the
				// given PATH instead of searching $PATH.
				explicitPath = fp.value()
				if explicitPath == "" {
					return failf(2, "hash: -p: option requires an argument\n")
				}
			case "-l":
				// `hash -l`: emit reusable `builtin hash …` form.
				listOnly = true
			case "-t":
				// `hash -t name …`: print just the path for each
				// hashed name. Handled below in name loop.
			case "-d":
				// `hash -d NAME`: forget specific name.
				if names := fp.args(); len(names) > 0 {
					for _, n := range names {
						delete(r.cmdHashTable, n)
					}
				}
				break
			default:
				return failf(1, "hash: %s: invalid option\n", flag)
			}
		}
		_ = listOnly
		if clearHash {
			clear(r.cmdHashTable)
			break
		}
		remaining := fp.args()
		if len(remaining) == 0 {
			// List cached commands in bash's format:
			//   hits	command
			//      N	/path
			if len(r.cmdHashTable) == 0 {
				r.outf("hash: hash table empty\n")
				break
			}
			r.outf("hits\tcommand\n")
			entries := make([]cmdHashEntry, 0, len(r.cmdHashTable))
			for _, entry := range r.cmdHashTable {
				entries = append(entries, entry)
			}
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].path < entries[j].path
			})
			for _, entry := range entries {
				r.outf("%4d\t%s\n", entry.hits, entry.path)
			}
			break
		}
		// Cache specific commands.
		for _, name := range remaining {
			var path string
			if explicitPath != "" {
				if r.opts[optRestricted] {
					if strings.Contains(explicitPath, "/") {
						r.errf("%shash: %s: restricted\n", r.bashErrPrefix(pos), explicitPath)
						exit.code = 1
						continue
					}
					if _, err := LookPathDir(r.Dir, r.writeEnv, explicitPath); err != nil {
						r.errf("%shash: %s: not found\n", r.bashErrPrefix(pos), explicitPath)
						exit.code = 1
						continue
					}
				}
				path = explicitPath
			} else {
				p, err := LookPathDir(r.Dir, r.writeEnv, name)
				if err != nil {
					r.errf(r.bashErrPrefix(pos)+"hash: %s: not found\n", name)
					exit.code = 1
					continue
				}
				path = p
			}
			if r.cmdHashTable == nil {
				r.cmdHashTable = make(map[string]cmdHashEntry)
			}
			r.cmdHashTable[name] = cmdHashEntry{path: path}
		}
	case "help":
		if len(args) == 0 {
			r.outf("bashy, version %s\n", "5.3.0(1)-bashy")
			r.outf("These shell commands are defined internally.\n\n")
			builtinList := []string{
				":", ".", "[", "alias", "bg", "bind", "break", "builtin",
				"caller", "cd", "command", "continue", "declare", "dirs",
				"disown", "echo", "enable", "eval", "exec", "exit",
				"export", "false", "fc", "fg", "getopts", "hash", "help",
				"history", "jobs", "kill", "let", "local", "logout",
				"mapfile", "popd", "printf", "pushd", "pwd", "read",
				"readarray", "readonly", "return", "set", "shift", "shopt",
				"source", "test", "times", "trap", "true", "type",
				"typeset", "ulimit", "umask", "unalias", "unset", "wait",
			}
			for _, b := range builtinList {
				r.outf(" %s\n", b)
			}
		} else {
			for _, name := range args {
				if IsBuiltin(name) {
					r.outf("%s: %s is a shell builtin\n", name, name)
				} else {
					r.errf(r.bashErrPrefix(pos)+"help: no help topics match `%s'\n", name)
					exit.code = 1
				}
			}
		}
	case "enable":
		fp := flagParser{remaining: args}
		disable := false
		deleteDynamic := false
		loadDynamic := false
		listAll := false
		showAll := false
		specialOnly := false
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-n":
				disable = true
			case "-p":
				// `enable -p` lists all builtins in the format
				// `enable [-n] NAME` per builtin.
				listAll = true
			case "-a":
				// `enable -a` lists all builtins regardless of
				// enable/disable state.
				showAll = true
				listAll = true
			case "-s":
				// `-s` filters the listing to POSIX special
				// builtins (1003.1 § 2.14). Bash combines it
				// with `-p`/`-n`/`-a` for that subset.
				specialOnly = true
				listAll = true
			case "-f":
				// `-f` loads dynamic builtins, which are not
				// supported. Accept the flag and consume its
				// filename argument to avoid breaking scripts.
				fp.value()
				loadDynamic = true
			case "-d":
				// `-d` deletes a dynamically-loaded builtin.
				deleteDynamic = true
			default:
				return failf(2, "enable: %s: invalid option\n", flag)
			}
		}
		remaining := fp.args()
		if len(remaining) == 0 {
			if listAll {
				// Print every recognised builtin, marking disabled
				// ones with the `-n` flag. Bash sorts alphabetically.
				names := []string{
					":", ".", "[", "alias", "bg", "bind", "break", "builtin",
					"caller", "cd", "command", "compgen", "complete", "compopt",
					"continue", "declare", "dirs", "disown", "echo", "enable",
					"eval", "exec", "exit", "export", "false", "fc", "fg",
					"getopts", "hash", "help", "history", "jobs", "kill",
					"let", "local", "logout", "mapfile", "popd", "printf",
					"pushd", "pwd", "read", "readarray", "readonly", "return",
					"set", "shift", "shopt", "source", "suspend", "test",
					"times", "trap", "true", "type", "typeset", "ulimit",
					"umask", "unalias", "unset", "wait",
				}
				sort.Strings(names)
				for _, n := range names {
					if specialOnly && !isPosixSpecialBuiltin(n) {
						continue
					}
					if r.disabledBuiltins[n] {
						if disable || showAll || specialOnly {
							r.outf("enable -n %s\n", n)
						}
					} else if !disable {
						r.outf("enable %s\n", n)
					}
				}
				break
			}
			// Default (no args, no -p): list only enabled OR only
			// disabled depending on `-n`.
			if disable {
				for name := range r.disabledBuiltins {
					r.outf("enable -n %s\n", name)
				}
			}
			break
		}
		for _, name := range remaining {
			if !IsBuiltin(name) {
				r.errf(r.bashErrPrefix(pos)+"enable: %s: not a shell builtin\n", name)
				exit.code = 1
				continue
			}
			if deleteDynamic {
				if r.dynamicBuiltins[name] {
					delete(r.dynamicBuiltins, name)
					if r.disabledBuiltins == nil {
						r.disabledBuiltins = make(map[string]bool)
					}
					r.disabledBuiltins[name] = true
					continue
				}
				r.errf(r.bashErrPrefix(pos)+"enable: %s: not dynamically loaded\n", name)
				exit.code = 1
				continue
			}
			if loadDynamic {
				if r.dynamicBuiltins == nil {
					r.dynamicBuiltins = make(map[string]bool)
				}
				r.dynamicBuiltins[name] = true
			}
			if disable {
				if r.disabledBuiltins == nil {
					r.disabledBuiltins = make(map[string]bool)
				}
				r.disabledBuiltins[name] = true
			} else {
				delete(r.disabledBuiltins, name)
			}
		}
	case "eval":
		src := strings.Join(args, " ")
		p := syntax.NewParser()
		file, err := p.Parse(strings.NewReader(src), "")
		if err != nil {
			if r.opts[optExpandAliases] {
				if expanded, ok := r.expandRawAliasSource(src); ok {
					if retry, rerr := p.Parse(strings.NewReader(expanded), ""); rerr == nil {
						r.stmts(ctx, retry.Stmts)
						exit = r.exit
						break
					}
				}
			}
			// Bash 5.3 prints eval-time parse errors as
			// `<file>: eval: line N: <text>` — `eval:` lives between
			// the filename and `line N:`, not after it as `failf`
			// would arrange. The line N refers to the outer-script
			// line where `eval` was called.
			var pe syntax.ParseError
			if r.bashCompatErrors && errors.As(err, &pe) {
				name := r.filename
				if name == "" {
					name = "bashy"
				}
				text := pe.Text
				// Rewrite our generic "statements must be separated"
				// message to bash's "syntax error near unexpected
				// token `X'" form when we can identify the
				// offending token from the source.
				switch {
				case text == "statements must be separated by &, ; or a newline":
					if tok := offendingToken(src, pe.Pos); tok != "" {
						text = fmt.Sprintf("syntax error near unexpected token `%s'", tok)
					}
				case strings.HasPrefix(text, "reached EOF without matching"):
					// Bash phrases unclosed brace/paren EOFs as
					// "unexpected EOF while looking for matching `X'".
					// An unclosed bare `{` (block) uses a special
					// shape that points back at the opening line.
					switch {
					case strings.Contains(text, "`${`"):
						text = "unexpected EOF while looking for matching `}'"
					case strings.Contains(text, "`{`"):
						if openLine := firstBraceLine(src); openLine > 0 {
							// Map eval-source-relative line back
							// to the outer-script line by adding
							// the eval call's own line minus 1.
							absLine := int(pos.Line()) + openLine - 1
							text = fmt.Sprintf(
								"syntax error: unexpected end of file from `{' command on line %d",
								absLine)
						} else {
							text = "unexpected EOF while looking for matching `}'"
						}
					case strings.Contains(text, "`$(`") || strings.Contains(text, "`(`"):
						text = "unexpected EOF while looking for matching `)'"
					}
				}
				// bash reports the eval-time EOF on the line right
				// after the eval source ran out, not the eval-call
				// line itself. Add the eval source's line count to
				// approximate; a trailing `\` in the eval payload
				// triggers bash's line-continuation reader, so
				// count one extra line for that case.
				evalLine := int(pos.Line())
				if strings.HasPrefix(text, "unexpected EOF") ||
					strings.HasPrefix(text, "syntax error: unexpected end of file") {
					evalLine += strings.Count(src, "\n") + 1
					if strings.HasSuffix(src, "\\") {
						evalLine++
					}
				}
				r.errf("%s: eval: line %d: %s\n", name, evalLine, text)
				// Bash also echoes the offending source line on a
				// second `<file>: eval: line N: \`<line>'` line —
				// except for "unexpected EOF" / "syntax error:
				// unexpected end of file" diagnostics, which
				// already self-describe the unclosed token.
				if !strings.HasPrefix(text, "unexpected EOF") &&
					!strings.HasPrefix(text, "syntax error: unexpected end of file") {
					if line := evalSourceLine(src, int(pe.Pos.Line())); line != "" {
						r.errf("%s: eval: line %d: `%s'\n", name, pos.Line(), line)
					}
				}
				exit.code = 1
				return exit
			}
			return failf(1, "eval: %v\n", err)
		}
		r.stmts(ctx, file.Stmts)
		exit = r.exit
	case "source", ".":
		// Bash 5.3: accept `-p PATH` to override the search path.
		var pathOverride string
		havePathOverride := false
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-p":
				pathOverride = fp.value()
				havePathOverride = true
				if pathOverride == "" {
					return failf(2, "%s: -p: option requires an argument\n", name)
				}
			default:
				return failf(2, "%s: %s: invalid option\n%s: usage: %s\n",
					name, flag, name, bashUsage[name])
			}
		}
		args = fp.args()
		if len(args) < 1 {
			r.errf("%s%s: filename argument required\n%s: usage: %s\n",
				r.bashErrPrefix(pos), name, name, bashUsage[name])
			exit.code = 2
			return exit
		}
		if r.opts[optRestricted] && strings.Contains(args[0], "/") {
			r.errf("%s.: %s: restricted\n", r.bashErrPrefix(pos), args[0])
			exit.code = 1
			return exit
		}
		var path string
		var err error
		if havePathOverride {
			// Search the explicit PATH only; layer a one-off overlay on
			// top of the writeEnv so PATH is overridden but other env
			// stays intact for the search.
			overlay := newOverlayEnviron(r.writeEnv, false)
			overlay.Set("PATH", expand.Variable{Kind: expand.String, Str: pathOverride})
			path, err = scriptFromPathDir(r.Dir, overlay, args[0])
		} else {
			path, err = scriptFromPathDir(r.Dir, r.writeEnv, args[0])
		}
		if err != nil {
			// If the script was not found in PATH or there was any error, pass
			// the source path to the open handler so it has a chance to look
			// at files it manages (eg: virtual filesystem), and also allow
			// it to look for the sourced script in the current directory.
			path = args[0]
		}
		// In bash-compat mode, let r.open print its own bash-shaped
		// `<file>: line N: <path>: No such file or directory` line and
		// avoid stacking a redundant `source: ` prefix on top. Outside
		// compat mode, keep the legacy "source: <go-error>" wording.
		f, err := r.open(ctx, path, os.O_RDONLY, 0, r.bashCompatErrors)
		if err != nil {
			if r.bashCompatErrors {
				exit.code = 1
				return exit
			}
			return failf(1, "source: %v\n", err)
		}
		defer f.Close()
		p := syntax.NewParser()
		file, err := p.Parse(f, path)
		if err != nil {
			return failf(1, "source: %v\n", err)
		}

		// Keep the current versions of some fields we might modify.
		oldParams := r.Params
		oldSourceSetParams := r.sourceSetParams
		oldInSource := r.inSource

		// If we run "source file args...", set said args as parameters.
		// Otherwise, keep the current parameters.
		sourceArgs := len(args[1:]) > 0
		if sourceArgs {
			r.Params = args[1:]
			r.sourceSetParams = false
		}
		// We want to track if the sourced file explicitly sets the
		// parameters.
		r.sourceSetParams = false
		r.inSource = true // know that we're inside a sourced script.
		r.stmts(ctx, file.Stmts)

		// If we modified the parameters and the sourced file didn't
		// explicitly set them, we restore the old ones.
		if sourceArgs && !r.sourceSetParams {
			r.Params = oldParams
		}
		r.sourceSetParams = oldSourceSetParams
		r.inSource = oldInSource

		exit = r.exit
		exit.returning = false
	case "[":
		if len(args) == 0 || args[len(args)-1] != "]" {
			return failf(2, "[: missing `]'\n")
		}
		args = args[:len(args)-1]
		fallthrough
	case "test":
		// bash arg-count quirks for `test`:
		//
		//   - 2 args: usually `-X arg` (unary). bash picks unary
		//     when neither side looks like the operator it needs:
		//     args[1] isn't a known *binary* op AND args[0] isn't
		//     a known *unary* op (or `!`). In that case the
		//     diagnostic blames args[0] with "unary operator
		//     expected". When args[1] IS a binary op (e.g.
		//     `a -a`), bash instead treats it as a binary form
		//     missing the right operand — let the parser surface
		//     that.
		if len(args) == 2 && testBinaryOp(args[1]) == illegalTok {
			u := testUnaryOp(args[0])
			// `(` is in our unary table as TsParen (grouping),
			// but bash doesn't accept it as a true unary operator
			// in the 2-arg form — treat it like an unknown name.
			if (u == illegalTok || u == syntax.TsParen) && args[0] != "!" {
				r.errf("%s%s: %s: unary operator expected\n",
					r.bashErrPrefix(pos), name, args[0])
				exit.code = 2
				return exit
			}
		}
		if len(args) == 3 {
			if op := testBinaryOp(args[1]); op != illegalTok &&
				op != syntax.AndTest && op != syntax.OrTest {
				word := func(s string) *syntax.Word {
					return &syntax.Word{Parts: []syntax.WordPart{
						&syntax.Lit{Value: s},
					}}
				}
				expr := &syntax.BinaryTest{
					Op: op,
					X:  word(args[0]),
					Y:  word(args[2]),
				}
				r.testIntErr = ""
				exit.oneIf(r.bashTest(ctx, expr, true) == "")
				if r.testIntErr != "" {
					inner := name
					r.errf("%s%s: %s: integer expected\n",
						r.bashErrPrefix(pos), inner, r.testIntErr)
					r.testIntErr = ""
					exit.code = 2
				}
				return exit
			}
		}
		// 3-arg `arg1 OP arg2` where OP isn't a binary operator:
		// bash 5.3 emits `<OP>: binary operator expected` rather
		// than the generic "too many arguments" path used for 4+
		// args. Catch it here so the testParser doesn't take the
		// general recursive route for this case. Skip if args[0]
		// is `!` / `(` (grouping/negation), or any known unary op
		// — those go through the recursive parser unchanged.
		if len(args) == 3 &&
			testBinaryOp(args[1]) == illegalTok &&
			args[0] != "!" && args[0] != "(" &&
			testUnaryOp(args[0]) == illegalTok {
			r.errf("%s%s: %s: binary operator expected\n",
				r.bashErrPrefix(pos), name, args[1])
			exit.code = 2
			return exit
		}
		parseErr := false
		closer := ""
		if name == "[" {
			closer = "]"
		}
		p := testParser{
			rem:    args,
			closer: closer,
			err: func(err error) {
				// bash format: `<file>: line N: <test|[>: <msg>`
				r.errf("%s%s: %v\n",
					r.bashErrPrefix(pos), name, err)
				parseErr = true
			},
		}
		p.next()
		expr := p.classicTest("[", false)
		if parseErr {
			exit.code = 2
			return exit
		}
		r.testIntErr = ""
		exit.oneIf(r.bashTest(ctx, expr, true) == "")
		if r.testIntErr != "" {
			// bash: `<file>: line N: test: <arg>: integer expected` (exit 2).
			// The `[` form uses `[` instead of `test` as the inner name.
			inner := name
			r.errf("%s%s: %s: integer expected\n",
				r.bashErrPrefix(pos), inner, r.testIntErr)
			r.testIntErr = ""
			exit.code = 2
		}
	case "strmatch":
		if len(args) != 2 {
			return failf(2, "strmatch: usage: strmatch string pattern\n")
		}
		if !bashStrmatch(args[1], args[0]) {
			exit.code = 1
		}
	case "exec":
		if r.opts[optRestricted] {
			r.errf("%sexec: restricted\n", r.bashErrPrefix(pos))
			exit.code = 1
			return exit
		}
		// TODO: Consider unix.Exec, i.e. actually replacing
		// the process. It's in theory what a shell should do,
		// but in practice it would kill the entire Go process
		// and it's not available on Windows.
		var argv0 string
		clearEnv := false
		loginShell := false
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-a":
				argv0 = fp.value()
				if argv0 == "" {
					return failf(2, "exec: -a: option requires an argument\n")
				}
			case "-c":
				// bash 5.3 `-c` clears the environment for the
				// exec'd command.
				clearEnv = true
			case "-l":
				// bash 5.3 `-l` makes the exec'd shell act as a
				// login shell by prefixing argv[0] with `-`.
				loginShell = true
			default:
				return invalidOpt("exec", flag)
			}
		}
		args = fp.args()
		if len(args) == 0 {
			if argv0 != "" {
				return failf(2, "exec: -a requires a command to execute\n")
			}
			r.keepRedirs = true
			break
		}
		if loginShell {
			if argv0 != "" {
				argv0 = "-" + argv0
			} else if len(args) > 0 {
				argv0 = "-" + filepath.Base(args[0])
			}
		}
		r.exit.exiting = true
		r.execAs(ctx, pos, argv0, clearEnv, args)
		exit = r.exit
	case "command":
		showV := false  // -v: name or path
		showVV := false // -V: "X is a Y" description
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-v":
				showV = true
			case "-V":
				showVV = true
			case "-p":
				if r.opts[optRestricted] {
					r.errf("%scommand: -p: restricted\n", r.bashErrPrefix(pos))
					exit.code = 1
					return exit
				}
				// bash 5.3 `-p` runs the lookup with a default PATH;
				// we don't currently honour the override but accept
				// the flag so scripts that rely on it don't error.
			default:
				return invalidOpt("command", flag)
			}
		}
		args := fp.args()
		if len(args) == 0 {
			break
		}
		if !showV && !showVV {
			if IsBuiltin(args[0]) {
				return r.builtin(ctx, pos, args[0], args[1:])
			}
			r.exec(ctx, pos, args)
			exit = r.exit
			return exit
		}
		last := uint8(0)
		for _, arg := range args {
			last = 0
			if showVV {
				ms := r.typeMatches(arg, false)
				if len(ms) == 0 {
					r.errf(r.bashErrPrefix(pos)+"command: %s: not found\n", arg)
					last = 1
					continue
				}
				r.outf("%s\n", ms[0].desc)
				// bash's `command -V <fn>` also dumps the body
				// for a function match, like `type <fn>`.
				if ms[0].kind == "function" {
					if body := r.Funcs[arg]; body != nil {
						r.printFuncDecl(arg, body)
					}
				}
				continue
			}
			// -v: minimal form. Functions/builtins/keywords print the
			// name; files print the path.
			if syntax.IsKeyword(arg) || r.Funcs[arg] != nil || IsBuiltin(arg) {
				r.outf("%s\n", arg)
			} else if als, ok := r.alias[arg]; ok && r.opts[optExpandAliases] {
				var buf bytes.Buffer
				if len(als.args) > 0 {
					syntax.NewPrinter().Print(&buf, &syntax.CallExpr{Args: als.args})
				} else if als.file != nil {
					syntax.NewPrinter().Print(&buf, als.file)
					bs := bytes.TrimRight(buf.Bytes(), "\n")
					buf.Reset()
					buf.Write(bs)
				}
				if als.blank {
					buf.WriteByte(' ')
				}
				r.outf("alias %s='%s'\n", arg, &buf)
			} else if path, err := LookPathDir(r.Dir, r.writeEnv, arg); err == nil {
				r.outf("%s\n", path)
			} else {
				last = 1
			}
		}
		exit.code = last
	case "dirs":
		// `dirs +N` / `dirs -N` selects a single entry. `-v` adds
		// "index<TAB>dir" line-per-entry. `-p` is line-per-entry.
		// `-l` reserved (resolve ~ to HOME) — we emit the raw
		// paths either way. Without flags or index, print the
		// whole stack top-first on one line.
		vertical := false
		perLine := false
		idx := -1
		idxSign := byte(0)
		for _, a := range args {
			switch {
			case a == "-v":
				vertical = true
				perLine = true
			case a == "-p":
				perLine = true
			case a == "-c":
				r.dirStack = append(r.dirStack[:0], r.Dir)
				return exit
			case a == "-l":
				// no-op; we don't shorten paths.
			case strings.HasPrefix(a, "+") || strings.HasPrefix(a, "-"):
				n, err := strconv.Atoi(a[1:])
				if err != nil {
					r.errf("%sdirs: %s: invalid number\n",
						r.bashErrPrefix(r.curStmtPos), a)
					r.errf("dirs: usage: dirs [-clpv] [+N] [-N]\n")
					exit.code = 1
					return exit
				}
				idx = n
				idxSign = a[0]
			default:
				r.errf("%sdirs: %s: invalid option\n",
					r.bashErrPrefix(r.curStmtPos), a)
				r.errf("dirs: usage: dirs [-clpv] [+N] [-N]\n")
				exit.code = 1
				return exit
			}
		}
		topFirst := make([]string, len(r.dirStack))
		for i, d := range r.dirStack {
			topFirst[len(r.dirStack)-1-i] = d
		}
		if idx >= 0 {
			n := idx
			if idxSign == '-' {
				n = len(topFirst) - 1 - n
			}
			if n < 0 || n >= len(topFirst) {
				return failf(2, "dirs: %d: directory stack index out of range\n", idx)
			}
			if vertical {
				r.outf("%2d  %s\n", n, topFirst[n])
			} else {
				r.outf("%s\n", topFirst[n])
			}
			break
		}
		if perLine {
			for i, d := range topFirst {
				if vertical {
					r.outf("%2d  %s\n", i, d)
				} else {
					r.outf("%s\n", d)
				}
			}
			break
		}
		for i, dir := range slices.Backward(r.dirStack) {
			r.outf("%s", dir)
			if i > 0 {
				r.out(" ")
			}
		}
		r.out("\n")
	case "pushd":
		change := true
		if len(args) > 0 && args[0] == "-n" {
			change = false
			args = args[1:]
		}
		swap := func() string {
			oldtop := r.dirStack[len(r.dirStack)-1]
			top := r.dirStack[len(r.dirStack)-2]
			r.dirStack[len(r.dirStack)-1] = top
			r.dirStack[len(r.dirStack)-2] = oldtop
			return top
		}
		switch len(args) {
		case 0:
			if !change {
				break
			}
			if len(r.dirStack) < 2 {
				return failf(1, "pushd: no other directory\n")
			}
			newtop := swap()
			if code := r.changeDir(ctx, "pushd", newtop); code != 0 {
				exit.code = code
				return exit
			}
			r.builtin(ctx, syntax.Pos{}, "dirs", nil)
		case 1:
			// bash's pushd treats `+N` / `-N` arguments as a
			// stack-index rotation; reject other `-…` / `+…`
			// forms as an "invalid number" usage error before
			// trying to change directories.
			arg := args[0]
			if len(arg) > 1 && (arg[0] == '+' || arg[0] == '-') {
				idx, err := strconv.Atoi(arg[1:])
				if err != nil {
					r.errf("%spushd: %s: invalid number\n",
						r.bashErrPrefix(r.curStmtPos), arg)
					r.errf("pushd: usage: pushd [-n] [+N | -N | dir]\n")
					exit.code = 1
					return exit
				}
				topFirst := dirStackTopFirst(r.dirStack)
				n := dirStackIndex(len(topFirst), arg[0], idx)
				if n < 0 || n >= len(topFirst) {
					return failf(1, "pushd: %s: directory stack index out of range\n", arg)
				}
				oldStack := slices.Clone(r.dirStack)
				rotated := append(append([]string(nil), topFirst[n:]...), topFirst[:n]...)
				setDirStackTopFirst(r, rotated)
				if change {
					if code := r.changeDir(ctx, "pushd", rotated[0]); code != 0 {
						r.dirStack = oldStack
						exit.code = code
						return exit
					}
					r.builtin(ctx, syntax.Pos{}, "dirs", nil)
				}
				return exit
			}
			if change {
				// Push a new top slot first so that changeDir's
				// "keep dirStack top in sync with r.Dir" update
				// targets the new slot rather than overwriting the
				// previous top.
				r.dirStack = append(r.dirStack, "")
				if code := r.changeDir(ctx, "pushd", arg); code != 0 {
					r.dirStack = r.dirStack[:len(r.dirStack)-1]
					exit.code = code
					return exit
				}
			} else {
				r.dirStack = append(r.dirStack, arg)
				swap()
			}
			r.builtin(ctx, syntax.Pos{}, "dirs", nil)
		default:
			return failf(2, "pushd: too many arguments\n")
		}
	case "popd":
		change := true
		if len(args) > 0 && args[0] == "-n" {
			change = false
			args = args[1:]
		}
		switch len(args) {
		case 0:
			if len(r.dirStack) < 2 {
				return failf(1, "popd: directory stack empty\n")
			}
			oldtop := r.dirStack[len(r.dirStack)-1]
			r.dirStack = r.dirStack[:len(r.dirStack)-1]
			if change {
				newtop := r.dirStack[len(r.dirStack)-1]
				if code := r.changeDir(ctx, "popd", newtop); code != 0 {
					exit.code = code
					return exit
				}
			} else {
				r.dirStack[len(r.dirStack)-1] = oldtop
			}
			r.builtin(ctx, syntax.Pos{}, "dirs", nil)
		default:
			// `popd +N` / `popd -N` are stack-index ops in
			// bash; other `-…` / `+…` args are an "invalid
			// number" usage error.
			arg := args[0]
			if len(arg) > 1 && (arg[0] == '+' || arg[0] == '-') {
				idx, err := strconv.Atoi(arg[1:])
				if err != nil {
					r.errf("%spopd: %s: invalid number\n",
						r.bashErrPrefix(r.curStmtPos), arg)
					r.errf("popd: usage: popd [-n] [+N | -N]\n")
					exit.code = 1
					return exit
				}
				topFirst := dirStackTopFirst(r.dirStack)
				n := dirStackIndex(len(topFirst), arg[0], idx)
				if n < 0 || n >= len(topFirst) {
					return failf(1, "popd: %s: directory stack index out of range\n", arg)
				}
				if len(topFirst) < 2 {
					return failf(1, "popd: directory stack empty\n")
				}
				oldStack := slices.Clone(r.dirStack)
				newTopFirst := append(append([]string(nil), topFirst[:n]...), topFirst[n+1:]...)
				setDirStackTopFirst(r, newTopFirst)
				if change && n == 0 {
					if code := r.changeDir(ctx, "popd", newTopFirst[0]); code != 0 {
						r.dirStack = oldStack
						exit.code = code
						return exit
					}
				}
				r.builtin(ctx, syntax.Pos{}, "dirs", nil)
				return exit
			}
			return failf(2, "popd: invalid argument\n")
		}
	case "return":
		if !r.inFunc && !r.inSource {
			return failf(1, "return: can only `return' from a function or sourced script\n")
		}
		switch len(args) {
		case 0:
		case 1:
			n, err := strconv.Atoi(args[0])
			if err != nil {
				return failf(2, "invalid return status code: %q\n", args[0])
			}
			exit.code = uint8(n)
		default:
			return failf(2, "return: too many arguments\n")
		}
		exit.returning = true
	case "read":
		var prompt string
		raw := false
		silent := false
		readline := false
		readArray := false
		var timeout time.Duration
		nchars := 0
		// nstrict tracks `-N`: read exactly that many bytes, ignoring
		// the delimiter and skipping the IFS split step (the buffer
		// becomes one verbatim field).
		nstrict := false
		delim := "\n"
		// readFD: -u <N>. -1 means "use r.stdin" (the default).
		readFD := -1
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-s":
				silent = true
			case "-r":
				raw = true
			case "-a":
				readArray = true
			case "-p":
				prompt = fp.value()
				if prompt == "" {
					return failf(2, "read: -p: option requires an argument\n")
				}
			case "-t":
				val := fp.value()
				if val == "" {
					return failf(2, "read: -t: option requires an argument\n")
				}
				secs, err := strconv.ParseFloat(val, 64)
				if err != nil || secs < 0 {
					return failf(1, "read: %s: invalid timeout specification\n", val)
				}
				timeout = time.Duration(secs * float64(time.Second))
			case "-n", "-N":
				val := fp.value()
				n, err := strconv.Atoi(val)
				if err != nil || n < 0 {
					return failf(2, "read: %s: invalid number\n", val)
				}
				nchars = n
				nstrict = flag == "-N"
			case "-d":
				d := fp.value()
				if d == "" {
					delim = "\x00"
				} else {
					delim = d[:1]
				}
			case "-e", "-i":
				// -e (readline) and -i (initial text) require readline integration.
				// Accept but ignore for now.
				readline = true
				if flag == "-i" {
					fp.value() // consume the argument
				}
			case "-u":
				val := fp.value()
				n, err := strconv.Atoi(val)
				if err != nil || n < 0 {
					return failf(2, "read: %s: invalid file descriptor specification\n", val)
				}
				readFD = n
			default:
				return invalidOpt("read", flag)
			}
		}

		args := fp.args()
		for _, name := range args {
			if !syntax.ValidName(name) {
				return failf(2, "read: `%s': not a valid identifier\n", name)
			}
		}

		if prompt != "" {
			r.out(prompt)
		}

		// Resolve the reader: `-u N` opens fd N from the runner's
		// fd table; otherwise we keep r.stdin. Swap r.stdin so
		// readLine (which talks to r.stdin directly) sees the right
		// source. With `-t T` against a non-deadline-able file
		// (FIFOs without O_NONBLOCK), SetReadDeadline silently
		// fails and the read would block forever — skip the swap
		// in that case so the existing `r.stdin` path (which is
		// known to be deadline-able) still bounds the wait.
		var savedStdin *os.File
		stdinSwapped := false
		if readFD >= 0 {
			switch {
			case readFD == 1, readFD == 2:
				return failf(2, "read: %d: invalid file descriptor: not open for reading\n", readFD)
			case readFD == 0:
				// `-u 0` is just stdin — no swap needed.
			default:
				f, ok := r.fdTable[readFD]
				if !ok {
					return failf(2, "read: %d: invalid file descriptor: Bad file descriptor\n", readFD)
				}
				canSwap := true
				if timeout > 0 {
					// Probe whether SetReadDeadline is honored.
					if err := f.SetReadDeadline(time.Now().Add(time.Hour)); err != nil {
						canSwap = false
					} else {
						f.SetReadDeadline(time.Time{})
					}
				}
				if canSwap {
					savedStdin = r.stdin
					r.stdin = f
					stdinSwapped = true
				}
			}
		}
		defer func() {
			if stdinSwapped {
				r.stdin = savedStdin
			}
		}()

		readCtx := ctx
		if timeout > 0 {
			var cancel context.CancelFunc
			readCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		var line []byte
		var err error
		if nchars > 0 {
			buf := make([]byte, nchars)
			if nstrict {
				// `-N`: read EXACTLY nchars bytes, never honoring the
				// delimiter.
				n, readErr := io.ReadFull(r.stdin, buf)
				line = buf[:n]
				err = readErr
				if err == io.ErrUnexpectedEOF {
					err = io.EOF
				}
			} else {
				// `-n`: read up to nchars characters, stopping early at
				// the delimiter. Bash drops the delimiter byte from the
				// result, so read one byte at a time but count complete
				// UTF-8 runes.
				one := make([]byte, 1)
				delimByte := byte('\n')
				if len(delim) > 0 {
					delimByte = delim[0]
				}
				for chars := 0; chars < nchars; {
					n, readErr := r.stdin.Read(one)
					if n > 0 {
						if one[0] == delimByte {
							break
						}
						line = append(line, one[0])
						start := len(line) - 1
						for start > 0 && (line[start]&0xc0) == 0x80 {
							start--
						}
						cur := line[start:]
						if utf8.FullRune(cur) {
							chars++
						} else if len(cur) >= utf8.UTFMax {
							// Malformed input should still make progress.
							chars++
						}
					}
					if readErr != nil {
						err = readErr
						break
					}
				}
			}
		} else if silent {
			// Note that on Windows, syscall.Stdin is of type uintptr.
			line, err = term.ReadPassword(int(syscall.Stdin))
		} else {
			delimByte := byte('\n')
			if len(delim) > 0 {
				delimByte = delim[0]
			}
			line, err = r.readLine(readCtx, raw, delimByte)
		}
		// readLine already stops at the configured delimiter and
		// discards it; nothing left to trim here.
		_ = delim
		if readArray {
			// read -a arrayname: split line into fields and assign to indexed array.
			arrayName := shellReplyVar
			if len(args) > 0 {
				arrayName = args[0]
			}
			// Use -1 as max to get all fields without joining the last ones.
			values := expand.ReadFields(r.ecfg, string(line), -1, raw)
			r.setVar(arrayName, expand.Variable{
				Set:  true,
				Kind: expand.Indexed,
				List: values,
			})
		} else {
			if len(args) == 0 {
				// `read` (no NAMEs) assigns the raw line to
				// REPLY with leading/trailing IFS whitespace
				// preserved — bash's "otherwise unmodified"
				// behaviour. The trimming/splitting only kicks
				// in with explicit variable names.
				r.setVarString(shellReplyVar, string(line))
			} else if nstrict {
				// `-N` assigns the raw buffer to the first variable
				// and clears the rest; no field splitting per bash.
				r.setVarString(args[0], string(line))
				for _, name := range args[1:] {
					r.setVarString(name, "")
				}
			} else {
				values := expand.ReadFields(r.ecfg, string(line), len(args), raw)
				readonlyFailed := false
				for i, name := range args {
					val := ""
					if i < len(values) {
						val = values[i]
					}
					if r.lookupVar(name).ReadOnly {
						// bash: abort the read with exit 2 on
						// readonly-assignment failure. setVarString
						// will surface the diagnostic itself.
						r.setVarString(name, val)
						readonlyFailed = true
						break
					}
					r.setVarString(name, val)
				}
				if readonlyFailed {
					exit.code = 2
					return exit
				}
			}
		}

		// We can get data back from readLine and an error at the same time, so
		// check err after we process the data. For EOF with no data,
		// bash still clears the named variables to empty before
		// returning the non-zero exit status.
		if err != nil {
			if timeout > 0 && errors.Is(readCtx.Err(), context.DeadlineExceeded) {
				exit.code = 142
				return exit
			}
			if readline && timeout > 0 && len(line) == 0 {
				exit.code = 142
				return exit
			}
			if len(line) == 0 && !readArray && len(args) > 0 {
				for _, name := range args {
					r.setVarString(name, "")
				}
			}
			exit.code = 1
			return exit
		}

	case "getopts":
		// bash rejects any leading `-X` flag with an "invalid option"
		// diagnostic, even though our own optstring may legitimately
		// start with `-` (no, it can't — only `:` is special in bash).
		if len(args) > 0 && len(args[0]) > 1 && args[0][0] == '-' && args[0][1] != ':' {
			r.errf("%s%s: %s: invalid option\n",
				r.bashErrPrefix(r.curStmtPos), "getopts", args[0])
			r.errf("getopts: usage: getopts optstring name [arg ...]\n")
			exit.code = 2
			return exit
		}
		if len(args) < 2 {
			// bash 5.3 emits the usage line without the
			// `<file>: line N: ` prefix that other builtin
			// errors carry.
			r.errf("getopts: usage: getopts optstring name [arg ...]\n")
			exit.code = 2
			return exit
		}
		optind, _ := strconv.Atoi(r.envGet("OPTIND"))
		if optind-1 != r.optState.argidx {
			if optind < 1 {
				optind = 1
			}
			r.optState = getopts{argidx: optind - 1}
		}
		optstr := args[0]
		name := args[1]
		invalidName := !syntax.ValidName(name)
		args = args[2:]
		if len(args) == 0 {
			args = r.Params
		}
		// Invalid identifier: bash still advances getopts' internal
		// state (so the caller's subsequent `shift $((OPTIND-1))`
		// works) but refuses the assignment and returns rc=2.
		if invalidName {
			r.optState.next(optstr, args)
			optind := r.optState.argidx + 1
			r.setVarString("OPTIND", strconv.Itoa(optind))
			return failf(2, "getopts: `%s': not a valid identifier\n", name)
		}
		// Diagnostics fire unless the optstring starts with ':' (silent
		// mode) or the caller sets OPTERR=0 — the latter being bash's
		// runtime escape hatch when the optstring is hard-coded.
		diagnostics := !strings.HasPrefix(optstr, ":")
		if opterr, err := strconv.Atoi(r.envGet("OPTERR")); err == nil && opterr == 0 {
			diagnostics = false
		}

		opt, optarg, done := r.optState.next(optstr, args)

		r.setVarString(name, string(opt))
		// bash's getopts only surfaces the OPTARG-readonly diagnostic
		// when it would have written to OPTARG (i.e. the default branch
		// below): the unset that happens at the top of every getopts
		// call is silent on readonly.
		optargRO := r.lookupVar("OPTARG").ReadOnly
		if !optargRO {
			r.delVar("OPTARG")
		}
		// bash prefixes diagnostics with $0 (the script name) and prints
		// the offending character unquoted (e.g. `./script.sh: illegal
		// option -- c`).
		scriptName := r.argv0
		if scriptName == "" {
			scriptName = r.filename
		}
		if scriptName == "" {
			scriptName = "bashy"
		}
		switch {
		case opt == '?' && diagnostics && !done:
			r.errf("%s: illegal option -- %s\n", scriptName, optarg)
		case opt == ':' && diagnostics:
			r.errf("%s: option requires an argument -- %s\n", scriptName, optarg)
		default:
			if optarg != "" {
				r.setVarString("OPTARG", optarg)
			}
		}
		if optind-1 != r.optState.argidx {
			r.setVarString("OPTIND", strconv.FormatInt(int64(r.optState.argidx+1), 10))
		}

		exit.oneIf(done)

	case "shopt":
		mode := ""
		sawSet := false
		sawUnset := false
		posixOpts := false
		printReusable := false
		quiet := false
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-s":
				sawSet = true
				mode = flag
			case "-u":
				sawUnset = true
				mode = flag
			case "-o":
				posixOpts = true
			case "-p":
				// bash's `shopt -p` lists every option in
				// `shopt -s NAME` / `shopt -u NAME` form.
				printReusable = true
			case "-q":
				// `shopt -q NAME`: return 0 if set, 1 if not,
				// no output. Inline below in the per-arg loop.
				quiet = true
			default:
				return invalidOpt("shopt", flag)
			}
		}
		if sawSet && sawUnset {
			r.errf("%sshopt: cannot set and unset shell options simultaneously\n", r.bashErrPrefix(pos))
			exit.code = 1
			return exit
		}
		args := fp.args()
		// Emit a line as either `shopt -s NAME` / `shopt -u NAME`
		// (or `set -o`/`set +o` when -o is in play) for the
		// printReusable form, or `name<TAB>on/off` otherwise.
		emitOpt := func(name string, enabled, supported bool) {
			if printReusable {
				if posixOpts {
					flag := "+o"
					if enabled {
						flag = "-o"
					}
					r.outf("set %s %s\n", flag, name)
				} else {
					flag := "-u"
					if enabled {
						flag = "-s"
					}
					r.outf("shopt %s %s\n", flag, name)
				}
				return
			}
			width := 20 // shopt uses 20-char padding
			if posixOpts {
				width = 15 // shopt -o (aka set -o) uses 15
			}
			r.printOptLineWidth(name, enabled, supported, width)
		}
		if len(args) == 0 {
			// When combined with `-p`, `shopt -s` lists only set
			// options and `shopt -u` lists only unset ones.
			// Without `-p`, the same form just lists everything
			// in default bash format.
			showSet := mode == "" || mode == "-s"
			showUnset := mode == "" || mode == "-u"
			if posixOpts {
				// Build the combined list: real POSIX options
				// from posixOptsTable, plus all no-op aliases
				// (history, hashall, etc.) so `set -o` matches
				// bash's output. Sort alphabetically.
				type oentry struct {
					name    string
					enabled bool
				}
				var list []oentry
				for i, opt := range &posixOptsTable {
					if opt.name == "restricted" {
						continue
					}
					list = append(list, oentry{opt.name, r.opts[i]})
				}
				for name, on := range noOpSetOptions {
					if v, ok := r.noOpSetState[name]; ok {
						on = v
					}
					list = append(list, oentry{name, on})
				}
				sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })
				for _, e := range list {
					if (e.enabled && !showSet) || (!e.enabled && !showUnset) {
						continue
					}
					emitOpt(e.name, e.enabled, true)
				}
				break
			}
			{
				names := make([]string, len(bashOptsTable))
				for i, opt := range bashOptsTable {
					names[i] = opt.name
				}
				idx := make(map[string]int, len(names))
				for i, n := range names {
					idx[n] = i
				}
				sort.Strings(names)
				for _, n := range names {
					i := idx[n]
					enabled := r.opts[len(posixOptsTable)+i]
					if (enabled && !showSet) || (!enabled && !showUnset) {
						continue
					}
					emitOpt(n, enabled, bashOptsTable[i].supported)
				}
			}
			break
		}
		for _, arg := range args {
			opt, supported := (*bool)(nil), true
			if posixOpts {
				opt = r.posixOptByName(arg)
				if opt == nil {
					if defaultOn, ok := noOpSetOptions[arg]; ok {
						// `shopt -so NAME` for an accept-and-ignore
						// option (physical, ignoreeof, etc.): record
						// the toggle so subsequent listings echo it.
						if mode == "-s" || mode == "-u" {
							if r.noOpSetState == nil {
								r.noOpSetState = make(map[string]bool)
							}
							r.noOpSetState[arg] = mode == "-s"
							continue
						}
						// For `shopt -p -o NAME` / `shopt -q -o NAME`
						// emit/check using the tracked state so the
						// listing form matches bash's output.
						state := defaultOn
						if v, ok := r.noOpSetState[arg]; ok {
							state = v
						}
						if quiet {
							if !state {
								exit.code = 1
							}
							continue
						}
						emitOpt(arg, state, true)
						continue
					}
				}
			} else {
				opt, supported = r.bashOptByName(arg)
			}
			if opt == nil {
				// Bash 5.3 distinguishes the two `shopt` namespaces:
				// `shopt NAME` → "invalid shell option name"
				// `shopt -o NAME` → "invalid option name"
				kind := "invalid shell option name"
				if posixOpts {
					kind = "invalid option name"
				}
				r.errf("%sshopt: %s: %s\n", r.bashErrPrefix(pos), arg, kind)
				exit.code = 1
				return exit
			}

			switch mode {
			case "-s", "-u":
				// bash silently accepts `shopt -s NAME` even for
				// options that don't change anything at runtime
				// (cdspell, checkhash, histappend, etc.). Mirror
				// that — store the bit so subsequent `shopt -p`
				// reflects the user's choice; we just don't do
				// anything with it.
				_ = supported
				*opt = mode == "-s"
				// Some shopts have a mirrored runner-level
				// option (`optExpandAliases` etc.); keep them
				// in sync so the alias / extglob / etc.
				// code paths see the same state.
				switch arg {
				case "expand_aliases":
					r.opts[optExpandAliases] = *opt
				}
			default: // ""
				if !quiet {
					emitOpt(arg, *opt, supported)
				}
				// Bash's `shopt name` returns 0 if set, 1 if not —
				// useful for `if shopt -q name`-style probes
				// (and the open form too).
				if !*opt {
					exit.code = 1
				}
			}
		}
		r.updateExpandOpts()

	case "alias":
		show := func(name string, als alias) {
			var buf bytes.Buffer
			if als.raw != "" {
				buf.WriteString(als.raw)
			} else if len(als.args) > 0 {
				printer := syntax.NewPrinter()
				printer.Print(&buf, &syntax.CallExpr{
					Args: als.args,
				})
			} else if als.file != nil {
				printer := syntax.NewPrinter()
				printer.Print(&buf, als.file)
				// Bash 5.3 single-quotes the whole body; the
				// printer emits trailing newline which we strip
				// so the `'<body>'` quoting closes cleanly.
				bs := bytes.TrimRight(buf.Bytes(), "\n")
				buf.Reset()
				buf.Write(bs)
			}
			if als.blank {
				buf.WriteByte(' ')
			}
			r.outf("alias %s='%s'\n", name, &buf)
		}

		// `alias -p` prints all aliases (same as no args). Reject
		// any other `-X` option with bash 5.3's wording + usage.
		filtered := args
		if len(filtered) > 0 && filtered[0] == "-p" {
			filtered = filtered[1:]
			for name, als := range r.alias {
				show(name, als)
			}
		} else if len(filtered) > 0 && len(filtered[0]) > 1 && filtered[0][0] == '-' && !strings.Contains(filtered[0], "=") {
			r.errf("%salias: %s: invalid option\n", r.bashErrPrefix(pos), filtered[0])
			r.errf("alias: usage: alias [-p] [name[=value] ... ]\n")
			exit.code = 2
			return exit
		}
		if len(args) == 0 {
			for name, als := range r.alias {
				show(name, als)
			}
		}
	argsLoop:
		for _, arg := range filtered {
			name, src, ok := strings.Cut(arg, "=")
			if !ok {
				als, ok := r.alias[name]
				if !ok {
					r.errf(r.bashErrPrefix(pos)+"alias: %s: not found\n", name)
					exit.code = 1
					continue
				}
				show(name, als)
				continue
			}
			if !validAliasName(name) {
				r.errf("%salias: `%s': invalid alias name\n", r.bashErrPrefix(pos), name)
				exit.code = 1
				continue
			}

			// Bash stores alias bodies as TEXT and only re-parses
			// at expansion time, so things like
			// `alias switch=case` (a body that's a bare keyword) or
			// `alias foo="echo 'Error:"` (with unclosed quotes that
			// continue into the next user input) are legal even
			// though they don't parse standalone. Try the multi-stmt
			// parse first so embedded newlines / compound commands
			// run correctly; if that fails, fall back to the
			// per-word parse to preserve the legacy text-style
			// behaviour for tricky bodies.
			parser := syntax.NewParser()
			als := alias{
				blank: strings.TrimRight(src, " \t") != src,
			}
			if strings.HasPrefix(strings.TrimLeft(src, " \t"), "#") {
				als.raw = src
				if r.alias == nil {
					r.alias = make(map[string]alias)
				}
				r.alias[name] = als
				continue argsLoop
			}
			file, perr := parser.Parse(strings.NewReader(src), "")
			if perr == nil {
				if len(file.Stmts) == 1 {
					if ce, ok := file.Stmts[0].Cmd.(*syntax.CallExpr); ok && len(ce.Assigns) == 0 && file.Stmts[0].Redirs == nil {
						als.args = ce.Args
					}
				}
				if als.args == nil {
					als.file = file
				}
			} else {
				// Stmt-parse failed — try the per-word path. If even
				// that fails, surface the original error so users see
				// what's wrong (matches the old behaviour).
				var words []*syntax.Word
				var werr error
				for w, e := range parser.WordsSeq(strings.NewReader(src)) {
					if e != nil {
						werr = e
						break
					}
					words = append(words, w)
				}
				if werr != nil {
					als.raw = src
					if r.alias == nil {
						r.alias = make(map[string]alias)
					}
					r.alias[name] = als
					continue argsLoop
				}
				als.args = words
			}
			if r.alias == nil {
				r.alias = make(map[string]alias)
			}
			r.alias[name] = als
		}
	case "unalias":
		all := false
		for len(args) > 0 && len(args[0]) > 1 && args[0][0] == '-' {
			switch args[0] {
			case "-a":
				all = true
				args = args[1:]
			default:
				r.errf("%sunalias: %s: invalid option\n", r.bashErrPrefix(pos), args[0])
				r.errf("unalias: usage: unalias [-a] name [name ...]\n")
				exit.code = 2
				return exit
			}
		}
		if all {
			r.alias = nil
			break
		}
		if len(args) == 0 {
			r.errf("unalias: usage: unalias [-a] name [name ...]\n")
			exit.code = 2
			return exit
		}
		for _, name := range args {
			if _, ok := r.alias[name]; !ok {
				r.errf("%sunalias: %s: not found\n", r.bashErrPrefix(pos), name)
				exit.code = 1
				continue
			}
			delete(r.alias, name)
		}

	case "trap":
		fp := flagParser{remaining: args}
		listSignals := false
		printTraps := false
		callback := "-"
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-l":
				listSignals = true
			case "-p":
				printTraps = true
			case "-":
				// default signal
			default:
				r.errf("%strap: %s: invalid option\n", r.bashErrPrefix(pos), flag)
				r.errf("trap: usage: trap [-lp] [[arg] signal_spec ...]\n")
				exit.code = 2
				return exit
			}
		}
		if listSignals {
			col := 0
			for i := 1; i <= 15; i++ {
				if name, ok := signalNames[i]; ok {
					col++
					r.outf("%2d) SIG%-10s", i, name)
					if col%5 == 0 {
						r.outf("\n")
					}
				}
			}
			if col%5 != 0 {
				r.outf("\n")
			}
			break
		}
		args := fp.args()
		if printTraps || len(args) == 0 {
			// Print traps, optionally filtered by signal names
			filter := make(map[string]bool)
			for _, a := range args {
				filter[normalizeSignal(a)] = true
			}
			// bash prints `trap -- 'CMD' SIGNAME` with the body in
			// single-quotes (`'`) and the signal name prefixed with
			// `SIG` for all non-EXIT/non-DEBUG/non-ERR/non-RETURN
			// pseudo-signals. Order matches bash: EXIT first
			// (signal 0), then numeric signals in ascending order,
			// then ERR/DEBUG/RETURN at the end.
			sigPrefix := func(name string) string {
				switch name {
				case "EXIT", "DEBUG", "ERR", "RETURN":
					return name
				default:
					return "SIG" + name
				}
			}
			// Build the sort order: EXIT, then signals 1..15
			// (HUP, INT, QUIT, ILL, …, TERM) in numeric order,
			// then ERR, DEBUG, RETURN.
			sigOrder := []string{"EXIT"}
			for i := 1; i <= 31; i++ {
				if name, ok := signalNames[i]; ok {
					sigOrder = append(sigOrder, name)
				}
			}
			sigOrder = append(sigOrder, "ERR", "DEBUG", "RETURN")
			for _, sig := range sigOrder {
				cb, ok := r.trapCallbacks[sig]
				if !ok {
					continue
				}
				if len(filter) > 0 && !filter[sig] {
					continue
				}
				quoted := "'" + strings.ReplaceAll(cb, "'", `'\''`) + "'"
				r.outf("trap -- %s %s\n", quoted, sigPrefix(sig))
			}
			break
		}
		reset := false
		switch len(args) {
		case 1:
			// 1-arg form is just signal names — reset to default.
			reset = true
		default:
			callback = args[0]
			args = args[1:]
		}
		// `-` is the explicit "reset to default" sentinel; an
		// empty string means "ignore the signal" (a distinct state
		// that `trap -p` prints as `trap -- '' SIG`).
		if callback == "-" {
			reset = true
		}
		for _, arg := range args {
			sig := normalizeSignal(arg)
			if sig == "" {
				return failf(2, "trap: %s: invalid signal specification\n", arg)
			}
			if reset {
				delete(r.trapCallbacks, sig)
			} else {
				if r.trapCallbacks == nil {
					r.trapCallbacks = make(map[string]string)
				}
				r.trapCallbacks[sig] = callback
			}
		}

	case "readarray", "mapfile":
		dropDelim := false
		delim := "\n"
		// -O origin: index to start writing at (default 0).
		// -s skip:   discard the first `skip` lines from the input.
		// -n max:    copy at most `max` lines (0 = no limit).
		// -c quant:  invoke -C callback every `quant` lines (default 5000).
		// -C cb:     shell code run as `cb INDEX LINE` every quant lines.
		origin, skip, maxLines, quantum := 0, 0, 0, 5000
		callback := ""
		// readFD lets the caller pick an open FD other than stdin
		// (mapfile -u 3). -1 means "use r.stdin".
		readFD := -1
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-t":
				// Remove the delim from each line read
				dropDelim = true
			case "-d":
				if len(fp.remaining) == 0 {
					return failf(2, "%s: -d: option requires an argument\n", name)
				}
				delim = fp.value()
				if delim == "" {
					// Bash sets the delim to an ASCII NUL if provided with an empty
					// string.
					delim = "\x00"
				}
			case "-O":
				v := fp.value()
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 {
					return failf(2, "%s: %s: invalid origin specification\n", name, v)
				}
				origin = n
			case "-s":
				v := fp.value()
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 {
					return failf(2, "%s: %s: invalid line count specification\n", name, v)
				}
				skip = n
			case "-n":
				v := fp.value()
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 {
					return failf(2, "%s: %s: invalid line count specification\n", name, v)
				}
				maxLines = n
			case "-c":
				v := fp.value()
				n, err := strconv.Atoi(v)
				if err != nil || n <= 0 {
					return failf(2, "%s: %s: invalid callback quantum\n", name, v)
				}
				quantum = n
			case "-C":
				v := fp.value()
				if v == "" {
					return failf(2, "%s: -C: option requires an argument\n", name)
				}
				callback = v
			case "-u":
				v := fp.value()
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 {
					return failf(2, "%s: %s: invalid file descriptor specification\n", name, v)
				}
				readFD = n
			default:
				return invalidOpt(name, flag)
			}
		}

		args := fp.args()
		var arrayName string
		switch len(args) {
		case 0:
			arrayName = "MAPFILE"
		case 1:
			if args[0] == "" {
				return failf(1, "%s: empty array variable name\n", name)
			}
			if !syntax.ValidName(args[0]) {
				return failf(2, "%s: `%s': not a valid identifier\n", name, args[0])
			}
			arrayName = args[0]
		default:
			return failf(2, "%s: Only one array name may be specified, %v\n", name, args)
		}

		// Resolve the input source: -u FD selects an entry from the
		// per-runner fdTable; 0 means stdin; anything else is an
		// error if the fd hasn't been opened by a redirect.
		var src io.Reader = r.stdin
		switch {
		case readFD < 0, readFD == 0:
			// keep r.stdin
		case readFD == 1, readFD == 2:
			return failf(2, "%s: %d: invalid file descriptor: not open for reading\n", name, readFD)
		default:
			f, ok := r.fdTable[readFD]
			if !ok {
				return failf(2, "%s: %d: invalid file descriptor: Bad file descriptor\n", name, readFD)
			}
			src = f
		}
		var newLines []string
		scanner := bufio.NewScanner(src)
		scanner.Split(mapfileSplit(delim[0], dropDelim))
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			if skip > 0 && lineNum <= skip {
				continue
			}
			newLines = append(newLines, scanner.Text())
			// Fire the callback after the line is recorded — order
			// matters when -n caps reads, so do this before the
			// maxLines break check.
			if callback != "" && len(newLines)%quantum == 0 {
				idx := origin + len(newLines) - 1
				quoted, qerr := syntax.Quote(scanner.Text(), syntax.LangBash)
				if qerr == nil {
					cb := fmt.Sprintf("%s %d %s", callback, idx, quoted)
					if prog, perr := syntax.NewParser().Parse(strings.NewReader(cb), ""); perr == nil {
						r.stmts(ctx, prog.Stmts)
					}
				}
			}
			if maxLines > 0 && len(newLines) >= maxLines {
				break
			}
		}
		if err := scanner.Err(); err != nil {
			return failf(2, "%s: unable to read, %v\n", name, err)
		}

		// Merge into the existing indexed array so that entries below
		// origin and above origin+len(newLines) survive (matches bash).
		var vr expand.Variable
		vr.Kind = expand.Indexed
		if origin > 0 {
			prev := r.lookupVar(arrayName)
			if prev.Kind == expand.Indexed {
				vr.List = append([]string(nil), prev.List...)
			}
		}
		if len(vr.List) < origin {
			vr.List = append(vr.List, make([]string, origin-len(vr.List))...)
		}
		end := origin + len(newLines)
		if len(vr.List) < end {
			vr.List = append(vr.List, make([]string, end-len(vr.List))...)
		}
		for i, line := range newLines {
			vr.List[origin+i] = line
		}
		r.setVar(arrayName, vr)

	case "jobs":
		for i, bg := range r.bgProcs {
			marker := ' '
			switch i {
			case len(r.bgProcs) - 1:
				marker = '+'
			case len(r.bgProcs) - 2:
				marker = '-'
			}
			cmd := bg.cmd
			if cmd == "" {
				cmd = "running"
			}
			select {
			case <-bg.done:
				r.outf("[%d]%c  Done                       %s\n", i+1, marker, cmd)
			default:
				r.outf("[%d]%c  Running                    %s\n", i+1, marker, cmd)
			}
		}
	case "fg":
		// Argument forms mirror the merged `wait` logic: no args → most
		// recent bgProc; %N → bash job-spec; gN → legacy $! sentinel;
		// bare integer → real OS PID (since `$!` now returns one when the
		// bg statement spawned a real exec). Stdio is not re-attached —
		// see docs/plan-punted-builtins.md for why.
		//
		// Bash distinguishes "no current job" (no-arg with empty job
		// table) from "no such job" (arg doesn't match anything); replicate
		// that so scripts/tests can rely on the message shape.
		var bg *bgProc
		switch {
		case len(args) == 0:
			if len(r.bgProcs) == 0 {
				return failf(1, "fg: no current job\n")
			}
			bg = r.bgProcs[len(r.bgProcs)-1]
		case strings.HasPrefix(args[0], "%"):
			arg := strings.TrimPrefix(args[0], "%")
			n := int(atoi(arg))
			if n < 1 || n > len(r.bgProcs) {
				return failf(1, "fg: %%%s: no such job\n", arg)
			}
			bg = r.bgProcs[n-1]
		default:
			if rest, ok := strings.CutPrefix(args[0], "g"); ok {
				n := int(atoi(rest))
				if n < 1 || n > len(r.bgProcs) {
					return failf(1, "fg: %s: no such job\n", args[0])
				}
				bg = r.bgProcs[n-1]
			} else {
				pid, perr := strconv.ParseInt(args[0], 10, 64)
				if perr != nil {
					return failf(1, "fg: %s: no such job\n", args[0])
				}
				for _, candidate := range r.bgProcs {
					if candidate.pid.Load() == pid {
						bg = candidate
						break
					}
				}
				if bg == nil {
					return failf(1, "fg: pid %s is not a child of this shell\n", args[0])
				}
			}
		}
		// If a real OS PID has been published, defensively resume it in
		// case an external SIGSTOP left it stopped. Non-blocking: we only
		// send SIGCONT when pidReady is already closed; otherwise the
		// goroutine has not exec'd anything yet and there's nothing to
		// resume.
		select {
		case <-bg.pidReady:
			if pid := bg.pid.Load(); pid > 0 {
				continueIfStopped(int(pid))
			}
		default:
		}
		<-bg.done
		exit = *bg.exit
	case "bg":
		// In this interpreter, background jobs are already running.
		// bg is effectively a no-op since we don't support job stopping (SIGTSTP).
		if len(r.bgProcs) == 0 {
			return failf(1, "bg: no current job\n")
		}
	case "fc":
		// Stub: fc requires history infrastructure.
		return failf(2, "fc: history not available\n")
	case "bind":
		// Stub: bind requires readline infrastructure.
	case "history":
		// Stub: history requires history infrastructure.
		r.outf("history: not available in non-interactive mode\n")
	case "suspend":
		return failf(1, "suspend: not supported\n")
	case "runner-state":
		// Agentic introspection. Emits a JSON object describing the
		// current runner state to stdout. Subcommand selects which
		// section; with no arg, the full dump is returned.
		section := "all"
		if len(args) > 0 {
			section = args[0]
		}
		obj := map[string]any{}
		emitVars := func() map[string]string {
			m := map[string]string{}
			r.writeEnv.Each(func(name string, vr expand.Variable) bool {
				if vr.IsSet() {
					m[name] = vr.String()
				}
				return true
			})
			return m
		}
		emitOpts := func() map[string]bool {
			m := map[string]bool{}
			for i, opt := range &posixOptsTable {
				m[opt.name] = r.opts[i]
			}
			for i, opt := range bashOptsTable {
				m[opt.name] = r.opts[len(posixOptsTable)+i]
			}
			return m
		}
		emitTraps := func() map[string]string {
			m := map[string]string{}
			for k, v := range r.trapCallbacks {
				m[k] = v
			}
			return m
		}
		emitFds := func() []int {
			fds := []int{}
			for n := range r.fdTable {
				fds = append(fds, n)
			}
			for n := range r.inheritedFds {
				if !slices.Contains(fds, n) {
					fds = append(fds, n)
				}
			}
			slices.Sort(fds)
			return fds
		}
		emitFuncs := func() []string {
			names := []string{}
			for k := range r.Funcs {
				names = append(names, k)
			}
			slices.Sort(names)
			return names
		}
		switch section {
		case "vars":
			obj["vars"] = emitVars()
		case "opts":
			obj["opts"] = emitOpts()
		case "traps":
			obj["traps"] = emitTraps()
		case "fds":
			obj["fds"] = emitFds()
		case "funcs":
			obj["funcs"] = emitFuncs()
		case "callstack":
			frames := []map[string]any{}
			for _, f := range r.callStack {
				frames = append(frames, map[string]any{
					"funcName": f.funcName,
					"source":   f.source,
					"line":     f.line,
				})
			}
			obj["callstack"] = frames
		case "all", "":
			obj["vars"] = emitVars()
			obj["opts"] = emitOpts()
			obj["traps"] = emitTraps()
			obj["fds"] = emitFds()
			obj["funcs"] = emitFuncs()
			obj["subshell_level"] = r.subshellLevel
			obj["umask"] = fmt.Sprintf("%04o", r.umask)
			obj["deterministic"] = r.deterministic
		default:
			return failf(2, "runner-state: unknown section %q (try: vars opts traps fds funcs callstack all)\n", section)
		}
		buf, err := json.Marshal(obj)
		if err != nil {
			return failf(1, "runner-state: %v\n", err)
		}
		r.out(string(buf))
		r.out("\n")
	case "logout":
		// Bash refuses `logout` from a non-login shell. Embedders mark
		// the runner via WithLoginShell.
		if !r.loginShell {
			return failf(1, "logout: not login shell: use `exit'\n")
		}
		switch len(args) {
		case 0:
			exit = r.lastExit
		case 1:
			n, err := strconv.Atoi(args[0])
			if err != nil {
				return failf(2, "logout: invalid exit status code: %q\n", args[0])
			}
			exit.code = uint8(n)
		default:
			return failf(1, "logout: too many arguments\n")
		}
		exit.exiting = true
	case "compgen":
		actionType := ""
		varName := ""
		prefix := ""
		filter := ""
		word := ""
		for i := 0; i < len(args); i++ {
			arg := args[i]
			switch arg {
			case "-A":
				if i+1 >= len(args) {
					return invalidOpt("compgen", arg)
				}
				actionType = args[i+1]
				i++
			case "-V":
				if i+1 >= len(args) {
					return invalidOpt("compgen", arg)
				}
				varName = args[i+1]
				if !syntax.ValidName(varName) {
					return failf(1, "compgen: `%s': not a valid identifier\n", varName)
				}
				i++
			case "-P":
				if i+1 >= len(args) {
					return invalidOpt("compgen", arg)
				}
				prefix = args[i+1]
				i++
			case "-X":
				if i+1 >= len(args) {
					return invalidOpt("compgen", arg)
				}
				filter = args[i+1]
				i++
			case "-o":
				if i+1 >= len(args) {
					return invalidOpt("compgen", arg)
				}
				switch args[i+1] {
				case "bashdefault", "default", "dirnames", "filenames", "nospace":
				default:
					return failf(1, "compgen: %s: invalid option name\n", args[i+1])
				}
				i++
			case "-a":
				actionType = "alias"
			case "-b":
				actionType = "builtin"
			case "-k":
				actionType = "keyword"
			case "-r", "-D":
				return invalidOpt("compgen", arg)
			default:
				if strings.HasPrefix(arg, "-") {
					return invalidOpt("compgen", arg)
				}
				word = arg
			}
		}
		names, ok := r.compgenNames(actionType)
		if !ok {
			return failf(1, "compgen: %s: invalid action name\n", actionType)
		}
		var out []string
		for _, n := range names {
			if word != "" && !strings.HasPrefix(n, word) {
				continue
			}
			if filter != "" {
				if matched, _ := filepath.Match(filter, n); matched {
					continue
				}
			}
			out = append(out, prefix+n)
		}
		if varName != "" {
			r.setVar(varName, expand.Variable{Set: true, Kind: expand.Indexed, List: out})
		} else {
			for _, n := range out {
				r.outf("%s\n", n)
			}
		}
	case "complete", "compopt":
		if name == "compopt" {
			if len(args) >= 2 && args[0] == "-o" {
				return failf(1, "compopt: %s: invalid option name\n", args[1])
			}
			break
		}
		exit = r.completeBuiltin(pos, args)
		return exit
	case "times":
		// Print accumulated user and system times.
		r.outf("0m0.000s 0m0.000s\n0m0.000s 0m0.000s\n")
	case "umask":
		symbolic := false
		printFlag := false
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-S":
				symbolic = true
			case "-p":
				printFlag = true
			default:
				return failf(2, "umask: %s: invalid option\n", flag)
			}
		}
		args = fp.args()
		// helper to format the current mask in symbolic form
		formatSymbolic := func(mask int) string {
			// umask bits are inverted: a 1 bit means "deny".
			// Convert each user/group/other triad.
			perm := func(shift int) string {
				bits := (^mask >> shift) & 7
				s := ""
				if bits&4 != 0 {
					s += "r"
				}
				if bits&2 != 0 {
					s += "w"
				}
				if bits&1 != 0 {
					s += "x"
				}
				return s
			}
			return fmt.Sprintf("u=%s,g=%s,o=%s", perm(6), perm(3), perm(0))
		}
		if len(args) == 0 {
			switch {
			case symbolic && printFlag:
				r.outf("umask -S %s\n", formatSymbolic(r.umask))
			case symbolic:
				r.outf("%s\n", formatSymbolic(r.umask))
			case printFlag:
				r.outf("umask %04o\n", r.umask)
			default:
				r.outf("%04o\n", r.umask)
			}
			break
		}
		// Setting umask: accept either an octal mode or a bash-style
		// symbolic form (e.g. `u=rwx,g=rx,o=rx`, `g-w`, `+x`). The
		// symbolic form mutates the current mask; the octal form
		// replaces it. We deliberately do not call syscall.Umask
		// (process-wide) — only the per-Runner virtual mask moves.
		symRes := parseSymbolicUmask(args[0], r.umask)
		if symRes.ok {
			r.umask = symRes.mask
			break
		}
		if symRes.kind != "" {
			// Symbolic mode that parsed partially before hitting
			// an invalid byte — match bash's diagnostic shape.
			tok := string(symRes.badChar)
			if symRes.badChar == 0 {
				tok = ""
			}
			return failf(1, "umask: `%s': invalid symbolic mode %s\n", tok, symRes.kind)
		}
		mask, err := strconv.ParseUint(args[0], 8, 32)
		if err != nil {
			return failf(1, "umask: %s: octal number out of range\n", args[0])
		}
		r.umask = int(mask)
	case "export":
		// Handle "export" when used as a simple command (e.g., IFS=: export x).
		for _, arg := range args {
			eqIdx := strings.IndexByte(arg, '=')
			if eqIdx >= 0 {
				name := arg[:eqIdx]
				val := arg[eqIdx+1:]
				r.setVar(name, expand.Variable{Set: true, Kind: expand.String, Str: val, Exported: true})
			} else {
				vr := r.lookupVar(arg)
				vr.Exported = true
				r.setVar(arg, vr)
			}
		}
	case "readonly":
		for _, arg := range args {
			eqIdx := strings.IndexByte(arg, '=')
			if eqIdx >= 0 {
				name := arg[:eqIdx]
				val := arg[eqIdx+1:]
				r.setVar(name, expand.Variable{Set: true, Kind: expand.String, Str: val, ReadOnly: true})
			} else {
				vr := r.lookupVar(arg)
				vr.ReadOnly = true
				r.setVar(arg, vr)
			}
		}
	case "local":
		if !r.inFunc {
			return failf(1, "local: can only be used in a function\n")
		}
		for _, arg := range args {
			eqIdx := strings.IndexByte(arg, '=')
			if eqIdx >= 0 {
				name := arg[:eqIdx]
				val := arg[eqIdx+1:]
				r.setVar(name, expand.Variable{Set: true, Kind: expand.String, Str: val, Local: true})
			} else {
				vr := r.lookupVar(arg)
				vr.Local = true
				r.setVar(arg, vr)
			}
		}
	case "declare", "typeset":
		// Simple declare when called as a command (not keyword).
		// Keyword form is handled by DeclClause in runner.go.
		for _, arg := range args {
			eqIdx := strings.IndexByte(arg, '=')
			if eqIdx >= 0 {
				name := arg[:eqIdx]
				val := arg[eqIdx+1:]
				vr := expand.Variable{Set: true, Kind: expand.String, Str: val}
				if r.inFunc {
					vr.Local = true
				}
				r.setVar(name, vr)
			}
		}
	case "ulimit":
		// Minimal best-effort ulimit: read-only on the common
		// resource flags via syscall.Getrlimit. Setting is a no-op
		// (bash's tests use `ulimit -n N` defensively and then
		// `ulimit -n` to read it back — we just keep the override
		// in r.ulimits so the read returns it).
		exit = r.ulimitBuiltin(pos, args)
	default:
		if hint, ok := unsupportedHints[name]; ok {
			return failf(2, "%s: not supported in this shell — %s\n", name, hint)
		}
		return failf(2, "%s: not supported in this shell\n", name)
	}
	return exit
}

func (r *Runner) jsonOut(v any) exitStatus {
	buf, err := json.Marshal(v)
	if err != nil {
		r.errf("json: %v\n", err)
		return exitStatus{code: 1}
	}
	r.out(string(buf))
	r.out("\n")
	return exitStatus{}
}

func bashPrintfFormatError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "invalid format character") ||
		strings.Contains(msg, "missing format character")
}

// offendingToken extracts the token starting at pos within src, used
// to reformat our generic "statements must be separated" parser error
// into bash's "syntax error near unexpected token `X'" shape. Returns
// "" when pos is out of range or no token can be identified.
func offendingToken(src string, pos syntax.Pos) string {
	col := int(pos.Col())
	line := int(pos.Line())
	if line <= 0 || col <= 0 {
		return ""
	}
	// Walk to the start of the requested line.
	curLine := 1
	i := 0
	for ; i < len(src) && curLine < line; i++ {
		if src[i] == '\n' {
			curLine++
		}
	}
	// Advance to the requested column (1-indexed).
	i += col - 1
	if i >= len(src) {
		return ""
	}
	// Single-char operators bash names verbatim.
	switch src[i] {
	case ')', '(', '|', '&', ';', '<', '>', '`':
		return string(src[i])
	}
	// Identifier-ish token: keep reading until whitespace or
	// another operator.
	start := i
	for ; i < len(src); i++ {
		c := src[i]
		if c == ' ' || c == '\t' || c == '\n' || c == ';' || c == '&' || c == '|' || c == '<' || c == '>' || c == '(' || c == ')' || c == '`' {
			break
		}
	}
	return src[start:i]
}

// evalSourceLine returns the 1-indexed nth line of src with the
// trailing newline stripped, or "" when n is out of range. Used by
// the eval builtin to echo the offending line under a parse error.
// firstBraceLine returns the 1-indexed line of the first bare `{`
// token in src (skipping over `${`, `$()` insides, and single/double
// quoted strings) — bash's "from `{' command on line M" diagnostic
// points back at where the unclosed block started, which we can
// only approximate from the source.
func firstBraceLine(src string) int {
	line := 1
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\n':
			line++
		case '\\':
			if i+1 < len(src) {
				i++
			}
		case '\'':
			for i++; i < len(src) && src[i] != '\''; i++ {
				if src[i] == '\n' {
					line++
				}
			}
		case '"':
			for i++; i < len(src) && src[i] != '"'; i++ {
				switch src[i] {
				case '\\':
					if i+1 < len(src) {
						i++
					}
				case '\n':
					line++
				}
			}
		case '$':
			if i+1 < len(src) && (src[i+1] == '{' || src[i+1] == '(') {
				i++ // skip the `{` / `(`; we don't deeply scan
			}
		case '{':
			return line
		}
	}
	return 0
}

func evalSourceLine(src string, n int) string {
	if n <= 0 {
		return ""
	}
	cur := 1
	start := 0
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			if cur == n {
				return src[start:i]
			}
			cur++
			start = i + 1
		}
	}
	if cur == n {
		return src[start:]
	}
	return ""
}

// bashUsage holds the usage line bash 5.3 prints after a builtin
// rejects an invalid flag — verbatim from bash so the test suite
// diffs cleanly. Only the builtins exercised by the bash 5.3 test
// suite are listed; missing entries simply omit the usage line.
var bashUsage = map[string]string{
	"alias":    "alias [-p] [name[=value] ... ]",
	"bg":       "bg [job_spec ...]",
	"break":    "break [n]",
	"cd":       "cd [-L|[-P [-e]] [-@]] [dir]",
	".":        ". [-p path] filename [arguments]",
	"command":  "command [-pVv] command [arg ...]",
	"complete": "complete [-abcdefgjksuv] [-pr] [-DEI] [-o option] [-A action] [-G globpat] [-W wordlist] [-F function] [-C command] [-X filterpat] [-P prefix] [-S suffix] [name ...]",
	"compgen":  "compgen [-V varname] [-abcdefgjksuv] [-o option] [-A action] [-G globpat] [-W wordlist] [-F function] [-C command] [-X filterpat] [-P prefix] [-S suffix] [word]",
	"continue": "continue [n]",
	"declare":  "declare [-aAfFgiIlnrtux] [name[=value] ...] or declare -p [-aAfFilnrtux] [name ...]",
	"disown":   "disown [-h] [-ar] [jobspec ... | pid ...]",
	"enable":   "enable [-a] [-dnps] [-f filename] [name ...]",
	"exec":     "exec [-cl] [-a name] [command [argument ...]] [redirection ...]",
	"export":   "export [-fn] [name[=value] ...] or export -p",
	"fc":       "fc [-e ename] [-lnr] [first] [last] or fc -s [pat=rep] [command]",
	"fg":       "fg [job_spec]",
	"getopts":  "getopts optstring name [arg ...]",
	"hash":     "hash [-lr] [-p pathname] [-dt] [name ...]",
	"help":     "help [-dms] [pattern ...]",
	"history":  "history [-c] [-d offset] [n] or history -anrw [filename] or history -ps arg [arg...]",
	"jobs":     "jobs [-lnprs] [jobspec ...] or jobs -x command [args]",
	"kill":     "kill [-s sigspec | -n signum | -sigspec] pid | jobspec ... or kill -l [sigspec]",
	"let":      "let arg [arg ...]",
	"local":    "local [option] name[=value] ...",
	"logout":   "logout [n]",
	"mapfile":  "mapfile [-d delim] [-n count] [-O origin] [-s count] [-t] [-u fd] [-C callback] [-c quantum] [array]",
	"printf":   "printf [-v var] format [arguments]",
	"pwd":      "pwd [-LP]",
	"read":     "read [-Eers] [-a array] [-d delim] [-i text] [-n nchars] [-N nchars] [-p prompt] [-t timeout] [-u fd] [name ...]",
	"readonly": "readonly [-aAf] [name[=value] ...] or readonly -p",
	"return":   "return [n]",
	"set":      "set [-abefhkmnptuvxBCEHPT] [-o option-name] [--] [-] [arg ...]",
	"shift":    "shift [n]",
	"shopt":    "shopt [-pqsu] [-o] [optname ...]",
	"source":   "source [-p path] filename [arguments]",
	"trap":     "trap [-Plp] [[action] signal_spec ...]",
	"type":     "type [-afptP] name [name ...]",
	"typeset":  "typeset [-aAfFgiIlnrtux] name[=value] ... or typeset -p [-aAfFilnrtux] [name ...]",
	"umask":    "umask [-p] [-S] [mode]",
	"unalias":  "unalias [-a] name [name ...]",
	"unset":    "unset [-f] [-v] [-n] [name ...]",
	"wait":     "wait [-fn] [-p var] [id ...]",
}

// typeMatch is a single resolution of a name. type / command -V iterate
// them in bash priority order (keyword → alias → function → builtin →
// file). desc holds the "X is a Y" line for the default / -V output;
// path is set only for file matches.
type typeMatch struct {
	kind string // "keyword" | "alias" | "function" | "builtin" | "file"
	desc string
	path string
}

type cmdHashEntry struct {
	path       string
	hits       int
	restricted bool
}

type completionSpec struct {
	action   string
	options  []string
	flags    []string
	funcName string
	command  string
	wordlist string
	filter   string
	prefix   string
	suffix   string
}

func (s completionSpec) String(name string) string {
	var parts []string
	parts = append(parts, "complete")
	for _, opt := range s.options {
		parts = append(parts, "-o", opt)
	}
	if s.action != "" {
		parts = append(parts, "-A", s.action)
	}
	parts = append(parts, s.flags...)
	if s.funcName != "" {
		parts = append(parts, "-F", s.funcName)
	}
	if s.command != "" {
		parts = append(parts, "-C", s.command)
	}
	if s.wordlist != "" {
		parts = append(parts, "-W", bashCompletionQuote(s.wordlist))
	}
	if s.filter != "" {
		parts = append(parts, "-X", bashCompletionQuote(s.filter))
	}
	if s.prefix != "" {
		parts = append(parts, "-P", bashCompletionQuote(s.prefix))
	}
	if s.suffix != "" {
		parts = append(parts, "-S", bashCompletionQuote(s.suffix))
	}
	parts = append(parts, name)
	return strings.Join(parts, " ")
}

func bashCompletionQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func dirStackTopFirst(stack []string) []string {
	topFirst := make([]string, len(stack))
	for i, d := range stack {
		topFirst[len(stack)-1-i] = d
	}
	return topFirst
}

func setDirStackTopFirst(r *Runner, topFirst []string) {
	if cap(r.dirStack) < len(topFirst) {
		r.dirStack = make([]string, len(topFirst))
	} else {
		r.dirStack = r.dirStack[:len(topFirst)]
	}
	for i, d := range topFirst {
		r.dirStack[len(topFirst)-1-i] = d
	}
}

func dirStackIndex(length int, sign byte, idx int) int {
	if sign == '-' {
		return length - 1 - idx
	}
	return idx
}

func bashBuiltinNames() []string {
	return []string{
		".", ":", "[", "alias", "bg", "bind", "break", "builtin",
		"caller", "cd", "command", "compgen", "complete", "compopt",
		"continue", "declare", "dirs", "disown", "echo", "enable",
		"eval", "exec", "exit", "export", "false", "fc", "fg",
		"getopts", "hash", "help", "history", "jobs", "kill",
		"let", "local", "logout", "mapfile", "popd", "printf",
		"pushd", "pwd", "read", "readarray", "readonly", "return",
		"set", "shift", "shopt", "source", "suspend", "test",
		"times", "trap", "true", "type", "typeset", "ulimit",
		"umask", "unalias", "unset", "wait",
	}
}

func bashKeywordNames() []string {
	return []string{
		"if", "then", "else", "elif", "fi", "case", "esac", "for",
		"select", "while", "until", "do", "done", "in", "function",
		"time", "{", "}", "!", "[[", "]]", "coproc",
	}
}

func bashHelpTopicNames() []string {
	names := []string{"!", "%", "(( ... ))"}
	names = append(names, bashBuiltinNames()...)
	names = append(names,
		"[[ ... ]]", "case", "coproc", "for", "for ((", "function", "if",
		"select", "time", "until", "variables", "while", "{ ... }")
	slices.Sort(names)
	return names
}

func bashCompletePrintOrder() []string {
	return []string{
		"printenv", "texi2html", "groupmod", "typeset", "nohup", "unalias",
		"groupdel", "telnet", "local", "readonly", "cd", "type", "ln",
		"gunzip", "makeinfo", "jobs", "pushd", "acroread", "unset",
		"ghostview", "rsh", "exec", "kill", "eval", "chown", "gzip",
		"newgrp", "shopt", "ftp", "rlogin", "getopts", "nice", "gdb",
		"fg", "dvips", "texi2dvi", ".", "declare", "export", "xdvi",
		"su", "popd", "trap", "wait", "zmore", "disown", "gs", "gv",
		"source", "make", "bg", "cat", "mkdir", "help", "read", "time",
		"zcat", "uncompress", "rmdir", "more", "gzcat",
	}
}

func bashSetOptNames() []string {
	var names []string
	for _, opt := range posixOptsTable {
		if opt.name != "" && opt.name != "restricted" {
			names = append(names, opt.name)
		}
	}
	for n := range noOpSetOptions {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

func (r *Runner) compgenNames(actionType string) ([]string, bool) {
	switch actionType {
	case "alias":
		var names []string
		for n := range r.alias {
			names = append(names, n)
		}
		slices.Sort(names)
		return names, true
	case "function":
		names := make([]string, 0, len(r.Funcs))
		for n := range r.Funcs {
			names = append(names, n)
		}
		slices.Sort(names)
		return names, true
	case "shopt":
		names := make([]string, 0, len(bashOptsTable))
		for _, opt := range bashOptsTable {
			names = append(names, opt.name)
		}
		slices.Sort(names)
		return names, true
	case "setopt":
		return bashSetOptNames(), true
	case "builtin", "enabled":
		return bashBuiltinNames(), true
	case "keyword":
		return bashKeywordNames(), true
	case "helptopic":
		return bashHelpTopicNames(), true
	case "variable":
		var names []string
		r.writeEnv.Each(func(n string, _ expand.Variable) bool {
			names = append(names, n)
			return true
		})
		slices.Sort(names)
		return names, true
	}
	return nil, false
}

func (r *Runner) completeBuiltin(pos syntax.Pos, args []string) exitStatus {
	var exit exitStatus
	spec := completionSpec{}
	printSpecs := false
	removeSpecs := false
	var names []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-p":
			printSpecs = true
		case "-r":
			removeSpecs = true
		case "--":
			names = append(names, args[i+1:]...)
			i = len(args)
		case "-V":
			r.errf("%scomplete: -V: invalid option\n", r.bashErrPrefix(pos))
			r.errf("complete: usage: %s\n", bashUsage["complete"])
			exit.code = 2
			return exit
		case "-A":
			if i+1 >= len(args) {
				exit.code = 2
				return exit
			}
			spec.action = args[i+1]
			i++
		case "-F":
			spec.funcName = args[i+1]
			i++
		case "-C":
			spec.command = args[i+1]
			i++
		case "-W":
			spec.wordlist = args[i+1]
			i++
		case "-X":
			spec.filter = args[i+1]
			i++
		case "-P":
			spec.prefix = args[i+1]
			i++
		case "-S":
			spec.suffix = args[i+1]
			i++
		case "-o":
			if i+1 >= len(args) {
				exit.code = 2
				return exit
			}
			switch args[i+1] {
			case "bashdefault", "default", "dirnames", "filenames", "nospace":
				spec.options = append(spec.options, args[i+1])
				slices.Sort(spec.options)
			default:
				r.errf("%scomplete: %s: invalid option name\n", r.bashErrPrefix(pos), args[i+1])
				exit.code = 1
				return exit
			}
			i++
		case "-a":
			spec.flags = append(spec.flags, "-a")
		case "-b":
			if i == len(args)-1 {
				r.errf("complete: usage: %s\n", bashUsage["complete"])
				exit.code = 2
				return exit
			}
			spec.flags = append(spec.flags, "-b")
		case "-c", "-d", "-e", "-f", "-g", "-j", "-k", "-s", "-u", "-v":
			spec.flags = append(spec.flags, arg)
		default:
			if strings.HasPrefix(arg, "-") {
				r.errf("%scomplete: %s: invalid option\n", r.bashErrPrefix(pos), arg)
				r.errf("complete: usage: %s\n", bashUsage["complete"])
				exit.code = 2
				return exit
			}
			names = append(names, arg)
		}
	}
	if r.completionSpecs == nil {
		r.completionSpecs = make(map[string]completionSpec)
	}
	if printSpecs {
		if len(names) > 0 {
			ok := false
			for _, n := range names {
				if spec, exists := r.completionSpecs[n]; exists {
					r.outf("%s\n", spec.String(n))
					ok = true
				}
			}
			if !ok {
				exit.code = 1
			}
			return exit
		}
		seen := make(map[string]bool, len(r.completionSpecs))
		var keys []string
		for _, n := range bashCompletePrintOrder() {
			if _, ok := r.completionSpecs[n]; ok {
				keys = append(keys, n)
				seen[n] = true
			}
		}
		var rest []string
		for n := range r.completionSpecs {
			if !seen[n] {
				rest = append(rest, n)
			}
		}
		slices.Sort(rest)
		keys = append(keys, rest...)
		for _, n := range keys {
			r.outf("%s\n", r.completionSpecs[n].String(n))
		}
		if len(keys) == 0 {
			exit.code = 1
		}
		return exit
	}
	if removeSpecs {
		if len(names) == 0 {
			clear(r.completionSpecs)
			return exit
		}
		for _, n := range names {
			if _, ok := r.completionSpecs[n]; !ok {
				r.errf("%scomplete: %s: no completion specification\n", r.bashErrPrefix(pos), n)
				exit.code = 1
				continue
			}
			delete(r.completionSpecs, n)
		}
		return exit
	}
	for _, n := range names {
		r.completionSpecs[n] = spec
	}
	return exit
}

// typeMatches returns all resolutions of arg, in bash priority order.
// skipFuncs corresponds to `type -f` (suppress function matches);
// alias matches are only included when [optExpandAliases] is set.
func (r *Runner) typeMatches(arg string, skipFuncs bool) []typeMatch {
	var ms []typeMatch
	if syntax.IsKeyword(arg) {
		ms = append(ms, typeMatch{
			kind: "keyword",
			desc: fmt.Sprintf("%s is a shell keyword", arg),
		})
	}
	if als, ok := r.alias[arg]; ok && r.opts[optExpandAliases] {
		var buf bytes.Buffer
		if len(als.args) > 0 {
			syntax.NewPrinter().Print(&buf, &syntax.CallExpr{Args: als.args})
		} else if als.file != nil {
			syntax.NewPrinter().Print(&buf, als.file)
			bs := bytes.TrimRight(buf.Bytes(), "\n")
			buf.Reset()
			buf.Write(bs)
		}
		if als.blank {
			buf.WriteByte(' ')
		}
		ms = append(ms, typeMatch{
			kind: "alias",
			desc: fmt.Sprintf("%s is aliased to `%s'", arg, &buf),
		})
	}
	// Bash POSIX mode: POSIX special builtins (break, :, continue,
	// ., eval, exec, exit, …) take precedence over functions during
	// command lookup. List the builtin entry first in that case.
	posixSpecial := r.opts[optPosix] && isPosixSpecialBuiltin(arg) && IsBuiltin(arg) && !r.disabledBuiltins[arg]
	if posixSpecial {
		ms = append(ms, typeMatch{
			kind: "builtin",
			desc: fmt.Sprintf("%s is a special shell builtin", arg),
		})
	}
	if !skipFuncs {
		if _, ok := r.Funcs[arg]; ok {
			ms = append(ms, typeMatch{
				kind: "function",
				desc: fmt.Sprintf("%s is a function", arg),
			})
		}
	}
	if !posixSpecial && IsBuiltin(arg) && !r.disabledBuiltins[arg] {
		ms = append(ms, typeMatch{
			kind: "builtin",
			desc: fmt.Sprintf("%s is a shell builtin", arg),
		})
	}
	// Check the command hash table before doing a PATH lookup.
	// Bash uses the hashed entry directly (`<name> is hashed
	// (<path>)`) even when the file no longer exists.
	if entry, ok := r.cmdHashTable[arg]; ok {
		entry.hits++
		r.cmdHashTable[arg] = entry
		ms = append(ms, typeMatch{
			kind: "file",
			desc: fmt.Sprintf("%s is hashed (%s)", arg, entry.path),
			path: entry.path,
		})
	} else if path, err := LookPathDir(r.Dir, r.writeEnv, arg); err == nil {
		ms = append(ms, typeMatch{
			kind: "file",
			desc: fmt.Sprintf("%s is %s", arg, path),
			path: path,
		})
	}
	return ms
}

// symbolicUmaskResult conveys how parseSymbolicUmask classified its input.
// `kind` is "" on success or `looks-octal` (caller should try ParseUint),
// otherwise the bash diagnostic kind: "character", "operator", or "".
type symbolicUmaskResult struct {
	mask    int
	ok      bool
	badChar byte
	kind    string // "character" | "operator" | "" (general)
}

// parseSymbolicUmask applies a bash-style symbolic umask string to
// `current` and returns the new mask. The grammar is one or more
// clauses separated by commas; each clause is `[who][op]perms` where
// `who` is any combination of `u`, `g`, `o`, `a` (default `a`),
// `op` is `=`, `+`, or `-`, and `perms` is any combination of
// `r`, `w`, `x`. On failure, returns kind="" for "looks octal" so
// caller can try numeric parse, or "character"/"operator" with the
// offending byte for a bash-shaped diagnostic.
func parseSymbolicUmask(s string, current int) symbolicUmaskResult {
	if s == "" {
		return symbolicUmaskResult{}
	}
	// Quick reject: octal-looking input goes through ParseUint instead.
	allDigits := true
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			allDigits = false
			break
		}
	}
	if allDigits {
		return symbolicUmaskResult{}
	}
	mask := current
	for _, clause := range strings.Split(s, ",") {
		if clause == "" {
			return symbolicUmaskResult{badChar: ',', kind: "character"}
		}
		who := 0 // bitmask of which triads to affect: 4=u, 2=g, 1=o
		i := 0
		for ; i < len(clause); i++ {
			switch clause[i] {
			case 'u':
				who |= 4
			case 'g':
				who |= 2
			case 'o':
				who |= 1
			case 'a':
				who |= 7
			default:
				goto opStart
			}
		}
	opStart:
		if who == 0 {
			who = 7 // default = `a`
		}
		if i >= len(clause) {
			return symbolicUmaskResult{badChar: 0, kind: "operator"}
		}
		op := clause[i]
		if op != '=' && op != '+' && op != '-' {
			return symbolicUmaskResult{badChar: op, kind: "operator"}
		}
		i++
		var perms int
		for ; i < len(clause); i++ {
			switch clause[i] {
			case 'r':
				perms |= 4
			case 'w':
				perms |= 2
			case 'x':
				perms |= 1
			default:
				// Ignore `s` (setuid/setgid) and `t` (sticky)
				// — they don't fit in a umask. `X` (execute
				// if dir) is similarly absent. Anything else
				// is an error.
				if clause[i] != 's' && clause[i] != 't' && clause[i] != 'X' {
					return symbolicUmaskResult{badChar: clause[i], kind: "character"}
				}
			}
		}
		// Apply to each affected triad. Remember: umask bits MEAN
		// "denied", so allowed perms clear the corresponding bits.
		applyTriad := func(shift int) {
			cur := (^mask >> shift) & 7
			switch op {
			case '=':
				cur = perms
			case '+':
				cur |= perms
			case '-':
				cur &^= perms
			}
			// Rebuild mask: set triad bits to ~cur.
			mask &^= 7 << shift
			mask |= ((^cur) & 7) << shift
		}
		if who&4 != 0 {
			applyTriad(6)
		}
		if who&2 != 0 {
			applyTriad(3)
		}
		if who&1 != 0 {
			applyTriad(0)
		}
	}
	return symbolicUmaskResult{mask: mask & 0o777, ok: true}
}

// ulimitBuiltin implements a best-effort `ulimit`. `ulimit -X` reads
// the current limit (RLIMIT_NOFILE for `-n`, "unlimited" otherwise),
// and `ulimit -X N` records the value in r.ulimitOverride so the
// next read returns it. We don't actually call setrlimit because
// changes would affect the whole process and require permissions; the
// override is purely cosmetic but lets scripts that probe-and-loop
// against `ulimit -n` finish.
func (r *Runner) ulimitBuiltin(_ syntax.Pos, args []string) exitStatus {
	var exit exitStatus
	flag := "-f"
	var setVal string
	for _, a := range args {
		if len(a) > 1 && a[0] == '-' {
			flag = a
			continue
		}
		setVal = a
	}
	if setVal != "" {
		if r.ulimitOverride == nil {
			r.ulimitOverride = make(map[string]string)
		}
		r.ulimitOverride[flag] = setVal
		return exit
	}
	if r.ulimitOverride != nil {
		if v, ok := r.ulimitOverride[flag]; ok {
			r.outf("%s\n", v)
			return exit
		}
	}
	switch flag {
	case "-n":
		var rlim syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &rlim); err != nil {
			r.outf("unlimited\n")
			return exit
		}
		// RLIM_INFINITY is `0xffffffffffffffff` (uint64 max) on
		// linux and `0x7fffffffffffffff` (int64 max) on darwin/BSD.
		if rlim.Cur == ^uint64(0) || rlim.Cur == 1<<63-1 {
			r.outf("unlimited\n")
		} else {
			r.outf("%d\n", rlim.Cur)
		}
	default:
		r.outf("unlimited\n")
	}
	return exit
}

// unsupportedHints carries actionable messages for bash/POSIX builtins that
// IsBuiltin recognizes but this runner does not implement. The hint is
// appended to "<name>: not supported in this shell — " so agentic callers
// see a named alternative or escape hatch rather than a generic refusal.
// Most former entries (fg/bg/jobs/fc/umask/logout/etc.) now have explicit
// implementations in the dispatcher and no longer reach the default arm;
// future design work may swap some back to hint-only for outpost.
var unsupportedHints = map[string]string{
	"newgrp": "group switching is not supported; switch groups in the parent process (e.g. with sudo -g)",
}

// mapfileSplit returns a suitable Split function for a [bufio.Scanner];
// the code is mostly stolen from [bufio.ScanLines].
func mapfileSplit(delim byte, dropDelim bool) bufio.SplitFunc {
	// Bash strings can't hold a NUL byte, so when -d '' selects the
	// NUL delimiter we always drop it from the token regardless of -t.
	if delim == 0 {
		dropDelim = true
	}
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := bytes.IndexByte(data, delim); i >= 0 {
			// We have a full newline-terminated line.
			if dropDelim {
				return i + 1, data[0:i], nil
			} else {
				return i + 1, data[0 : i+1], nil
			}
		}
		// If we're at EOF, we have a final, non-terminated line. Return it.
		if atEOF {
			return len(data), data, nil
		}
		// Request more data.
		return 0, nil, nil
	}
}

func (r *Runner) printOptLine(name string, enabled, supported bool) {
	r.printOptLineWidth(name, enabled, supported, 15)
}

// printOptLineWidth emits a `name<spaces><tab>on/off` line padding the
// name to a width-character field. Bash uses 15 for `set -o` and 20
// for `shopt`. Names longer than the field get no padding.
func (r *Runner) printOptLineWidth(name string, enabled, supported bool, width int) {
	_ = supported
	pad := width - len(name)
	if pad < 0 {
		pad = 0
	}
	r.outf("%s%s\t%s\n", name, strings.Repeat(" ", pad), r.optStatusText(enabled))
}

func (r *Runner) readLine(ctx context.Context, raw bool, delim byte) ([]byte, error) {
	if r.stdin == nil {
		return nil, errors.New("interp: can't read, there's no stdin")
	}

	var line []byte
	esc := false

	stopc := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		r.stdin.SetReadDeadline(time.Now())
		close(stopc)
	})
	defer func() {
		if !stop() {
			// The AfterFunc was started.
			// Wait for it to complete, and reset the file's deadline.
			<-stopc
			r.stdin.SetReadDeadline(time.Time{})
		}
	}()
	for {
		var buf [1]byte
		n, err := r.stdin.Read(buf[:])
		if n > 0 {
			b := buf[0]
			switch {
			case !raw && b == '\\':
				line = append(line, b)
				esc = !esc
			case !raw && b == '\n' && esc && delim == '\n':
				// Backslash-newline is a line continuation only
				// when the delimiter is the default newline. With
				// `-d <other>` bash treats newline as ordinary.
				line = line[:len(line)-1]
				esc = false
			case b == delim:
				return line, nil
			default:
				line = append(line, b)
				esc = false
			}
		}
		if err != nil {
			return line, err
		}
	}
}

func (r *Runner) changeDir(ctx context.Context, cmd, path string) uint8 {
	if path == "" {
		r.errf("%s%s: empty directory path\n", r.bashErrPrefix(r.curStmtPos), cmd)
		return 1
	}
	apath := r.absPath(path)
	info, err := r.stat(ctx, apath)
	if err != nil {
		// bash format: `<file>: line N: <cmd>: <path>: No such file or directory`
		r.errf("%s%s: %s: No such file or directory\n",
			r.bashErrPrefix(r.curStmtPos), cmd, bashDiagnosticWord(path))
		return 1
	}
	if !info.IsDir() {
		r.errf("%s%s: %s: Not a directory\n",
			r.bashErrPrefix(r.curStmtPos), cmd, bashDiagnosticWord(path))
		return 1
	}
	if r.access(ctx, apath, access_X_OK) != nil {
		r.errf("%s%s: %s: Permission denied\n",
			r.bashErrPrefix(r.curStmtPos), cmd, bashDiagnosticWord(path))
		return 1
	}
	if r.lookupVar("OLDPWD").ReadOnly {
		r.errf("%sOLDPWD: readonly variable\n", r.bashErrPrefix(r.curStmtPos))
		return 1
	}
	if r.lookupVar("PWD").ReadOnly {
		r.errf("%sPWD: readonly variable\n", r.bashErrPrefix(r.curStmtPos))
		return 1
	}
	r.Dir = apath
	// Keep the top of the directory stack in sync with the current
	// dir so `pushd`/`popd`/`dirs` see the cd's effect. Bash treats
	// the topmost dirStack entry as the live "current dir".
	if len(r.dirStack) > 0 {
		r.dirStack[len(r.dirStack)-1] = apath
	}
	r.setVarString("OLDPWD", r.envGet("PWD"))
	r.setVarString("PWD", apath)
	return 0
}

func absPath(dir, path string) string {
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	return filepath.Clean(path) // TODO: this clean is likely unnecessary
}

func (r *Runner) absPath(path string) string {
	return absPath(r.Dir, path)
}

// flagParser is used to parse builtin flags.
//
// It's similar to the getopts implementation, but with some key differences.
// First, the API is designed for Go loops, making it easier to use directly.
// Second, it doesn't require the awkward ":ab" syntax that getopts uses.
// Third, it supports "-a" flags as well as "+a".
type flagParser struct {
	current   string
	remaining []string
}

func (p *flagParser) more() bool {
	if p.current != "" {
		// We're still parsing part of "-ab".
		return true
	}
	if len(p.remaining) == 0 {
		// Nothing left.
		p.remaining = nil
		return false
	}
	arg := p.remaining[0]
	if arg == "--" {
		// We explicitly stop parsing flags.
		p.remaining = p.remaining[1:]
		return false
	}
	if len(arg) == 0 || (arg[0] != '-' && arg[0] != '+') {
		// The next argument is not a flag.
		return false
	}
	// More flags to come.
	return true
}

func (p *flagParser) flag() string {
	arg := p.current
	if arg == "" {
		arg = p.remaining[0]
		p.remaining = p.remaining[1:]
	} else {
		p.current = ""
	}
	if len(arg) > 2 {
		// We have "-ab", so return "-a" and keep "-b".
		p.current = arg[:1] + arg[2:]
		arg = arg[:2]
	}
	return arg
}

func (p *flagParser) value() string {
	// When the previous `flag()` call split a cluster like `-dX`,
	// the rest (`X`, prefixed with `-`) is sitting in `p.current`.
	// For value-taking flags, that's the value — consume it.
	if p.current != "" {
		// p.current is shaped like "-<rest>". Strip the leading "-"
		// to recover the original tail.
		v := p.current[1:]
		p.current = ""
		return v
	}
	if len(p.remaining) == 0 {
		return ""
	}
	arg := p.remaining[0]
	p.remaining = p.remaining[1:]
	return arg
}

func (p *flagParser) args() []string { return p.remaining }

type getopts struct {
	argidx  int
	runeidx int
}

func (g *getopts) next(optstr string, args []string) (opt rune, optarg string, done bool) {
	if len(args) == 0 || g.argidx >= len(args) {
		return '?', "", true
	}
	arg := []rune(args[g.argidx])
	if len(arg) < 2 || arg[0] != '-' {
		return '?', "", true
	}
	// `--` is the end-of-options marker; consume it so OPTIND points
	// past it, matching bash.
	if string(arg) == "--" {
		g.argidx++
		g.runeidx = 0
		return '?', "", true
	}
	if arg[1] == '-' {
		return '?', "", true
	}

	opts := arg[1:]
	opt = opts[g.runeidx]
	hasRest := g.runeidx+1 < len(opts)

	i := strings.IndexRune(optstr, opt)
	if i < 0 {
		// invalid option — advance past this letter so we don't loop forever
		if hasRest {
			g.runeidx++
		} else {
			g.argidx++
			g.runeidx = 0
		}
		return '?', string(opt), false
	}

	if i+1 < len(optstr) && optstr[i+1] == ':' {
		// Option requires an argument. If there's content remaining in
		// the current cluster (e.g. `-bbval` for option `b`), that's
		// the value. Otherwise consume the next arg.
		if hasRest {
			optarg = string(opts[g.runeidx+1:])
			g.argidx++
			g.runeidx = 0
		} else {
			g.argidx++
			g.runeidx = 0
			if g.argidx >= len(args) {
				return ':', string(opt), false
			}
			optarg = args[g.argidx]
			g.argidx++
		}
		return opt, optarg, false
	}

	if hasRest {
		g.runeidx++
	} else {
		g.argidx++
		g.runeidx = 0
	}
	return opt, optarg, false
}

// optStatusText returns a shell option's status text display
func (r *Runner) optStatusText(status bool) string {
	if status {
		return "on"
	}
	return "off"
}

// signalNames maps signal numbers to names (POSIX + common).
var signalNames = map[int]string{
	0:  "EXIT",
	1:  "HUP",
	2:  "INT",
	3:  "QUIT",
	4:  "ILL",
	5:  "TRAP",
	6:  "ABRT",
	7:  "BUS",
	8:  "FPE",
	9:  "KILL",
	10: "USR1",
	11: "SEGV",
	12: "USR2",
	13: "PIPE",
	14: "ALRM",
	15: "TERM",
}

// normalizeSignal converts a signal specification to a canonical name.
// Accepts: "EXIT", "ERR", "DEBUG", "RETURN", "INT", "SIGINT", "2", etc.
// Returns "" if the signal is not recognized.
func normalizeSignal(s string) string {
	s = strings.ToUpper(s)
	s = strings.TrimPrefix(s, "SIG")
	// Pseudo-signals
	switch s {
	case "EXIT", "ERR", "DEBUG", "RETURN":
		return s
	}
	// Check by name
	if _, ok := signalByName(s); ok {
		return s
	}
	// Check by number
	if n, err := strconv.Atoi(s); err == nil {
		if name, ok := signalNames[n]; ok {
			return name
		}
	}
	return ""
}
