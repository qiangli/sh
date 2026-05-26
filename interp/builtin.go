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
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

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
		if prefix := r.bashErrPrefix(pos); prefix != "" {
			r.errf(prefix+format, a...)
		} else {
			r.errf(format, a...)
		}
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
		if err := Params(args...)(r); err != nil {
			return failf(2, "set: %v\n", err)
		}
		r.updateExpandOpts()
	case "shift":
		n := 1
		switch len(args) {
		case 0:
		case 1:
			if n2, err := strconv.Atoi(args[0]); err == nil {
				n = n2
				break
			}
			fallthrough
		default:
			return failf(2, "usage: shift [n]\n")
		}
		if n >= len(r.Params) {
			r.Params = nil
		} else {
			r.Params = r.Params[n:]
		}
	case "unset":
		vars := true
		funcs := true
	unsetOpts:
		for i, arg := range args {
			switch arg {
			case "-v":
				funcs = false
			case "-f":
				vars = false
			default:
				args = args[i:]
				break unsetOpts
			}
		}

		for _, arg := range args {
			if vars && r.lookupVar(arg).IsSet() {
				r.delVar(arg)
			} else if _, ok := r.Funcs[arg]; ok && funcs {
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
				arg, _, _ = expand.Format(r.ecfg, arg, nil)
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
					return failf(1, "printf: %q: not a valid identifier\n", assignTo)
				}
			default:
				return invalidOpt("printf", flag)
			}
		}
		args = fp.args()
		if len(args) == 0 {
			return failf(2, "usage: printf [-v var] format [arguments]\n")
		}
		format, args := args[0], args[1:]
		var sb strings.Builder
		for {
			s, n, err := expand.Format(r.ecfg, format, args)
			if err != nil {
				return failf(1, "%v\n", err)
			}
			if assignTo != "" {
				sb.WriteString(s)
			} else {
				r.out(s)
			}
			args = args[n:]
			if n == 0 || len(args) == 0 {
				break
			}
		}
		if assignTo != "" {
			r.setVarString(assignTo, sb.String())
		}
	case "break", "continue":
		if !r.inLoop {
			return failf(0, "%s is only useful in a loop\n", name)
		}
		enclosing := &r.breakEnclosing
		if name == "continue" {
			enclosing = &r.contnEnclosing
		}
		switch len(args) {
		case 0:
			*enclosing = 1
		case 1:
			if n, err := strconv.Atoi(args[0]); err == nil {
				*enclosing = n
				break
			}
			fallthrough
		default:
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
				return failf(2, "invalid option: %q\n", args[0])
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
		var path string
		switch len(args) {
		case 0:
			path = r.envGet("HOME")
		case 1:
			path = args[0]

			// replicate the commonly implemented behavior of `cd -`
			// ref: https://www.man7.org/linux/man-pages/man1/cd.1p.html#OPERANDS
			if path == "-" {
				path = r.envGet("OLDPWD")
				r.outf("%s\n", path)
			}
		default:
			return failf(2, "usage: cd [dir]\n")
		}
		exit.code = r.changeDir(ctx, "cd", path)
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
					r.errf("kill: %s: invalid signal specification\n", a)
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
				r.errf("kill: %s: no job control in this shell\n", target)
				continue
			}
			pid, err := strconv.Atoi(target)
			if err != nil {
				exit.code = 1
				r.errf("kill: %s: arguments must be process IDs\n", target)
				continue
			}
			if err := sendSignal(pid, sig); err != nil {
				exit.code = 1
				r.errf("kill: (%d) - %v\n", pid, err)
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
				}
			}
		}
		if anyNotFound {
			exit.code = 1
		}
	case "caller":
		// Print call stack info: line_number subroutine filename
		level := 0
		if len(args) > 0 {
			level = int(atoi(args[0]))
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
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-r":
				clearHash = true
			default:
				return failf(1, "hash: %s: invalid option\n", flag)
			}
		}
		if clearHash {
			clear(r.cmdHashTable)
			break
		}
		remaining := fp.args()
		if len(remaining) == 0 {
			// List cached commands
			for name, path := range r.cmdHashTable {
				r.outf("hash -p %s %s\n", path, name)
			}
			break
		}
		// Cache specific commands
		for _, name := range remaining {
			path, err := LookPathDir(r.Dir, r.writeEnv, name)
			if err != nil {
				r.errf(r.bashErrPrefix(pos)+"hash: %s: not found\n", name)
				exit.code = 1
				continue
			}
			if r.cmdHashTable == nil {
				r.cmdHashTable = make(map[string]string)
			}
			r.cmdHashTable[name] = path
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
					r.errf("help: no help topics match `%s'\n", name)
					exit.code = 1
				}
			}
		}
	case "enable":
		fp := flagParser{remaining: args}
		disable := false
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-n":
				disable = true
			default:
				return failf(2, "enable: %s: invalid option\n", flag)
			}
		}
		remaining := fp.args()
		if len(remaining) == 0 {
			// List enabled/disabled builtins
			if disable {
				for name := range r.disabledBuiltins {
					r.outf("enable -n %s\n", name)
				}
			}
			break
		}
		for _, name := range remaining {
			if !IsBuiltin(name) {
				r.errf("enable: %s: not a shell builtin\n", name)
				exit.code = 1
				continue
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
			return failf(1, "eval: %v\n", err)
		}
		r.stmts(ctx, file.Stmts)
		exit = r.exit
	case "source", ".":
		if len(args) < 1 {
			return failf(2, "%v: source: need filename\n", pos)
		}
		path, err := scriptFromPathDir(r.Dir, r.writeEnv, args[0])
		if err != nil {
			// If the script was not found in PATH or there was any error, pass
			// the source path to the open handler so it has a chance to look
			// at files it manages (eg: virtual filesystem), and also allow
			// it to look for the sourced script in the current directory.
			path = args[0]
		}
		f, err := r.open(ctx, path, os.O_RDONLY, 0, false)
		if err != nil {
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
			return failf(2, "%v: [: missing matching ]\n", pos)
		}
		args = args[:len(args)-1]
		fallthrough
	case "test":
		parseErr := false
		p := testParser{
			rem: args,
			err: func(err error) {
				r.errf("%v: %v\n", pos, err)
				parseErr = true
			},
		}
		p.next()
		expr := p.classicTest("[", false)
		if parseErr {
			exit.code = 2
			return exit
		}
		exit.oneIf(r.bashTest(ctx, expr, true) == "")
	case "exec":
		// TODO: Consider unix.Exec, i.e. actually replacing
		// the process. It's in theory what a shell should do,
		// but in practice it would kill the entire Go process
		// and it's not available on Windows.
		var argv0 string
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-a":
				argv0 = fp.value()
				if argv0 == "" {
					return failf(2, "exec: -a: option requires an argument\n")
				}
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
		r.exit.exiting = true
		r.execAs(ctx, pos, argv0, args)
		exit = r.exit
	case "command":
		showV := false // -v: name or path
		showVV := false // -V: "X is a Y" description
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-v":
				showV = true
			case "-V":
				showVV = true
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
			if change {
				if code := r.changeDir(ctx, "pushd", args[0]); code != 0 {
					exit.code = code
					return exit
				}
				r.dirStack = append(r.dirStack, r.Dir)
			} else {
				r.dirStack = append(r.dirStack, args[0])
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
			return failf(2, "popd: invalid argument\n")
		}
	case "return":
		if !r.inFunc && !r.inSource {
			return failf(1, "return: can only be done from a func or sourced script\n")
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
		readArray := false
		var timeout time.Duration
		nchars := 0
		delim := "\n"
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
					return failf(2, "read: %s: invalid timeout specification\n", val)
				}
				timeout = time.Duration(secs * float64(time.Second))
			case "-n", "-N":
				val := fp.value()
				n, err := strconv.Atoi(val)
				if err != nil || n < 0 {
					return failf(2, "read: %s: invalid count\n", val)
				}
				nchars = n
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
				if flag == "-i" {
					fp.value() // consume the argument
				}
			case "-u":
				fp.value() // consume fd argument, ignore for now
			default:
				return invalidOpt("read", flag)
			}
		}

		args := fp.args()
		for _, name := range args {
			if !syntax.ValidName(name) {
				return failf(2, "read: invalid identifier %q\n", name)
			}
		}

		if prompt != "" {
			r.out(prompt)
		}

		readCtx := ctx
		if timeout > 0 {
			var cancel context.CancelFunc
			readCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}

		var line []byte
		var err error
		if nchars > 0 {
			// Read exactly nchars bytes.
			buf := make([]byte, nchars)
			n, readErr := r.stdin.Read(buf)
			line = buf[:n]
			err = readErr
		} else if silent {
			// Note that on Windows, syscall.Stdin is of type uintptr.
			line, err = term.ReadPassword(int(syscall.Stdin))
		} else {
			line, err = r.readLine(readCtx, raw)
		}
		// Handle custom delimiter: if delim != "\n", trim at the delimiter.
		if delim != "\n" && len(line) > 0 {
			if idx := strings.IndexByte(string(line), delim[0]); idx >= 0 {
				line = line[:idx]
			}
		}
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
				args = append(args, shellReplyVar)
			}

			values := expand.ReadFields(r.ecfg, string(line), len(args), raw)
			for i, name := range args {
				val := ""
				if i < len(values) {
					val = values[i]
				}
				r.setVarString(name, val)
			}
		}

		// We can get data back from readLine and an error at the same time, so
		// check err after we process the data.
		if err != nil {
			if timeout > 0 && errors.Is(readCtx.Err(), context.DeadlineExceeded) {
				exit.code = 142
				return exit
			}
			exit.code = 1
			return exit
		}

	case "getopts":
		if len(args) < 2 {
			return failf(2, "getopts: usage: getopts optstring name [arg ...]\n")
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
		if !syntax.ValidName(name) {
			return failf(2, "getopts: invalid identifier: %q\n", name)
		}
		args = args[2:]
		if len(args) == 0 {
			args = r.Params
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
		r.delVar("OPTARG")
		switch {
		case opt == '?' && diagnostics && !done:
			r.errf("getopts: illegal option -- %q\n", optarg)
		case opt == ':' && diagnostics:
			r.errf("getopts: option requires an argument -- %q\n", optarg)
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
		posixOpts := false
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-s", "-u":
				mode = flag
			case "-o":
				posixOpts = true
			case "-p", "-q":
				return failf(2, "shopt: unsupported option %q\n", flag)
			default:
				return invalidOpt("shopt", flag)
			}
		}
		args := fp.args()
		if len(args) == 0 {
			if posixOpts {
				for i, opt := range &posixOptsTable {
					r.printOptLine(opt.name, r.opts[i], true)
				}
			} else {
				for i, opt := range bashOptsTable {
					r.printOptLine(opt.name, r.opts[len(posixOptsTable)+i], opt.supported)
				}
			}
			break
		}
		for _, arg := range args {
			opt, supported := (*bool)(nil), true
			if posixOpts {
				opt = r.posixOptByName(arg)
			} else {
				opt, supported = r.bashOptByName(arg)
			}
			if opt == nil {
				return failf(1, "shopt: invalid option name %q\n", arg)
			}

			switch mode {
			case "-s", "-u":
				if !supported {
					return failf(1, "shopt: unsupported option %q\n", arg)
				}
				*opt = mode == "-s"
			default: // ""
				r.printOptLine(arg, *opt, supported)
			}
		}
		r.updateExpandOpts()

	case "alias":
		show := func(name string, als alias) {
			var buf bytes.Buffer
			if len(als.args) > 0 {
				printer := syntax.NewPrinter()
				printer.Print(&buf, &syntax.CallExpr{
					Args: als.args,
				})
			}
			if als.blank {
				buf.WriteByte(' ')
			}
			r.outf("alias %s='%s'\n", name, &buf)
		}

		if len(args) == 0 {
			for name, als := range r.alias {
				show(name, als)
			}
		}
	argsLoop:
		for _, arg := range args {
			name, src, ok := strings.Cut(arg, "=")
			if !ok {
				als, ok := r.alias[name]
				if !ok {
					r.errf("alias: %q not found\n", name)
					continue
				}
				show(name, als)
				continue
			}

			// TODO: parse any CallExpr perhaps, or even any Stmt
			parser := syntax.NewParser()
			var words []*syntax.Word
			for w, err := range parser.WordsSeq(strings.NewReader(src)) {
				if err != nil {
					r.errf("alias: could not parse %q: %v\n", src, err)
					continue argsLoop
				}
				words = append(words, w)
			}

			if r.alias == nil {
				r.alias = make(map[string]alias)
			}
			r.alias[name] = alias{
				args:  words,
				blank: strings.TrimRight(src, " \t") != src,
			}
		}
	case "unalias":
		for _, name := range args {
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
				r.errf("trap: %q: invalid option\n", flag)
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
			for sig, cb := range r.trapCallbacks {
				if len(filter) > 0 && !filter[sig] {
					continue
				}
				r.outf("trap -- %q %s\n", cb, sig)
			}
			break
		}
		switch len(args) {
		case 1:
			// assume it's a signal, the default will be restored
		default:
			callback = args[0]
			args = args[1:]
		}
		// Treat both empty and - the same: reset to default.
		if callback == "-" {
			callback = ""
		}
		for _, arg := range args {
			sig := normalizeSignal(arg)
			if sig == "" {
				return failf(2, "trap: %s: invalid signal specification\n", arg)
			}
			if callback == "" {
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
			if !syntax.ValidName(args[0]) {
				return failf(2, "%s: invalid identifier %q\n", name, args[0])
			}
			arrayName = args[0]
		default:
			return failf(2, "%s: Only one array name may be specified, %v\n", name, args)
		}

		var vr expand.Variable
		vr.Kind = expand.Indexed
		scanner := bufio.NewScanner(r.stdin)
		scanner.Split(mapfileSplit(delim[0], dropDelim))
		for scanner.Scan() {
			vr.List = append(vr.List, scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return failf(2, "%s: unable to read, %v\n", name, err)
		}
		r.setVar(arrayName, vr)

	case "jobs":
		for i, bg := range r.bgProcs {
			select {
			case <-bg.done:
				r.outf("[%d]   Done\n", i+1)
			default:
				r.outf("[%d]   Running\n", i+1)
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
			return failf(1, "logout: not login shell: use \"exit\"\n")
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
	case "compgen", "complete", "compopt":
		// Phase 6 stubs: programmable completion.
		return failf(1, "%s: programmable completion not yet implemented\n", name)
	case "times":
		// Print accumulated user and system times.
		r.outf("0m0.000s 0m0.000s\n0m0.000s 0m0.000s\n")
	case "umask":
		if len(args) == 0 {
			r.outf("%04o\n", r.umask)
			break
		}
		// Setting umask: parse octal value. Updates only the per-Runner
		// virtual umask; we deliberately do not call syscall.Umask, which
		// is process-wide and would clobber sibling runners. See
		// Runner.umask.
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
	default:
		if hint, ok := unsupportedHints[name]; ok {
			return failf(2, "%s: not supported in this shell — %s\n", name, hint)
		}
		return failf(2, "%s: not supported in this shell\n", name)
	}
	return exit
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
	"command":  "command [-pVv] command [arg ...]",
	"continue": "continue [n]",
	"declare":  "declare [-aAfFgiIlnrtux] [name[=value] ...] or declare -p [-aAfFilnrtux] [name ...]",
	"disown":   "disown [-h] [-ar] [jobspec ... | pid ...]",
	"enable":   "enable [-a] [-dnps] [-f filename] [name ...]",
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
	"read":     "read [-ers] [-a array] [-d delim] [-i text] [-n nchars] [-N nchars] [-p prompt] [-t timeout] [-u fd] [name ...]",
	"readonly": "readonly [-aAf] [name[=value] ...] or readonly -p",
	"return":   "return [n]",
	"set":      "set [-abefhkmnptuvxBCEHPT] [-o option-name] [--] [-] [arg ...]",
	"shift":    "shift [n]",
	"shopt":    "shopt [-pqsu] [-o] [optname ...]",
	"source":   "source filename [arguments]",
	"trap":     "trap [-lp] [[arg] signal_spec ...]",
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
		}
		if als.blank {
			buf.WriteByte(' ')
		}
		ms = append(ms, typeMatch{
			kind: "alias",
			desc: fmt.Sprintf("%s is aliased to `%s'", arg, &buf),
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
	if IsBuiltin(arg) {
		ms = append(ms, typeMatch{
			kind: "builtin",
			desc: fmt.Sprintf("%s is a shell builtin", arg),
		})
	}
	if path, err := LookPathDir(r.Dir, r.writeEnv, arg); err == nil {
		ms = append(ms, typeMatch{
			kind: "file",
			desc: fmt.Sprintf("%s is %s", arg, path),
			path: path,
		})
	}
	return ms
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
	"ulimit": "ulimit is not settable from this shell; set resource limits in the parent process",
}

// mapfileSplit returns a suitable Split function for a [bufio.Scanner];
// the code is mostly stolen from [bufio.ScanLines].
func mapfileSplit(delim byte, dropDelim bool) bufio.SplitFunc {
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
	state := r.optStatusText(enabled)
	if supported {
		r.outf("%s\t%s\n", name, state)
		return
	}
	r.outf("%s\t%s\t(%q not supported)\n", name, state, r.optStatusText(!enabled))
}

func (r *Runner) readLine(ctx context.Context, raw bool) ([]byte, error) {
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
			case !raw && b == '\n' && esc:
				// line continuation
				line = line[len(line)-1:]
				esc = false
			case b == '\n':
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
		r.errf("%s: empty directory path\n", cmd)
		return 1
	}
	apath := r.absPath(path)
	info, err := r.stat(ctx, apath)
	if err != nil || !info.IsDir() {
		r.errf("%s: no such file or directory: %q\n", cmd, path)
		return 1
	}
	if r.access(ctx, apath, access_X_OK) != nil {
		r.errf("%s: permission denied: %q\n", cmd, path)
		return 1
	}
	r.Dir = apath
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
	if len(arg) < 2 || arg[0] != '-' || arg[1] == '-' {
		return '?', "", true
	}

	opts := arg[1:]
	opt = opts[g.runeidx]
	if g.runeidx+1 < len(opts) {
		g.runeidx++
	} else {
		g.argidx++
		g.runeidx = 0
	}

	i := strings.IndexRune(optstr, opt)
	if i < 0 {
		// invalid option
		return '?', string(opt), false
	}

	if i+1 < len(optstr) && optstr[i+1] == ':' {
		if g.argidx >= len(args) {
			// missing argument
			return ':', string(opt), false
		}
		optarg = args[g.argidx]
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
