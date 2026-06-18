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
	"reflect"
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
	// While reader-level history expansion is armed (`set -o history`
	// plus `set -H`), `!`-prefixed words are history event designators
	// resolved by the history engine at dispatch, not external commands.
	if histDesignator(name) {
		return true
	}
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

func validBuiltinAssignName(name string) bool {
	if syntax.ValidName(name) {
		return true
	}
	base, idx, ok := splitArrayRef(name)
	return ok && syntax.ValidName(base) && !strings.Contains(idx, "]")
}

// subscriptQuotesBalanced reports whether every quote opened in an array
// subscript is also closed. bash's skipsubscript honours quoting, so a
// subscript left inside an unclosed quote (e.g. `80's`) swallows the
// closing `]` and never forms a valid array reference.
func subscriptQuotesBalanced(s string) bool {
	var quote byte // 0, '\'', '"', or '`'
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch quote {
		case '\'':
			if c == '\'' {
				quote = 0
			}
		case '"':
			if c == '\\' {
				i++
			} else if c == '"' {
				quote = 0
			}
		case '`':
			if c == '`' {
				quote = 0
			}
		default:
			switch c {
			case '\\':
				i++
			case '\'', '"', '`':
				quote = c
			}
		}
	}
	return quote == 0
}

// builtinAssignNameValid validates a read/printf assignment target. Beyond
// the basic identifier/array-reference shape, it mirrors bash's re-parse of
// an already-expanded subscript: without assoc_expand_once the subscript is
// scanned again via skipsubscript, so one left inside an unclosed quote
// (`a[80's]`) is rejected as not a valid identifier.
func (r *Runner) builtinAssignNameValid(name string, quoted bool) bool {
	if opt, _ := r.bashOptByName("assoc_expand_once"); opt != nil && *opt {
		// With assoc_expand_once the subscript was expanded once during
		// word expansion and is taken literally — bash does not re-parse
		// it, so an unquoted target's bracket structure is known from the
		// source word: `A[$rkey]` with rkey=`]` keeps the literal key `]`.
		if syntax.ValidName(name) {
			return true
		}
		base, idx, ok := splitArrayRef(name)
		if !ok || idx == "" || !syntax.ValidName(base) {
			return false
		}
		// A fully quoted target (`"A[$k]"`) is not recognised as an array
		// reference at parse time; bash re-scans the once-expanded string
		// with skipsubscript, so a subscript whose `]` closes before the
		// end (`A[]]`) leaves trailing junk and is rejected.
		if quoted {
			if closesEarly, _ := assocSubscriptBracketIssue(idx); closesEarly {
				return false
			}
		}
		return true
	}
	if !validBuiltinAssignName(name) {
		return false
	}
	if _, idx, ok := splitArrayRef(name); ok && !subscriptQuotesBalanced(idx) {
		return false
	}
	return true
}

// builtinTargetTokenQuoted reports whether the source token on the builtin's
// command line whose first byte is `target[0]` and that contains target's base
// name began with a quote. It is a best-effort source scan (mirroring
// unsetOperandSources) used only to decide whether a once-expanded array
// subscript should be re-scanned strictly. When the source can't be recovered
// it returns false (lenient).
func (r *Runner) builtinTargetQuoted(pos syntax.Pos, base string) bool {
	offs, ok := r.sourceOffset(pos)
	if !ok {
		return false
	}
	end := offs
	for end < len(r.bashSource) && r.bashSource[end] != '\n' {
		end++
	}
	line := string(r.bashSource[offs:end])
	i := 0
	for i < len(line) {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		start := i
		quoted := line[i] == '\'' || line[i] == '"'
		for i < len(line) {
			c := line[i]
			if c == ' ' || c == '\t' {
				break
			}
			switch c {
			case '\\':
				i += 2
			case '\'':
				i++
				for i < len(line) && line[i] != '\'' {
					i++
				}
				if i < len(line) {
					i++
				}
			case '"':
				i++
				for i < len(line) {
					if line[i] == '\\' {
						i += 2
						continue
					}
					if line[i] == '"' {
						i++
						break
					}
					i++
				}
			default:
				i++
			}
		}
		tok := line[start:i]
		// A quoted target reference like "A[$k]" begins with the quote and
		// carries the base name right after it: match `"`+base+`[`.
		if quoted && len(tok) >= len(base)+2 &&
			(tok[0] == '"' || tok[0] == '\'') &&
			strings.HasPrefix(tok[1:], base) &&
			len(tok) > len(base)+1 && tok[len(base)+1] == '[' {
			return true
		}
	}
	return false
}

func (r *Runner) unsetBuiltinArrayElem(name, idx string) bool {
	vr := r.lookupVar(name)
	if vr.Kind == expand.NameRef {
		if base, targetIdx, ok := splitArrayRef(vr.Str); ok && syntax.ValidName(base) {
			return r.unsetArrayElem(base, targetIdx)
		}
		if vr.Str != "" && syntax.ValidName(vr.Str) {
			name = vr.Str
			vr = r.lookupVar(name)
		}
	}
	if vr.Kind == expand.String && vr.IsSet() {
		if n, err := r.arithFromString(idx); err == nil && n == 0 {
			r.delVar(name)
			return true
		}
		r.errf("%sunset: %s: not an array variable\n", r.bashErrPrefix(r.curStmtPos), name)
		return false
	}
	if vr.Kind == expand.Associative {
		if _, ok := vr.Map[idx]; ok {
			delete(vr.Map, idx)
			vr.Set = true
			r.setVar(name, vr)
		}
		return true
	}
	return r.unsetArrayElem(name, idx)
}

func (r *Runner) unsetStringArrayElem(name, idx string) bool {
	vr := r.lookupVar(name)
	if vr.Kind == expand.String && vr.IsSet() {
		if n, err := r.arithFromString(idx); err == nil && n == 0 {
			r.delVar(name)
			return true
		}
		r.errf("%sunset: %s: not an array variable\n", r.bashErrPrefix(r.curStmtPos), name)
		return false
	}
	if vr.Kind != expand.Associative {
		return r.unsetArrayElem(name, idx)
	}
	key := r.unsetStringSubscript(idx)
	if _, ok := vr.Map[key]; ok {
		delete(vr.Map, key)
		vr.Set = true
		r.setVar(name, vr)
	}
	return true
}

func (r *Runner) unsetStringSubscript(idx string) string {
	if opt, _ := r.bashOptByName("assoc_expand_once"); opt != nil && *opt {
		return quoteRemoveUnsetSubscript(idx)
	}
	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	file, err := parser.Parse(strings.NewReader("x "+idx+"\n"), "")
	if err != nil || len(file.Stmts) != 1 {
		return idx
	}
	call, ok := file.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) != 2 {
		return idx
	}
	return r.literal(call.Args[1])
}

func quoteRemoveUnsetSubscript(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if i+1 < len(s) {
				i++
			}
			b.WriteByte(s[i])
		case '\'':
			i++
			for i < len(s) && s[i] != '\'' {
				b.WriteByte(s[i])
				i++
			}
		case '"':
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) {
					i++
				}
				b.WriteByte(s[i])
				i++
			}
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

type unsetOperandSource struct {
	quoted bool
}

func (r *Runner) unsetOperandSources(pos syntax.Pos) []unsetOperandSource {
	offs, ok := r.sourceOffset(pos)
	if !ok {
		return nil
	}
	end := offs
	for end < len(r.bashSource) && r.bashSource[end] != '\n' {
		end++
	}
	line := string(r.bashSource[offs:end])
	return scanUnsetOperandSources(line)
}

func scanUnsetOperandSources(line string) []unsetOperandSource {
	var operands []unsetOperandSource
	i := 0
	for i < len(line) {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= len(line) {
			break
		}
		start := i
		quoted := line[i] == '\'' || line[i] == '"'
		for i < len(line) {
			switch line[i] {
			case '\\':
				i += 2
			case '\'':
				i++
				for i < len(line) && line[i] != '\'' {
					i++
				}
				if i < len(line) {
					i++
				}
			case '"':
				i++
				for i < len(line) {
					if line[i] == '\\' {
						i += 2
						continue
					}
					if line[i] == '"' {
						i++
						break
					}
					i++
				}
			case ' ', '\t':
				goto wordDone
			default:
				i++
			}
		}
	wordDone:
		if line[start:i] == "unset" {
			continue
		}
		if strings.HasPrefix(line[start:i], "-") {
			continue
		}
		operands = append(operands, unsetOperandSource{quoted: quoted})
	}
	return operands
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
	invalidIdentifier := func(builtin, ident string) exitStatus {
		if r.bashCompatErrors {
			return failf(1, "%s: `%s': not a valid identifier\n", builtin, ident)
		}
		return failf(1, "%s: invalid name %q\n", builtin, ident)
	}
	// Emulate bash's reader-level history recording: every builtin
	// dispatch advances the history cursor up to its source line. No-op
	// unless a script has turned on `set -o history`.
	r.histSync(ctx, pos)
	if histDesignator(name) {
		// The histSync call above consumed this line's `!` event
		// designator from the reader timeline, echoing the expansion to
		// stderr and executing it. Surface that execution's exit code.
		exit.code = r.exit.code
		return exit
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
				return failf(2, "exit: %s: numeric argument required\n", args[0])
			}
			exit.code = uint8(n)
		default:
			return failf(2, "exit: too many arguments\n")
		}
		exit.exiting = true
	case "set":
		if len(args) == 1 && args[0] == "--json" {
			return r.jsonOut(map[string]any{"variables": r.variablesJSON(true)})
		}
		if len(args) == 2 && args[0] == "-o" && strings.HasPrefix(args[1], "-") {
			for _, name := range bashSetOptNames() {
				status := "off"
				if opt := r.posixOptByName(name); opt != nil && *opt {
					status = "on"
				} else if on, ok := r.noOpSetState[name]; ok && on {
					status = "on"
				}
				r.outf("%-15s\t%s\n", name, status)
			}
			break
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
				if !vr.IsSet() && !(vr.Kind == expand.Associative && len(vr.Map) > 0) {
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
					// bash's `set` renders associative arrays with the
					// same declare -p body quoting: keys quoted when they
					// carry special characters, values always double-quoted.
					r.outf("%s=(", name)
					first := true
					for _, k := range vr.AssocKeysForDeclare() {
						if !first {
							r.out(" ")
						}
						r.outf("[%s]=%s", bashAssocKeyQuote(k), bashDeclareQuote(vr.Map[k]))
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
		prevHistOn := r.noOpSetState["history"]
		prevHistExp := r.noOpSetState["histexpand"]
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
		if now := r.noOpSetState["history"]; now != prevHistOn {
			r.histSetEnabled(now, pos)
		}
		if now := r.noOpSetState["histexpand"]; now != prevHistExp {
			r.histSetExpand(now)
		}
		// `set -H` / `set +H` aren't modeled in noOpSetState; scan the
		// flag arguments for the histexpand toggle.
		for _, a := range args {
			if a == "--" || a == "-" || len(a) < 2 || (a[0] != '-' && a[0] != '+') {
				break
			}
			if a[1] != 'o' && strings.ContainsRune(a[1:], 'H') {
				r.histSetExpand(a[0] == '-')
			}
		}
	case "shift":
		if len(args) == 1 && args[0] == "--help" {
			r.outf("%s", bashHelpShiftLong())
			break
		}
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
				return failf(2, "shift: %s: numeric argument required\n", args[0])
			}
			if n2 < 0 {
				return failf(1, "shift: %s: shift count out of range\n", args[0])
			}
			if n2 > len(r.Params) {
				// Out of range: silent error by default; with
				// `shopt -s shift_verbose` or in POSIX mode, emit
				// a diagnostic.
				if opt, _ := r.bashOptByName("shift_verbose"); (opt != nil && *opt) || r.opts[optPosix] {
					return failf(1, "shift: %s: shift count out of range\n", args[0])
				}
				exit.code = 1
				return exit
			}
			n = n2
		default:
			return failf(2, "shift: too many arguments\n")
		}
		if n >= len(r.Params) {
			r.Params = nil
		} else {
			r.Params = r.Params[n:]
		}
	case "unset":
		vars := true
		funcs := true
		explicitVars := false
		// `-n NAME` unsets the nameref itself (not the variable
		// it points to). Without -n, unset of a nameref follows
		// the reference and unsets the target.
		nameref := false
	unsetOpts:
		for i, arg := range args {
			switch arg {
			case "-v":
				explicitVars = true
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

		if !vars && !funcs {
			return failf(1, "unset: cannot simultaneously unset a function and a variable\n")
		}

		operandSources := r.unsetOperandSources(pos)
		for i := 0; i < len(args); i++ {
			arg := args[i]
			operand := unsetOperandSource{}
			if i < len(operandSources) {
				operand = operandSources[i]
			}
			// Bash 5.3: `unset 1bad` errors with "not a valid identifier"
			// (exit 2) when the var-namespace is in scope. Function names
			// are unrestricted, so `unset -f 1bad` is allowed.
			//
			// Array-element form `name[index]` is valid: unset the
			// specified element instead of the whole variable.
			if vars {
				if name, idx, ok := splitArrayRef(arg); ok {
					if syntax.ValidName(name) {
						unsetElem := r.unsetBuiltinArrayElem
						if operand.quoted {
							unsetElem = r.unsetStringArrayElem
						}
						if !unsetElem(name, idx) {
							exit.code = 1
						}
						continue
					}
				}
				if !syntax.ValidName(arg) {
					if !explicitVars && strings.Contains(arg, "$(") {
						for i+1 < len(args) && !syntax.ValidName(args[i+1]) {
							i++
						}
						continue
					}
					if r.bashCompatErrors && strings.Contains(arg, "/") {
						continue
					}
					msg := fmt.Sprintf("unset: `%s': not a valid identifier\n", arg)
					// Bash reports in-function errors at the function body brace line.
					line := int(pos.Line())
					if n := len(r.callStack); n > 0 && r.callStack[n-1].bodyLine > 0 {
						line = int(r.callStack[n-1].bodyLine)
					}
					if prefix := r.bashErrPrefixLine(line); prefix != "" {
						msg = prefix + msg
					}
					r.errf("%s", msg)
					r.reportError("builtin", syntax.NewPos(pos.Offset(), uint(line), pos.Col()), name, msg, 2)
					exit.code = 2
					continue
				}
			}
			if nameref {
				// Skip the auto-resolve so we delete the nameref
				// variable itself rather than its target.
				vr := r.lookupVar(arg)
				// Bash only acts when NAME actually has the nameref
				// attribute; `unset -n` on a plain variable is a no-op
				// (it does NOT fall back to unsetting the variable).
				if vr.Kind != expand.NameRef {
					continue
				}
				if vr.ReadOnly {
					r.errf("%sunset: %s: cannot unset: readonly variable\n", r.bashErrPrefix(pos), arg)
					exit.code = 1
					continue
				}
				r.delVar(arg)
				continue
			}
			if vars {
				switch arg {
				case "BASH_LINENO", "BASH_SOURCE":
					r.errf("%sunset: %s: cannot unset\n", r.bashErrPrefix(pos), arg)
					exit.code = 1
					continue
				}
				vr := r.lookupVar(arg)
				if vr.Kind == expand.NameRef {
					// Bash: `unset NAME` on a nameref follows
					// the reference and unsets the *target*.
					// The nameref itself keeps the attribute
					// (now pointing at an unset variable).
					if vr.Str != "" {
						if base, idx, ok := splitArrayRef(vr.Str); ok && syntax.ValidName(base) {
							tgt := r.lookupVar(base)
							if tgt.ReadOnly {
								r.errf("%sunset: %s: cannot unset: readonly variable\n",
									r.bashErrPrefix(pos), base)
								exit.code = 1
								continue
							}
							if !r.unsetArrayElem(base, idx) {
								exit.code = 1
							}
							continue
						}
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
				targetBase := assignTo
				if b, _, ok := splitArrayRef(assignTo); ok {
					targetBase = b
				}
				if !r.builtinAssignNameValid(assignTo, r.builtinTargetQuoted(pos, targetBase)) {
					if r.bashCompatErrors {
						return failf(1, "printf: `%s': not a valid identifier\n", assignTo)
					}
					return failf(1, "printf: %q: not a valid identifier\n", assignTo)
				}
				if base, idx, ok := splitArrayRef(assignTo); ok && (idx == "@" || idx == "*") {
					if r.lookupVar(base).Kind == expand.Indexed {
						return failf(1, "%s: bad array subscript\n", assignTo)
					}
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
			// Writing through an as-yet-untargeted nameref retargets it;
			// reject a value that isn't a valid identifier.
			if vr := r.lookupVar(assignTo); vr.Kind == expand.NameRef && vr.Str == "" &&
				!validNameRefTarget(sb.String()) {
				return failf(1, "printf: `%s': not a valid identifier\n", sb.String())
			}
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
					if name == "continue" {
						r.breakEnclosing = 1
					} else {
						*enclosing = 1
					}
					r.errf("%s%s: %s: loop count out of range\n",
						r.bashErrPrefix(r.curStmtPos), name, args[0])
					exit.code = 1
					return exit
				}
				*enclosing = n
				break
			}
			r.breakEnclosing = 1
			r.errf("%s%s: %s: numeric argument required\n",
				r.bashErrPrefix(r.curStmtPos), name, args[0])
			exit.code = 128
			exit.exiting = true
			return exit
		default:
			return failf(2, "%s: too many arguments\n", name)
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
			} else if cdpath, printCdpath, ok := r.cdpath(ctx, path); ok {
				path = cdpath
				printPath = printCdpath
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
			case "-f":
				// Wait for the job to terminate (not merely stop). Our
				// jobs only ever terminate, so this is a no-op accepted
				// for compatibility.
			default:
				return invalidOpt("wait", flag)
			}
		}
		remaining := fp.args()
		if waitNext {
			// `wait -n [jobspec...]` waits for the NEXT of the named jobs
			// (or, with no names, any background job) to finish, returning
			// with $? = that job's exit status. With -p VAR it also stores
			// the finishing job's PID into VAR (which may be an array
			// element like A[k], handled by setVarString's array path).
			candidates := r.bgProcs
			if len(remaining) > 0 {
				candidates = candidates[:0:0]
				for _, arg := range remaining {
					bg := r.resolveJobArg(arg)
					if bg == nil {
						return failf(1, "wait: %s: no such job\n", arg)
					}
					candidates = append(candidates, bg)
				}
			}
			// Any candidate already done?
			for _, bg := range candidates {
				select {
				case <-bg.done:
					exit = *bg.exit
					r.reapCoproc(bg)
					r.storeWaitPid(pidVar, bg)
					goto waitDone
				default:
				}
			}
			// None done yet; block until the first of them finishes.
			if bg := waitAnyDone(candidates); bg != nil {
				exit = *bg.exit
				r.reapCoproc(bg)
				r.storeWaitPid(pidVar, bg)
			}
		waitDone:
			break
		}
		if len(remaining) == 0 {
			// "wait" without arguments returns exit status zero, unless a
			// trapped signal interrupts it (POSIX: return >128, then run
			// the trap at the next statement boundary).
			for _, bg := range r.bgProcs {
				if r.waitOrSignal(bg) {
					_, num := r.peekPendingSignal()
					exit.code = uint8(128 + num)
					return exit
				}
				r.reapCoproc(bg)
			}
			break
		}
		for _, arg := range remaining {
			// Accept either the legacy "gN" sentinel ($! used to always
			// return that) or a real numeric OS PID (what $! now
			// returns when the bg statement spawned a real process).
			var bg *bgProc
			var matchedIdx int64
			if strings.HasPrefix(arg, "%") {
				// Job-control notation is allowed even without monitor
				// mode (SUS requirement). An unknown spec is "no such
				// job" with exit 127.
				bg = r.resolveJobArg(arg)
				if bg == nil {
					return failf(127, "wait: %s: no such job\n", arg)
				}
				for i, c := range r.bgProcs {
					if c == bg {
						matchedIdx = int64(i + 1)
						break
					}
				}
			} else if rest, ok := strings.CutPrefix(arg, "g"); ok {
				idx := atoi(rest)
				if idx <= 0 || idx > int64(len(r.bgProcs)) {
					return failf(1, "wait: pid %s is not a child of this shell\n", arg)
				}
				bg = r.bgProcs[idx-1]
				matchedIdx = idx
			} else {
				pid, perr := strconv.ParseInt(arg, 10, 64)
				if perr != nil {
					return failf(1, "wait: `%s': not a pid or valid job spec\n", arg)
				}
				for i, candidate := range r.bgProcs {
					// Match a real OS PID (published by the exec handler) or
					// a coproc's synthetic `<NAME>_PID`. Skip coprocs already
					// reaped so a stale entry with a reused synthetic pid
					// doesn't shadow the live one.
					if candidate.pid.Load() == pid ||
						(candidate.coprocPid == pid && candidate.coprocPidVar != "") {
						bg = candidate
						matchedIdx = int64(i + 1)
						break
					}
				}
				if bg == nil {
					return failf(1, "wait: pid %s is not a child of this shell\n", arg)
				}
			}
			if r.waitOrSignal(bg) {
				_, num := r.peekPendingSignal()
				exit.code = uint8(128 + num)
				return exit
			}
			exit = *bg.exit
			r.reapCoproc(bg)
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
					r.errf("%skill: -s: option requires an argument\n", r.bashErrPrefix(pos))
					exit.code = 2
					return exit
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
					return failf(1, "kill: %s: invalid signal specification\n", spec)
				}
				sig = s
				remaining = remaining[1:]
				break killFlags
			}
		}
		if listOnly {
			if len(remaining) == 0 {
				r.printSignalList()
				break
			}
			for _, a := range remaining {
				if n, err := strconv.Atoi(a); err == nil {
					if n > 128 {
						n -= 128
					}
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
				r.errf(r.bashErrPrefix(pos)+"kill: %s: invalid signal specification\n", a)
			}
			break
		}
		if len(remaining) == 0 {
			r.errf("kill: usage: %s\n", bashUsage["kill"])
			exit.code = 2
			return exit
		}
		for _, target := range remaining {
			if strings.HasPrefix(target, "%") {
				// Job-control notation is permitted without monitor mode
				// (SUS). Resolve the spec to its real OS PID and signal it.
				bg := r.resolveJobArg(target)
				if bg == nil {
					exit.code = 1
					r.errf(r.bashErrPrefix(pos)+"kill: %s: no such job\n", target)
					continue
				}
				if bg.pidReady != nil {
					<-bg.pidReady
				}
				rp := int(bg.pid.Load())
				if rp == 0 {
					// pure-goroutine job with no OS pid to signal
					continue
				}
				if err := sendSignal(rp, sig); err != nil {
					exit.code = 1
					r.errf(r.bashErrPrefix(pos)+"kill: (%d) - %v\n", rp, err)
				}
				continue
			}
			if strings.HasPrefix(target, "g") {
				exit.code = 1
				r.errf(r.bashErrPrefix(pos)+"kill: %s: no job control in this shell\n", target)
				continue
			}
			pid, err := strconv.Atoi(target)
			if err != nil {
				exit.code = 1
				r.errf(r.bashErrPrefix(pos)+"kill: `%s': not a pid or valid job spec\n", target)
				continue
			}
			// A coproc's `<NAME>_PID` is a synthetic integer, not a real OS
			// pid; resolve it to the coprocess's real child so the signal
			// actually reaches the running command (e.g. `kill $COPROC_PID`).
			// The registry is shared with subshells, so this also works for
			// the common `{ sleep 1; kill $COPROC_PID; } &` idiom.
			if r.coprocReg != nil {
				if bg := r.coprocReg.lookup(int64(pid)); bg != nil {
					if bg.pidReady != nil {
						<-bg.pidReady
					}
					if rp := bg.pid.Load(); rp != 0 {
						pid = int(rp)
					}
				}
			}
			// A signal directed at our own $$ for which this runner owns
			// a trap is delivered synchronously into the pending queue,
			// rather than via the OS — relying on signal.Notify here would
			// race with the trap firing before the next statement boundary.
			// Subshells don't own the trap (the signal infra is not
			// inherited), so a backgrounded `kill -SIG $$` still sends a
			// real OS signal that the parent's Notify catches.
			if pid == r.shellPid() {
				if _, sname, ok := signalByNumber(int(sig)); ok && r.trapSignalActive(sname) {
					r.markPendingSignal(sname)
					continue
				}
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
		// Backgrounded `&` statements are goroutines, not kernel jobs, and
		// the runner never sends SIGHUP, so disown's only observable effect
		// here is removing entries from r.bgProcs — which matters because a
		// later argument-less `wait` blocks on every remaining bgProc. With
		// -h (keep the job but shield it from SIGHUP) there is nothing for
		// us to do, so the job stays waitable.
		all, noHup := false, false
		fp := flagParser{remaining: args}
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-a":
				all = true
			case "-h":
				noHup = true
			case "-r":
				// restrict to running jobs; all ours are "running"
			default:
				return invalidOpt("disown", flag)
			}
		}
		specs := fp.args()
		switch {
		case noHup:
			// Keep jobs in the table (we never deliver SIGHUP anyway).
		case all:
			r.bgProcs = nil
		case len(specs) == 0:
			// disown the current (most recent) job.
			if n := len(r.bgProcs); n > 0 {
				r.bgProcs = r.bgProcs[:n-1]
			}
		default:
			// Remove the named jobs, highest index first so earlier
			// removals don't shift the indices still to be removed.
			var idxs []int
			for _, spec := range specs {
				arg := strings.TrimPrefix(spec, "%")
				n := int(atoi(arg))
				if n >= 1 && n <= len(r.bgProcs) {
					idxs = append(idxs, n-1)
				}
			}
			sort.Sort(sort.Reverse(sort.IntSlice(idxs)))
			for _, i := range idxs {
				r.bgProcs = append(r.bgProcs[:i], r.bgProcs[i+1:]...)
			}
		}
	case "builtin":
		if len(args) < 1 {
			break
		}
		if strings.HasPrefix(args[0], "-") && args[0] != "-" {
			r.errf("%sbuiltin: %s: invalid option\n", r.bashErrPrefix(pos), args[0])
			r.errf("builtin: usage: %s\n", bashUsage["builtin"])
			exit.code = 2
			return exit
		}
		if !IsBuiltin(args[0]) {
			r.errf("%sbuiltin: %s: not a shell builtin\n", r.bashErrPrefix(pos), args[0])
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
			idx := len(r.callStack) - 1 - level
			frame := r.callStack[idx]
			funcName := "main"
			if idx > 0 {
				funcName = r.callStack[idx-1].funcName
			}
			r.outf("%d %s %s\n", frame.line, funcName, frame.source)
		} else {
			exit.code = 1
		}
	case "hash":
		fp := flagParser{remaining: args}
		clearHash := false
		var explicitPath string
		listOnly := false
		printPath := false
		deleteNames := false
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
				printPath = true
			case "-d":
				// `hash -d NAME`: forget specific name.
				deleteNames = true
			default:
				r.errf("%shash: %s: invalid option\n", r.bashErrPrefix(pos), flag)
				r.errf("hash: usage: %s\n", bashUsage["hash"])
				exit.code = 1
				return exit
			}
		}
		if clearHash {
			clear(r.cmdHashTable)
			break
		}
		remaining := fp.args()
		if enabled, ok := r.noOpSetState["hashall"]; ok && !enabled &&
			(len(remaining) > 0 || explicitPath != "" || printPath || deleteNames) {
			return failf(1, "hash: hashing disabled\n")
		}
		hashListEntry := func(name string, entry cmdHashEntry) {
			r.outf("builtin hash -p %s %s\n", entry.path, name)
		}
		if deleteNames {
			if len(remaining) == 0 {
				return failf(2, "hash: -d: option requires an argument\n")
			}
			for _, name := range remaining {
				if _, ok := r.cmdHashTable[name]; !ok {
					r.errf(r.bashErrPrefix(pos)+"hash: %s: not found\n", name)
					exit.code = 1
					continue
				}
				delete(r.cmdHashTable, name)
			}
			break
		}
		if printPath {
			if len(remaining) == 0 {
				break
			}
			for _, name := range remaining {
				entry, ok := r.cmdHashTable[name]
				if !ok {
					r.errf(r.bashErrPrefix(pos)+"hash: %s: not found\n", name)
					exit.code = 1
					continue
				}
				if listOnly {
					hashListEntry(name, entry)
				} else {
					r.outf("%s\n", entry.path)
				}
			}
			break
		}
		if len(remaining) == 0 {
			// List cached commands in bash's format:
			//   hits	command
			//      N	/path
			if len(r.cmdHashTable) == 0 {
				r.outf("hash: hash table empty\n")
				break
			}
			names := make([]string, 0, len(r.cmdHashTable))
			vr := r.lookupVar("BASH_CMDS")
			if vr.Kind == expand.Associative {
				names = vr.AssocKeysForDeclare()
			}
			if listOnly {
				for _, name := range names {
					hashListEntry(name, r.cmdHashTable[name])
				}
			} else {
				r.outf("hits\tcommand\n")
				for _, name := range names {
					entry := r.cmdHashTable[name]
					r.outf("%4d\t%s\n", entry.hits, entry.path)
				}
			}
			break
		}
		// Cache specific commands.
		if explicitPath != "" && len(remaining) == 0 {
			r.errf("%shash: -p: option requires an argument\n", r.bashErrPrefix(pos))
			r.errf("hash: usage: %s\n", bashUsage["hash"])
			exit.code = 2
			return exit
		}
		for _, name := range remaining {
			var path string
			if explicitPath != "" {
				if info, err := os.Stat(explicitPath); err == nil && info.IsDir() {
					r.errf("%shash: %s: Is a directory\n", r.bashErrPrefix(pos), explicitPath)
					exit.code = 1
					continue
				}
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
		mode := ""
		var patterns []string
		for i := 0; i < len(args); i++ {
			arg := args[i]
			switch arg {
			case "--":
				patterns = append(patterns, args[i+1:]...)
				i = len(args)
			case "-d":
				mode = "description"
			case "-m":
				mode = "man"
			case "-s":
				mode = "short"
			default:
				if strings.HasPrefix(arg, "-") {
					r.errf("%shelp: %s: invalid option\n", r.bashErrPrefix(pos), arg)
					r.errf("help: usage: help [-dms] [pattern ...]\n")
					exit.code = 2
					return exit
				}
				patterns = append(patterns, arg)
			}
		}
		if len(patterns) == 0 {
			r.outf("%s", bashHelpOverview())
			break
		}
		for _, pattern := range patterns {
			if !r.helpTopic(pattern, mode) {
				r.errf(r.bashErrPrefix(pos)+"help: no help topics match `%s'.  Try `help help' or `man -k %s' or `info %s'.\n", pattern, pattern, pattern)
				exit.code = 1
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
		if len(args) > 0 {
			switch first := args[0]; {
			case first == "--":
				args = args[1:]
			case strings.HasPrefix(first, "-") && first != "-":
				r.errf("%seval: %s: invalid option\n", r.bashErrPrefix(pos), first)
				r.errf("eval: usage: %s\n", bashUsage["eval"])
				exit.code = 2
				return exit
			}
		}
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
				// The parser stamps "from `(' command on line N"
				// with the line inside the eval'd string; bash
				// counts from the top of the enclosing script.
				// Re-base before the rewrites below (the `{` case
				// constructs its own absolute line).
				if i := strings.LastIndex(text, " command on line "); i >= 0 {
					if n, aerr := strconv.Atoi(text[i+len(" command on line "):]); aerr == nil {
						text = fmt.Sprintf("%s command on line %d",
							text[:i], n+int(pos.Line())-1)
					}
				}
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
				// An arithmetic operator error inside $((...)) or
				// $[...] is a *runtime* arithmetic error in bash,
				// which defers arithmetic parsing to expansion time:
				// no `eval:` tag, no source echo, and the evaluator's
				// "operand expected" shape.
				if strings.HasSuffix(text, "must follow an expression") {
					if srcLine := evalSourceLine(src, int(pe.Pos.Line())); srcLine != "" {
						if expr, ok := innerArithText(srcLine); ok {
							r.errf("%s: line %d: %s: arithmetic syntax error: operand expected (error token is %q)\n",
								name, pos.Line(), expr, expr)
							exit.code = 1
							return exit
						}
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
		var parsedArgs []string
		for i := 0; i < len(args); i++ {
			arg := args[i]
			switch {
			case arg == "--":
				parsedArgs = append(parsedArgs, args[i+1:]...)
				i = len(args)
			case arg == "-p":
				if i+1 >= len(args) {
					return failf(2, "%s: -p: option requires an argument\n", name)
				}
				i++
				pathOverride = args[i]
				havePathOverride = true
			case strings.HasPrefix(arg, "-") && len(arg) > 1:
				return failf(2, "%s: %s: invalid option\n%s: usage: %s\n",
					name, arg, name, bashUsage[name])
			default:
				parsedArgs = append(parsedArgs, args[i:]...)
				i = len(args)
			}
		}
		args = parsedArgs
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
			if havePathOverride || (r.opts[optPosix] && !strings.Contains(args[0], "/")) {
				r.errf("%s%s: %s: file not found\n", r.bashErrPrefix(pos), name, args[0])
				if r.opts[optPosix] {
					exit.exiting = true
				}
				exit.code = 1
				return exit
			}
			path = args[0]
		}
		// In bash-compat mode, let r.open print its own bash-shaped
		// `<file>: line N: <path>: No such file or directory` line and
		// avoid stacking a redundant `source: ` prefix on top. Outside
		// compat mode, keep the legacy "source: <go-error>" wording.
		f, err := r.open(ctx, path, os.O_RDONLY, 0, r.bashCompatErrors)
		if err != nil {
			if r.bashCompatErrors {
				if r.opts[optPosix] {
					exit.exiting = true
				}
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
		oldFilename := r.filename

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
		r.filename = path
		r.callStack = append(r.callStack, callFrame{
			line:         pos.Line(),
			source:       path,
			callerSource: oldFilename,
			funcName:     "source",
		})
		r.stmts(ctx, file.Stmts)
		r.callStack = r.callStack[:len(r.callStack)-1]
		if r.trapCallbacks["RETURN"] != "" && (r.functraceEnabled() || len(r.callStack) == 0) {
			prevLineno := r.ecfg.OverrideLineno
			prevDebugTrap := r.trapCallbacks["DEBUG"]
			r.ecfg.OverrideLineno = int(pos.Line())
			if !r.functraceEnabled() && len(r.callStack) == 0 {
				delete(r.trapCallbacks, "DEBUG")
			}
			r.trapCallback(ctx, r.trapCallbacks["RETURN"], "return")
			if prevDebugTrap != "" {
				r.trapCallbacks["DEBUG"] = prevDebugTrap
			}
			r.ecfg.OverrideLineno = prevLineno
		}
		r.filename = oldFilename

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
			if r.bashCompatErrors && args[0] == "-v" {
				r.errf("%s%s: %s: binary operator expected\n",
					r.bashErrPrefix(pos), name, args[1])
				exit.code = 2
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
				exit = r.builtin(ctx, pos, args[0], args[1:])
				if args[0] == "." || args[0] == "source" {
					exit.exiting = false
				}
				return exit
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
				r.outf("alias %s='%s'\n", arg, aliasValue(als))
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
			case a == "--":
				args = nil
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
		if len(args) > 0 && args[0] == "--" {
			args = nil
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
		if len(args) > 0 && args[0] == "--" {
			args = nil
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
			r.errf("%spopd: %s: invalid argument\n", r.bashErrPrefix(r.curStmtPos), arg)
			r.errf("popd: usage: popd [-n] [+N | -N]\n")
			exit.code = 2
			return exit
		}
	case "return":
		if len(args) > 1 {
			msg := r.bashErrPrefix(pos) + "return: too many arguments"
			r.errf("%s\n", msg)
			r.reportError("builtin", pos, name, msg, 2)
			exit.code = 2
			if r.inFunc || r.inSource {
				exit.returning = true
			}
			return exit
		}
		if !r.inFunc && !r.inSource {
			return failf(1, "return: can only `return' from a function or sourced script\n")
		}
		switch len(args) {
		case 0:
			// `return` with no argument returns the exit status of the
			// last command executed in the function/sourced script.
			// POSIX interp 1602: directly inside a signal-trap action
			// (not a function it called), it instead yields the $? in
			// effect when the trap was invoked.
			if r.inSignalTrap && len(r.callStack) == r.signalTrapDepth {
				exit.code = r.signalTrapExit
			} else {
				exit.code = r.lastExit.code
			}
		case 1:
			n, err := strconv.Atoi(args[0])
			if err != nil {
				msg := r.bashErrPrefix(pos) + fmt.Sprintf("return: %s: numeric argument required", args[0])
				r.errf("%s\n", msg)
				r.reportError("builtin", pos, name, msg, 2)
				exit.code = 2
				exit.returning = true
				return exit
			}
			exit.code = uint8(n)
		}
		exit.returning = true
	case "read":
		var prompt string
		raw := false
		silent := false
		readline := false
		readArray := false
		var timeout time.Duration
		var timeoutSpec string
		invalidTimeoutStatus := uint8(0)
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
				if err != nil {
					timeoutSpec = val
					invalidTimeoutStatus = 1
					break
				}
				if secs < 0 {
					timeoutSpec = val
					invalidTimeoutStatus = 2
					break
				}
				timeout = time.Duration(secs * float64(time.Second))
				if secs > 0 && timeout == 0 {
					timeout = time.Nanosecond
				}
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
			targetBase := name
			if b, _, ok := splitArrayRef(name); ok {
				targetBase = b
			}
			if !r.builtinAssignNameValid(name, r.builtinTargetQuoted(pos, targetBase)) {
				return failf(2, "read: `%s': not a valid identifier\n", name)
			}
		}

		// `read -a` requires an indexed array; bash refuses to read into
		// an existing associative array.
		if readArray {
			arrayName := shellReplyVar
			if len(args) > 0 {
				arrayName = args[0]
			}
			if vr := r.lookupVar(arrayName); vr.Kind == expand.NameRef {
				if _, _, ok := splitArrayRef(vr.Str); !ok {
					arrayName = vr.Str
				}
			}
			if vr := r.lookupVar(arrayName); vr.Kind == expand.Associative {
				return failf(2, "read: %s: not an indexed array\n", arrayName)
			}
		}

		// Resolve the reader: `-u N` opens fd N from the runner's
		// fd table; otherwise we keep r.stdin. Swap r.stdin so
		// readLine sees the requested source.
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
				savedStdin = r.stdin
				r.stdin = f
				stdinSwapped = true
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

		stdin := r.stdin
		var input io.Reader = stdin
		clearReadVars := func() {
			if readArray {
				arrayName := shellReplyVar
				if len(args) > 0 {
					arrayName = args[0]
				}
				r.setVar(arrayName, expand.Variable{
					Set:  true,
					Kind: expand.Indexed,
				})
				return
			}
			for _, name := range args {
				r.setVarString(name, "")
			}
		}
		if invalidTimeoutStatus != 0 {
			r.errf("%sread: %s: invalid timeout specification\n",
				r.bashErrPrefix(r.curStmtPos), timeoutSpec)
			exit.code = 1
			return exit
		}
		if prompt != "" && stdin != nil && term.IsTerminal(int(stdin.Fd())) {
			r.out(prompt)
		}
		readInput := func() ([]byte, error) {
			var line []byte
			if nchars > 0 {
				buf := make([]byte, nchars)
				if nstrict {
					// `-N`: read EXACTLY nchars bytes, never honoring the
					// delimiter.
					n, readErr := io.ReadFull(input, buf)
					line = buf[:n]
					if readErr == io.ErrUnexpectedEOF {
						readErr = io.EOF
					}
					return line, readErr
				}

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
					n, readErr := input.Read(one)
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
						return line, readErr
					}
				}
				return line, nil
			}
			if silent {
				// Note that on Windows, syscall.Stdin is of type uintptr.
				return term.ReadPassword(int(syscall.Stdin))
			}
			delimByte := byte('\n')
			if len(delim) > 0 {
				delimByte = delim[0]
			}
			return r.readLineFrom(readCtx, input, raw, delimByte)
		}
		isReadTimeout := func(err error) bool {
			return timeout > 0 && (errors.Is(readCtx.Err(), context.DeadlineExceeded) ||
				errors.Is(err, os.ErrDeadlineExceeded))
		}
		var line []byte
		var err error
		if timeout > 0 {
			if r.stdinTTYFallback || r.stdinDevTTY {
				clearReadVars()
				exit.code = 142
				return exit
			}
			deadline := time.Now().Add(timeout)
			cancelGrace := func() {}
			if stdin != nil && timeout < 10*time.Millisecond && fdReadableNow(stdin) {
				deadline = time.Now().Add(10 * time.Millisecond)
				var cancel context.CancelFunc
				readCtx, cancel = context.WithTimeout(ctx, 10*time.Millisecond)
				cancelGrace = cancel
			}
			if stdin != nil && stdin.SetReadDeadline(deadline) == nil {
				line, err = readInput()
				stdin.SetReadDeadline(time.Time{})
				cancelGrace()
			} else {
				cancelGrace()
				if stdin != nil {
					input = &timeoutFileReader{ctx: readCtx, file: stdin, deadline: deadline}
				}
				line, err = readInput()
			}
		} else {
			line, err = readInput()
		}
		if isReadTimeout(err) {
			clearReadVars()
			exit.code = 142
			return exit
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
			if vr := r.lookupVar(arrayName); vr.Kind == expand.NameRef {
				if _, _, ok := splitArrayRef(vr.Str); ok {
					return failf(1, "read: `%s': not a valid identifier\n", vr.Str)
				}
				arrayName = vr.Str
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
					lookupName := name
					if base, _, ok := splitArrayRef(name); ok {
						lookupName = base
					}
					if r.lookupVar(lookupName).ReadOnly {
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

		// Storing the option character into an empty-target nameref
		// retargets it; a non-identifier result like `?` is rejected.
		// Bash reports this after the regular `illegal option`
		// diagnostic, so defer the error past the switch below.
		nameVar := r.lookupVar(name)
		nameRefBadTarget := nameVar.Kind == expand.NameRef && nameVar.Str == "" &&
			!validNameRefTarget(string(opt))
		if !nameRefBadTarget {
			r.setVarString(name, string(opt))
		}
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
		if nameRefBadTarget {
			r.errf("%sgetopts: `%s': not a valid identifier\n", r.bashErrPrefix(r.curStmtPos), string(opt))
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
			width := 20 // bash 5.3 pads `shopt` names to 20 before the tab
			if posixOpts && len(args) == 0 {
				width = 15 // ...but `shopt -o` listings pad to 15
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
							prev := r.noOpSetState[arg]
							r.noOpSetState[arg] = mode == "-s"
							if arg == "history" && prev != (mode == "-s") {
								r.histSetEnabled(mode == "-s", pos)
							}
							if arg == "histexpand" && prev != (mode == "-s") {
								r.histSetExpand(mode == "-s")
							}
							if arg == "ignoreeof" {
								r.setIgnoreEOFOption(mode == "-s")
							}
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
			// Bash displays alias bodies verbatim (single-quoted),
			// preserving the original text exactly.
			r.outf("alias %s='%s'\n", name, aliasValue(als))
		}

		// showAll lists every alias sorted by name, matching bash 5.3
		// (which keeps its alias table sorted). Go map iteration order is
		// nondeterministic, so without this the listing flaps run-to-run.
		showAll := func() {
			names := make([]string, 0, len(r.alias))
			for name := range r.alias {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				show(name, r.alias[name])
			}
		}

		// `alias -p` prints all aliases (same as no args). Reject
		// any other `-X` option with bash 5.3's wording + usage.
		filtered := args
		if len(filtered) > 0 && filtered[0] == "-p" {
			filtered = filtered[1:]
			showAll()
		} else if len(filtered) > 0 && len(filtered[0]) > 1 && filtered[0][0] == '-' && !strings.Contains(filtered[0], "=") {
			r.errf("%salias: %s: invalid option\n", r.bashErrPrefix(pos), filtered[0])
			r.errf("alias: usage: alias [-p] [name[=value] ... ]\n")
			exit.code = 2
			return exit
		}
		if len(args) == 0 {
			showAll()
		}
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
			// though they don't parse standalone. parseAliasBody
			// handles the multi-stmt / per-word / raw fallbacks; the
			// same helper backs BASH_ALIASES[...]= assignments.
			if r.alias == nil {
				r.alias = make(map[string]alias)
			}
			r.alias[name] = parseAliasBody(src)
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
		printActions := false // -P: print only the action string
		dashReset := false    // a bare "-" action (reset to default)
		callback := "-"
	trapFlags:
		for fp.more() {
			switch flag := fp.flag(); flag {
			case "-l":
				listSignals = true
			case "-p":
				printTraps = true
			case "-P":
				printActions = true
			case "-":
				// A bare "-" is the reset-to-default *action*, not a
				// flag; stop flag parsing so it isn't mistaken for the
				// callback of `trap - SIG...`.
				dashReset = true
				break trapFlags
			default:
				r.errf("%strap: %s: invalid option\n", r.bashErrPrefix(pos), flag)
				r.errf("trap: usage: %s\n", bashUsage["trap"])
				exit.code = 2
				return exit
			}
		}
		if listSignals {
			r.printSignalList()
			break
		}
		if printTraps && printActions {
			return failf(2, "trap: cannot specify both -p and -P\n")
		}
		args := fp.args()
		if printActions {
			// `trap -P SIG...` prints only the action for each named
			// signal (no `trap -- ... SIG` wrapper).
			if len(args) == 0 {
				r.errf("%strap: -P requires at least one signal name\n", r.bashErrPrefix(pos))
				r.errf("trap: usage: %s\n", bashUsage["trap"])
				exit.code = 2
				return exit
			}
			for _, a := range args {
				sig := normalizeSignal(a)
				if sig == "" {
					return failf(1, "trap: %s: invalid signal specification\n", a)
				}
				if cb, ok := r.trapCallbacks[sig]; ok && cb != "" {
					r.outf("%s\n", cb)
				}
			}
			break
		}
		if !dashReset && (printTraps || len(args) == 0) {
			// Print traps, optionally filtered by signal names
			filter := make(map[string]bool)
			for _, a := range args {
				sig := normalizeSignal(a)
				if sig == "" {
					return failf(1, "trap: %s: invalid signal specification\n", a)
				}
				filter[sig] = true
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
			// Build the sort order: EXIT (signal 0) first, then
			// every trapped real signal in ascending numeric order
			// (so CHLD, WINCH, etc. — not just 1..15 — are listed),
			// then ERR, DEBUG, RETURN.
			var sigKeys []string
			for sig := range r.trapCallbacks {
				switch sig {
				case "EXIT", "ERR", "DEBUG", "RETURN":
					continue
				}
				sigKeys = append(sigKeys, sig)
			}
			sort.Slice(sigKeys, func(i, j int) bool {
				si, _ := signalByName(sigKeys[i])
				sj, _ := signalByName(sigKeys[j])
				return si < sj
			})
			sigOrder := append([]string{"EXIT"}, sigKeys...)
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
		if len(args) == 1 && args[0] == "" && !dashReset {
			// `trap ''` with no signals is a no-op in bash (empty action,
			// nothing to attach it to).
			break
		}
		reset := false
		switch {
		case dashReset:
			// `trap - SIG...`: the "-" action was already consumed; the
			// remaining args are all signal names to reset.
			reset = true
		case len(args) == 1:
			// 1-arg form resets the named signal to default, but only
			// if the operand actually names a signal. A lone operand
			// that isn't a signal is an action with no signal_spec,
			// which bash reports as a usage error (`trap 512`).
			if normalizeSignal(args[0]) == "" {
				r.errf("trap: usage: %s\n", bashUsage["trap"])
				exit.code = 2
				return exit
			}
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
				r.disableSignalTrap(sig)
			} else {
				if r.trapCallbacks == nil {
					r.trapCallbacks = make(map[string]string)
				}
				r.trapCallbacks[sig] = callback
				// A DEBUG trap set inside a function takes effect for the
				// remainder of that function's commands, even when the
				// frame was not otherwise traced (bash trap.tests: the
				// `func[29] funcdebug` line).
				if sig == "DEBUG" && callback != "" {
					if n := len(r.callStack); n > 0 {
						r.callStack[n-1].debugTrace = true
					}
				}
				// Register an OS signal handler so the signal is
				// delivered to this runner instead of taking its
				// default disposition (which would kill the process).
				// An empty callback means "ignore", which still needs
				// the handler installed to override the default.
				r.enableSignalTrap(sig)
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
			if vr := r.lookupVar(arrayName); vr.Kind == expand.NameRef {
				if _, _, ok := splitArrayRef(vr.Str); ok {
					return failf(1, "%s: `%s': not a valid identifier\n", name, vr.Str)
				}
				if vr.Str == "" {
					// An empty-target nameref can't be followed; bash drops
					// the nameref attribute and turns the variable itself
					// into the array (with a warning) rather than
					// dereferencing to an empty name.
					r.errf("%swarning: %s: removing nameref attribute\n",
						r.bashErrPrefix(r.curStmtPos), arrayName)
				} else {
					arrayName = vr.Str
				}
			}
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
		// mapfile always assigns the array, so it is set even when the
		// input was empty (`declare -a r=()` rather than `declare -a r`).
		var vr expand.Variable
		vr.Kind = expand.Indexed
		vr.Set = true
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
		// Job-control gating must match bash even though subshells here
		// are goroutines: refuse entirely when monitor mode is off, and
		// refuse a job that was backgrounded before job control was
		// enabled (`set +o monitor; cmd &; set -m; fg %1`). Without this,
		// fg would block forever on a job it can never foreground.
		if !r.monitorActive() {
			return failf(1, "fg: no job control\n")
		}
		var bg *bgProc
		jobIdx := 0
		switch {
		case len(args) == 0:
			if len(r.bgProcs) == 0 {
				return failf(1, "fg: no current jobs\n")
			}
			jobIdx = len(r.bgProcs)
		case strings.HasPrefix(args[0], "%"):
			arg := strings.TrimPrefix(args[0], "%")
			switch arg {
			case "%", "+", "":
				if len(r.bgProcs) == 0 {
					return failf(1, "fg: no current jobs\n")
				}
				jobIdx = len(r.bgProcs)
			case "-":
				if len(r.bgProcs) < 2 {
					return failf(1, "fg: no current jobs\n")
				}
				jobIdx = len(r.bgProcs) - 1
			default:
				n := int(atoi(arg))
				if n < 1 || n > len(r.bgProcs) {
					return failf(1, "fg: %%%s: no such job\n", arg)
				}
				jobIdx = n
			}
		default:
			if rest, ok := strings.CutPrefix(args[0], "g"); ok {
				n := int(atoi(rest))
				if n < 1 || n > len(r.bgProcs) {
					return failf(1, "fg: %s: no such job\n", args[0])
				}
				jobIdx = n
			} else {
				pid, perr := strconv.ParseInt(args[0], 10, 64)
				if perr != nil {
					return failf(1, "fg: %s: no such job\n", args[0])
				}
				for i, candidate := range r.bgProcs {
					if candidate.pid.Load() == pid {
						jobIdx = i + 1
						break
					}
				}
				if jobIdx == 0 {
					return failf(1, "fg: pid %s is not a child of this shell\n", args[0])
				}
			}
		}
		bg = r.bgProcs[jobIdx-1]
		if !bg.jobControl {
			return failf(1, "fg: job %d started without job control\n", jobIdx)
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
			return failf(1, "bg: no job control\n")
		}
	case "fc":
		return r.fcBuiltin(ctx, pos, args)
	case "bind":
		// Stub: bind requires readline infrastructure.
	case "history":
		return r.historyBuiltin(pos, args)
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
		for len(args) > 0 {
			if args[0] == "-S" || args[0] == "-p" {
				flag := args[0]
				args = args[1:]
				switch flag {
				case "-S":
					symbolic = true
				case "-p":
					printFlag = true
				}
				continue
			}
			if strings.HasPrefix(args[0], "-") && args[0] != "-" {
				r.errf("%sumask: %s: invalid option\n", r.bashErrPrefix(pos), args[0])
				r.errf("umask: usage: %s\n", bashUsage["umask"])
				exit.code = 2
				return exit
			}
			break
		}
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
				if !syntax.ValidName(name) {
					exit = invalidIdentifier("export", name)
					continue
				}
				if prev := r.lookupVar(name); prev.ReadOnly {
					r.errf("%s%s: readonly variable\n", r.bashErrPrefix(r.curStmtPos), name)
					exit.code = 1
					continue
				}
				val := arg[eqIdx+1:]
				r.setVar(name, expand.Variable{Set: true, Kind: expand.String, Str: val, Exported: true})
			} else {
				if !syntax.ValidName(arg) {
					exit = invalidIdentifier("export", arg)
					continue
				}
				vr := r.lookupVar(arg)
				vr.Exported = true
				r.setVar(arg, vr)
			}
		}
	case "readonly":
		if len(args) == 1 && (args[0] == "-a" || args[0] == "-A") {
			r.printArrayVars(args[0], true, r.opts[optPosix])
			break
		}
		for _, arg := range args {
			eqIdx := strings.IndexByte(arg, '=')
			if eqIdx >= 0 {
				name := arg[:eqIdx]
				if !syntax.ValidName(name) {
					exit = invalidIdentifier("readonly", name)
					continue
				}
				if prev := r.lookupVar(name); prev.ReadOnly {
					r.errf("%s%s: readonly variable\n", r.bashErrPrefix(r.curStmtPos), name)
					exit.code = 1
					continue
				}
				val := arg[eqIdx+1:]
				r.setVar(name, expand.Variable{Set: true, Kind: expand.String, Str: val, ReadOnly: true})
			} else {
				if !syntax.ValidName(arg) {
					exit = invalidIdentifier("readonly", arg)
					continue
				}
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
				if !syntax.ValidName(name) {
					exit = invalidIdentifier("local", name)
					continue
				}
				val := arg[eqIdx+1:]
				r.setVar(name, expand.Variable{Set: true, Kind: expand.String, Str: val, Local: true})
			} else {
				if !syntax.ValidName(arg) {
					exit = invalidIdentifier("local", arg)
					continue
				}
				vr := r.lookupVar(arg)
				vr.Local = true
				r.setVar(arg, vr)
			}
		}
	case "declare", "typeset":
		// Simple declare when called as a command (not keyword).
		// Keyword form is handled by DeclClause in runner.go.
		inFuncScope := r.inFunc || len(r.callStack) > 0
		printMode := false
		exportMode := false
		readonlyMode := false
		namerefMode := false
		arrayMode := ""
		var names []string
		for _, arg := range args {
			if strings.HasPrefix(arg, "-") && !strings.Contains(arg, "=") {
				fp := flagParser{remaining: []string{arg}}
				for fp.more() {
					switch flag := fp.flag(); flag {
					case "-p":
						printMode = true
					case "-x":
						exportMode = true
					case "-r":
						readonlyMode = true
					case "-n":
						namerefMode = true
					case "-a", "-A":
						arrayMode = flag
					default:
						// Other simple-command declare flags are handled by
						// DeclClause in the common path; keep this fallback
						// narrowly scoped to the inline-assignment cases.
					}
				}
				continue
			}
			eqIdx := strings.IndexByte(arg, '=')
			if eqIdx >= 0 {
				name := arg[:eqIdx]
				val := arg[eqIdx+1:]
				vr := expand.Variable{Set: true, Kind: expand.String, Str: val}
				prev := r.lookupVar(name)
				vr.Exported = prev.Exported || exportMode
				if readonlyMode {
					vr.ReadOnly = true
				}
				if inFuncScope {
					vr.Local = true
				}
				r.setVar(name, vr)
				names = append(names, name)
			} else if namerefMode {
				// `VAR=val declare -n NAME` (and other CallExpr-form
				// declares): adding the nameref attribute re-validates
				// NAME's current value as a reference target, matching
				// the keyword DeclClause path (nameref11.sub line 22).
				names = append(names, arg)
				vr := r.lookupVar(arg)
				if vr.Kind == expand.String && (vr.Str != "" || vr.Set) && !validNameRefTarget(vr.Str) {
					r.errf("%s%s: `%s': invalid variable name for name reference\n",
						r.bashErrPrefix(pos), name, vr.Str)
					exit.code = 1
					continue
				}
				vr.Kind = expand.NameRef
				vr.Exported = vr.Exported || exportMode
				vr.ReadOnly = vr.ReadOnly || readonlyMode
				vr.Local = vr.Local || inFuncScope
				r.setVar(arg, vr)
			} else {
				names = append(names, arg)
				if exportMode || readonlyMode || inFuncScope {
					vr := r.lookupVar(arg)
					vr.Exported = vr.Exported || exportMode
					vr.ReadOnly = vr.ReadOnly || readonlyMode
					vr.Local = vr.Local || inFuncScope
					r.setVar(arg, vr)
				}
			}
		}
		if len(names) == 0 && arrayMode != "" {
			r.printArrayVars(arrayMode, readonlyMode, false)
			break
		}
		if printMode {
			for _, name := range names {
				vr := r.lookupVar(name)
				if !vr.Declared() {
					r.errf(r.bashErrPrefix(pos)+"declare: %s: not found\n", name)
					exit.code = 1
					continue
				}
				if vr.Kind == expand.NameRef && vr.Integer && vr.Set && vr.Str == "" {
					// An integer nameref whose value assignment was
					// rejected (`declare -i foo; foo=7*6`) stays invisible
					// in bash: `declare -p` skips it silently. See the
					// matching DeclClause path in runner.go.
					continue
				}
				r.outf("%s\n", formatDeclareVar(name, vr, false))
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

// resolveJobArg maps a `wait`/`wait -n` argument to a background job:
// `%N` job-spec, `gN` legacy `$!` sentinel, or a real OS PID (also a
// coproc's synthetic `<NAME>_PID`). Returns nil when nothing matches.
func (r *Runner) resolveJobArg(arg string) *bgProc {
	if rest, ok := strings.CutPrefix(arg, "%"); ok {
		switch rest {
		case "%", "+", "":
			// current job (most recent)
			if len(r.bgProcs) == 0 {
				return nil
			}
			return r.bgProcs[len(r.bgProcs)-1]
		case "-":
			// previous job
			if len(r.bgProcs) < 2 {
				return nil
			}
			return r.bgProcs[len(r.bgProcs)-2]
		}
		n := int(atoi(rest))
		if n < 1 || n > len(r.bgProcs) {
			return nil
		}
		return r.bgProcs[n-1]
	}
	if rest, ok := strings.CutPrefix(arg, "g"); ok {
		n := int(atoi(rest))
		if n < 1 || n > len(r.bgProcs) {
			return nil
		}
		return r.bgProcs[n-1]
	}
	pid, perr := strconv.ParseInt(arg, 10, 64)
	if perr != nil {
		return nil
	}
	for _, candidate := range r.bgProcs {
		if candidate.pid.Load() == pid ||
			(candidate.coprocPid == pid && candidate.coprocPidVar != "") {
			return candidate
		}
	}
	return nil
}

// storeWaitPid implements `wait -p VAR`: store the finishing job's PID
// into VAR. The value mirrors `$!` — the real OS PID when one was
// published, else the legacy `g<N>` sentinel. VAR may be an array
// element (e.g. A[k]); setVarString routes that through the array path.
func (r *Runner) storeWaitPid(pidVar string, bg *bgProc) {
	if pidVar == "" || bg == nil {
		return
	}
	if bg.pidReady != nil {
		<-bg.pidReady
	}
	var val string
	if pid := bg.pid.Load(); pid > 0 {
		val = strconv.FormatInt(pid, 10)
	} else {
		for i, candidate := range r.bgProcs {
			if candidate == bg {
				val = "g" + strconv.Itoa(i+1)
				break
			}
		}
	}
	r.setVarString(pidVar, val)
}

// waitAnyDone blocks until the first of the given background jobs
// finishes and returns it (nil if the slice is empty).
func waitAnyDone(procs []*bgProc) *bgProc {
	if len(procs) == 0 {
		return nil
	}
	cases := make([]reflect.SelectCase, len(procs))
	for i, bg := range procs {
		cases[i] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(bg.done)}
	}
	chosen, _, _ := reflect.Select(cases)
	return procs[chosen]
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

// innerArithText extracts the body of the first $((...)) or $[...]
// in line, returning ok=false when neither is present.
func innerArithText(line string) (string, bool) {
	if i := strings.Index(line, "$(("); i >= 0 {
		if j := strings.Index(line[i:], "))"); j >= 0 {
			return strings.TrimSpace(line[i+3 : i+j]), true
		}
	}
	if i := strings.Index(line, "$["); i >= 0 {
		if j := strings.IndexByte(line[i:], ']'); j >= 0 {
			return strings.TrimSpace(line[i+2 : i+j]), true
		}
	}
	return "", false
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
	"builtin":  "builtin [shell-builtin [arg ...]]",
	"cd":       "cd [-L|[-P [-e]] [-@]] [dir]",
	".":        ". [-p path] filename [arguments]",
	"command":  "command [-pVv] command [arg ...]",
	"complete": "complete [-abcdefgjksuv] [-pr] [-DEI] [-o option] [-A action] [-G globpat] [-W wordlist] [-F function] [-C command] [-X filterpat] [-P prefix] [-S suffix] [name ...]",
	"compgen":  "compgen [-V varname] [-abcdefgjksuv] [-o option] [-A action] [-G globpat] [-W wordlist] [-F function] [-C command] [-X filterpat] [-P prefix] [-S suffix] [word]",
	"continue": "continue [n]",
	"declare":  "declare [-aAfFgiIlnrtux] [name[=value] ...] or declare -p [-aAfFilnrtux] [name ...]",
	"disown":   "disown [-h] [-ar] [jobspec ... | pid ...]",
	"enable":   "enable [-a] [-dnps] [-f filename] [name ...]",
	"eval":     "eval [arg ...]",
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

func bashHelpOverview() string {
	return `GNU bash, version 5.3.0(1)-release (x86_64-apple-darwin24.0.0)
These shell commands are defined internally.  Type ` + "`help'" + ` to see this list.
Type ` + "`help name'" + ` to find out more about the function ` + "`name'" + `.
Use ` + "`info bash'" + ` to find out more about the shell in general.
Use ` + "`man -k'" + ` or ` + "`info'" + ` to find out more about commands not in this list.

A star (*) next to a name means that the command is disabled.

 ! PIPELINE                              history [-c] [-d offset] [n] or hist>
 job_spec [&]                            if COMMANDS; then COMMANDS; [ elif C>
 (( expression ))                        jobs [-lnprs] [jobspec ...] or jobs >
 . [-p path] filename [arguments]        kill [-s sigspec | -n signum | -sigs>
 :                                       let arg [arg ...]
 [ arg... ]                              local [option] name[=value] ...
 [[ expression ]]                        logout [n]
 alias [-p] [name[=value] ... ]          mapfile [-d delim] [-n count] [-O or>
 bg [job_spec ...]                       popd [-n] [+N | -N]
 bind [-lpsvPSVX] [-m keymap] [-f file>  printf [-v var] format [arguments]
 break [n]                               pushd [-n] [+N | -N | dir]
 builtin [shell-builtin [arg ...]]       pwd [-LP]
 caller [expr]                           read [-Eers] [-a array] [-d delim] [>
 case WORD in [PATTERN [| PATTERN]...)>  readarray [-d delim] [-n count] [-O >
 cd [-L|[-P [-e]]] [-@] [dir]            readonly [-aAf] [name[=value] ...] o>
 command [-pVv] command [arg ...]        return [n]
 compgen [-V varname] [-abcdefgjksuv] >  select NAME [in WORDS ... ;] do COMM>
 complete [-abcdefgjksuv] [-pr] [-DEI]>  set [-abefhkmnptuvxBCEHPT] [-o optio>
 compopt [-o|+o option] [-DEI] [name .>  shift [n]
 continue [n]                            shopt [-pqsu] [-o] [optname ...]
 coproc [NAME] command [redirections]    source [-p path] filename [argument>
 declare [-aAfFgiIlnrtux] [name[=value>  suspend [-f]
 dirs [-clpv] [+N] [-N]                  test [expr]
 disown [-h] [-ar] [jobspec ... | pid >  time [-p] pipeline
 echo [-neE] [arg ...]                   times
 enable [-a] [-dnps] [-f filename] [na>  trap [-Plp] [[action] signal_spec ..>
 eval [arg ...]                          true
 exec [-cl] [-a name] [command [argume>  type [-afptP] name [name ...]
 exit [n]                                typeset [-aAfFgiIlnrtux] name[=value>
 export [-fn] [name[=value] ...] or ex>  ulimit [-SHabcdefiklmnpqrstuvxPRT] [>
 false                                   umask [-p] [-S] [mode]
 fc [-e ename] [-lnr] [first] [last] o>  unalias [-a] name [name ...]
 fg [job_spec]                           unset [-f] [-v] [-n] [name ...]
 for NAME [in WORDS ... ] ; do COMMAND>  until COMMANDS; do COMMANDS-2; done
 for (( exp1; exp2; exp3 )); do COMMAN>  variables - Names and meanings of so>
 function name { COMMANDS ; } or name >  wait [-fn] [-p var] [id ...]
 getopts optstring name [arg ...]        while COMMANDS; do COMMANDS-2; done
 hash [-lr] [-p pathname] [-dt] [name >  { COMMANDS ; }
 help [-dms] [pattern ...]
`
}

func bashHelpShiftLong() string {
	return `shift: shift [n]
    Shift positional parameters.
    
    Rename the positional parameters $N+1,$N+2 ... to $1,$2 ...  If N is
    not given, it is assumed to be 1.
    
    Exit Status:
    Returns success unless N is negative or greater than $#.
`
}

func (r *Runner) helpTopic(pattern, mode string) bool {
	switch mode {
	case "description":
		switch pattern {
		case "shift":
			r.outf("shift - Shift positional parameters.\n")
			return true
		}
	case "man":
		switch pattern {
		case ":":
			r.outf(`NAME
    : - Null command.

SYNOPSIS
    :

DESCRIPTION
    Null command.
    
    No effect; the command does nothing.
    
    Exit Status:
    Always succeeds.

SEE ALSO
    bash(1)

IMPLEMENTATION
    Copyright (C) 2025 Free Software Foundation, Inc.

`)
			return true
		}
	case "short":
		switch pattern {
		case "help":
			r.outf("help: help [-dms] [pattern ...]\n")
			return true
		case "builtin":
			r.outf("builtin: builtin [shell-builtin [arg ...]]\n")
			return true
		case "shift":
			r.outf("shift: shift [n]\n")
			return true
		case "read*":
			r.outf("Shell commands matching keyword `read*'\n\n")
			r.outf("%s", bashHelpReadShort())
			return true
		case "rea":
			r.outf("%s", bashHelpReadShort())
			return true
		}
	default:
		switch pattern {
		case ":":
			r.outf(`:: :
    Null command.
    
    No effect; the command does nothing.
    
    Exit Status:
    Always succeeds.
`)
			return true
		}
	}
	if mode == "" && IsBuiltin(pattern) {
		r.outf("%s: %s is a shell builtin\n", pattern, pattern)
		return true
	}
	return false
}

func bashHelpReadShort() string {
	return `read: read [-Eers] [-a array] [-d delim] [-i text] [-n nchars] [-N nchars] [-p prompt] [-t timeout] [-u fd] [name ...]
readarray: readarray [-d delim] [-n count] [-O origin] [-s count] [-t] [-u fd] [-C callback] [-c quantum] [array]
readonly: readonly [-aAf] [name[=value] ...] or readonly -p
`
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
		ms = append(ms, typeMatch{
			kind: "alias",
			desc: fmt.Sprintf("%s is aliased to `%s'", arg, aliasValue(als)),
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
// `r`, `w`, `x`, or permission-copy tokens `u`, `g`, `o`. Additional
// operators may appear in the permissions tail, e.g. `u+w=r+x`; they
// are applied left-to-right. On failure, returns kind="" for "looks
// octal" so caller can try numeric parse, or "character"/"operator"
// with the offending byte for a bash-shaped diagnostic.
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
		apply := func(op byte, perms int) {
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
		for i < len(clause) {
			i++
			perms := 0
			for ; i < len(clause); i++ {
				switch clause[i] {
				case '=', '+', '-':
					goto applySegment
				case 'r':
					perms |= 4
				case 'w':
					perms |= 2
				case 'x':
					perms |= 1
				case 'u':
					perms |= (^mask >> 6) & 7
				case 'g':
					perms |= (^mask >> 3) & 7
				case 'o':
					perms |= (^mask >> 0) & 7
				case 's', 't', 'X':
					// Ignore permission bits that don't fit in umask.
				default:
					return symbolicUmaskResult{badChar: clause[i], kind: "character"}
				}
			}
		applySegment:
			apply(op, perms)
			if i < len(clause) {
				op = clause[i]
				if op != '=' && op != '+' && op != '-' {
					return symbolicUmaskResult{badChar: op, kind: "operator"}
				}
			}
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
func (r *Runner) ulimitBuiltin(pos syntax.Pos, args []string) exitStatus {
	var exit exitStatus
	flag := "-f"
	var setVal string
	parsingOpts := true
	for _, a := range args {
		if parsingOpts && a == "--" {
			parsingOpts = false
			continue
		}
		if parsingOpts && len(a) > 1 && a[0] == '-' {
			for _, ch := range a[1:] {
				switch ch {
				case 'S', 'H':
					// Soft/hard selectors affect real setrlimit calls.
					// We keep cosmetic state only, so the resource flag
					// remains unchanged.
				case 'a':
					return exit
				case 'g':
					r.errf("%sulimit: -g: invalid option\n", r.bashErrPrefix(pos))
					r.errf("ulimit: usage: ulimit [-SHabcdefiklmnpqrstuvxPRT] [limit]\n")
					exit.code = 2
					return exit
				default:
					flag = "-" + string(ch)
				}
			}
			continue
		}
		setVal = a
	}
	if setVal != "" {
		if strings.HasPrefix(setVal, "+") {
			r.errf("%sulimit: %s: invalid number\n", r.bashErrPrefix(pos), setVal)
			exit.code = 1
			return exit
		}
		if flag == "-u" {
			r.errf("%sulimit: max user processes: cannot modify limit: Operation not permitted\n", r.bashErrPrefix(pos))
			exit.code = 1
			return exit
		}
		if setVal == "soft" || setVal == "hard" {
			return exit
		}
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
		// nofileLimit is per-platform: getrlimit on unix, "unlimited"
		// elsewhere (Windows has no fd rlimit concept).
		if cur, ok := nofileLimit(); ok {
			r.outf("%d\n", cur)
		} else {
			r.outf("unlimited\n")
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
// name to a width-character field. Bash 5.3 pads `shopt` to 20 and
// `set -o`/`shopt -o` to 15 (both encoded in the shopt fixture).
// Names longer than the field get no padding.
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
	return r.readLineFrom(ctx, r.stdin, raw, delim)
}

func (r *Runner) readLineFrom(ctx context.Context, stdin io.Reader, raw bool, delim byte) ([]byte, error) {
	var line []byte
	esc := false

	if file, ok := stdin.(*os.File); ok {
		stopc := make(chan struct{})
		stop := context.AfterFunc(ctx, func() {
			file.SetReadDeadline(time.Now())
			close(stopc)
		})
		defer func() {
			if !stop() {
				// The AfterFunc was started.
				// Wait for it to complete, and reset the file's deadline.
				<-stopc
				file.SetReadDeadline(time.Time{})
			}
		}()
	} else {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	for {
		var buf [1]byte
		n, err := stdin.Read(buf[:])
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

func (r *Runner) cdpath(ctx context.Context, path string) (string, bool, bool) {
	if path == "" || filepath.IsAbs(path) || strings.ContainsRune(path, filepath.Separator) {
		return "", false, false
	}
	cdpath := r.envGet("CDPATH")
	if cdpath == "" {
		return "", false, false
	}
	for _, elem := range strings.Split(cdpath, ":") {
		base := elem
		if base == "" {
			base = "."
		}
		candidate := absPath(r.absPath(base), path)
		info, err := r.stat(ctx, candidate)
		if err == nil && info.IsDir() && r.access(ctx, candidate, access_X_OK) == nil {
			return candidate, elem != "" && elem != ".", true
		}
	}
	return "", false, false
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

func (r *Runner) printSignalList() {
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
