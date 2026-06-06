// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"iter"
	"maps"
	"math"
	mathrand "math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/internal"
	"mvdan.cc/sh/v3/pattern"
	"mvdan.cc/sh/v3/syntax"
)

const (
	// shellReplyPS3Var, or PS3, is a special variable in Bash used by the select command,
	// while the shell is awaiting for input. the default value is [shellDefaultPS3]
	shellReplyPS3Var = "PS3"
	// shellDefaultPS3, or #?, is PS3's default value
	shellDefaultPS3 = "#? "
	// shellReplyVar, or REPLY, is a special variable in Bash that is used to store the result of
	// the select command or of the read command, when no variable name is specified
	shellReplyVar = "REPLY"

	// `sh-np` matches bash's own naming convention for procsub
	// named pipes; bash's test fixtures (procsub.tests) `case
	// "$1" in *sh-np*) ...` look for this substring to fall into a
	// hard-coded "file has been consumed" branch.
	fifoNamePrefix = "sh-np-"
)

func (r *Runner) fillExpandConfig(ctx context.Context) {
	r.ectx = ctx
	r.ecfg = &expand.Config{
		Env: expandEnv{r},
		OnFormatWarning: func(msg string) {
			// Bash 5.3 printf: numeric-conversion failures
			// (e.g. `printf %d xyz`) print the warning and set
			// exit status 1 but keep formatting (the value falls
			// back to 0). The exit-code wire-up lives in the
			// printf builtin which checks r.lastExpandExit.
			r.errf("%s%s\n", r.bashErrPrefix(r.curStmtPos), msg)
			r.lastExpandExit = exitStatus{code: 1}
		},
		CmdSubst: func(w io.Writer, cs *syntax.CmdSubst) error {
			switch len(cs.Stmts) {
			case 0: // nothing to do
				return nil
			case 1: // $(<file)
				word := catShortcutArg(cs.Stmts[0])
				if word == nil {
					break
				}
				path := r.literal(word)
				f, err := r.open(ctx, path, os.O_RDONLY, 0, true)
				if err != nil {
					return err
				}
				_, err = io.Copy(w, f)
				f.Close()
				return err
			}
			// Bash 5.3 funsub `${ cmd; }` (cs.TempFile) and valsub
			// `${|cmd;}` (cs.ReplyVar) run the body in the *caller's*
			// scope — variable assignments inside the substitution
			// persist. Regular `$(...)` uses a subshell.
			//
			// Important: capture into an independent buffer rather
			// than writing straight to w. The w supplied by
			// expand.cmdSubst is the shared bufferAlloc; r.fields()
			// invocations inside the body would otherwise interleave
			// their workspace usage with our capture and double up.
			if cs.TempFile || cs.ReplyVar {
				var captureBuf bytes.Buffer
				oldStdout := r.stdout
				r.stdout = &captureBuf
				// Bash 5.3: "Any variable assignments performed during
				// the execution of the [funsub] command list are local
				// to the command list and lost after the substitution
				// is performed." So the body runs in a function-like
				// variable scope (return / local are legal, plain
				// assignments don't leak), but in the *same process*
				// (no subshell — unlike $(...) which forks).
				oldInFunc := r.inFunc
				r.inFunc = true
				origEnv := r.writeEnv
				r.writeEnv = &overlayEnviron{parent: r.writeEnv, funcScope: true, funsubScope: true}
				r.stmts(ctx, cs.Stmts)
				r.writeEnv = origEnv
				r.inFunc = oldInFunc
				r.stdout = oldStdout
				// `return` is local to the funsub body (same as a
				// function); `exit` propagates out (kills the shell)
				// — bash 5.3 treats funsub like a function body for
				// scoping but does NOT swallow `exit`.
				r.exit.returning = false
				// Stash the funsub's exit status the same way `$(…)`
				// does so the assignment path's "if exit.ok() restore
				// lastExpandExit" recovery doesn't wipe our exiting
				// flag for `exit 0` inside the body.
				r.lastExpandExit = r.exit
				// w is expand.cmdSubst's shared bufferAlloc; reset it
				// to discard any residue that r.fields() left there
				// during the body run, then deposit our captured output.
				if sb, ok := w.(*strings.Builder); ok {
					sb.Reset()
				}
				w.Write(captureBuf.Bytes())
				if cs.ReplyVar {
					s := strings.TrimRight(captureBuf.String(), "\n")
					r.setVarString(shellReplyVar, s)
				}
				return nil
			}
			r2 := r.subshell(false)
			r2.stdout = w
			// inherit_errexit: bash command substitutions do NOT
			// inherit `set -e` by default — `$(false; echo ok)`
			// must echo `ok` even when the caller is under -e.
			// When inherit_errexit is enabled (or implicitly via
			// POSIX mode), copy -e through to the subshell.
			inheritErrexit := false
			if opt, _ := r.bashOptByName("inherit_errexit"); opt != nil && *opt {
				inheritErrexit = true
			}
			if r.opts[optPosix] {
				inheritErrexit = true
			}
			if inheritErrexit {
				r2.opts[optErrExit] = r.opts[optErrExit]
			} else {
				r2.opts[optErrExit] = false
			}
			r2.stmts(ctx, cs.Stmts)
			r2.exit.exiting = false   // subshells don't exit the parent shell
			r2.exit.returning = false // and they swallow `return` locally
			r.lastExpandExit = r2.exit
			if r2.exit.fatalExit {
				return r2.exit.err // surface fatal errors immediately
			}
			return nil
		},
		ProcSubst: func(ps *syntax.ProcSubst) (string, error) {
			if runtime.GOOS == "windows" {
				return "", fmt.Errorf("TODO: support process substitution on Windows")
			}
			if len(ps.Stmts) == 0 { // nothing to do
				return os.DevNull, nil
			}

			// We can't atomically create a random unused temporary FIFO.
			// Similar to [os.CreateTemp],
			// keep trying new random paths until one does not exist.
			// We use a uint64 because a uint32 easily runs into retries.
			var path string
			try := 0
			for {
				path = filepath.Join(r.tempDir, fifoNamePrefix+strconv.FormatUint(mathrand.Uint64(), 16))
				err := mkfifo(path, 0o666)
				if err == nil {
					break
				}
				if !os.IsExist(err) {
					return "", fmt.Errorf("cannot create fifo: %v", err)
				}
				if try++; try > 100 {
					return "", fmt.Errorf("giving up at creating fifo: %v", err)
				}
			}

			r2 := r.subshell(true)
			stdout := r.origStdout
			// TODO: note that `man bash` mentions that `wait` only waits for the last
			// process substitution as long as it is $!; the logic here would mean we wait for all of them.
			bg := &bgProc{
				done:     make(chan struct{}),
				exit:     new(exitStatus),
				pidReady: make(chan struct{}),
			}
			r.bgProcs = append(r.bgProcs, bg)
			go func() {
				defer func() {
					*bg.exit = r2.exit
					close(bg.done)
					select {
					case <-bg.pidReady:
					default:
						close(bg.pidReady)
					}
				}()
				switch ps.Op {
				case syntax.CmdIn:
					f, err := os.OpenFile(path, os.O_WRONLY, 0)
					if err != nil {
						r.errf("cannot open fifo for stdout: %v\n", err)
						return
					}
					r2.stdout = f
					defer func() {
						if err := f.Close(); err != nil {
							r.errf("closing stdout fifo: %v\n", err)
						}
						os.Remove(path)
					}()
				case syntax.CmdOut:
					f, err := os.OpenFile(path, os.O_RDONLY, 0)
					if err != nil {
						r.errf("cannot open fifo for stdin: %v\n", err)
						return
					}
					r2.stdin = f
					r2.stdout = stdout

					defer func() {
						f.Close()
						os.Remove(path)
					}()
				default:
					// Should only happen if we forgot a case above.
					panic(fmt.Sprintf("unexpected process substitution operator: %q", ps.Op))
				}
				r2.stmts(ctx, ps.Stmts)
				r2.exit.exiting = false // subshells don't exit the parent shell
			}()
			return path, nil
		},
	}
	r.ecfg.PromptExpand = r.promptExpand
	r.ecfg.StartTime = r.startTime
	r.updateExpandOpts()
}

// catShortcutArg checks if a statement is of the form "$(<file)". The redirect
// word is returned if there's a match, and nil otherwise.
func catShortcutArg(stmt *syntax.Stmt) *syntax.Word {
	if stmt.Cmd != nil || stmt.Negated || stmt.Background || stmt.Coprocess || stmt.Disown {
		return nil
	}
	if len(stmt.Redirs) != 1 {
		return nil
	}
	redir := stmt.Redirs[0]
	if redir.Op != syntax.RdrIn {
		return nil
	}
	return redir.Word
}

func (r *Runner) updateExpandOpts() {
	if r.opts[optNoGlob] {
		r.ecfg.ReadDir2 = nil
	} else {
		r.ecfg.ReadDir2 = func(s string) ([]fs.DirEntry, error) {
			return r.readDirHandler(r.handlerCtx(r.ectx, handlerKindReadDir, todoPos), s)
		}
	}
	r.ecfg.GlobStar = r.opts[optGlobStar]
	r.ecfg.DotGlob = r.opts[optDotGlob]
	r.ecfg.NoCaseGlob = r.opts[optNoCaseGlob]
	r.ecfg.NullGlob = r.opts[optNullGlob]
	r.ecfg.NoUnset = r.opts[optNoUnset]
	r.ecfg.ExtGlob = r.opts[optExtGlob]
	r.ecfg.Posix = r.opts[optPosix]
}

func (r *Runner) expandErr(err error) {
	if err == nil {
		return
	}
	errMsg := err.Error()
	fmt.Fprintln(r.stderr, errMsg)
	switch {
	case errors.As(err, &expand.UnsetParameterError{}):
	case errMsg == "invalid indirect expansion":
		// TODO: These errors are treated as fatal by bash.
		// Make the error type reflect that.
	default:
		return // other cases do not exit
	}
	r.exit.code = 1
	r.exit.exiting = true
}

func (r *Runner) arithm(expr syntax.ArithmExpr) int {
	n, err := expand.Arithm(r.ecfg, expr)
	if err != nil && r.bashCompatErrors {
		err = r.bashArithmError(expr, err)
	}
	r.lastArithErr = err
	r.expandErr(err)
	return n
}

// bashArithmError reformats an arithmetic-evaluation error so it
// matches bash 5.3's diagnostic shape:
//
//	<file>: line N: ((: <expr> : <message> (error token is "<token> ")
//
// The offending token is approximated as the right-hand side of a
// division/remainder when the error is "division by zero"; otherwise
// it's the whole expression. Bash includes a trailing space inside the
// quoted token — preserved here so tests diff cleanly.
func (r *Runner) bashArithmError(expr syntax.ArithmExpr, err error) error {
	msg := err.Error()
	bashMsg := msg
	switch {
	case strings.Contains(msg, "division by zero"), strings.Contains(msg, "division by 0"):
		bashMsg = "division by 0"
	default:
		// Other errors keep their wording; still wrap with the file
		// prefix and ((: ...) frame so they're parseable.
	}
	// Printer.Print doesn't accept bare ArithmExpr nodes; wrap in an
	// ArithmCmd via a Stmt so the printer's command path handles it,
	// then strip the surrounding "(( ... ))" and any escaped-newline
	// continuations the printer inserts for multi-line layout.
	printArithm := func(e syntax.ArithmExpr) string {
		var b strings.Builder
		syntax.NewPrinter().Print(&b, &syntax.Stmt{
			Cmd: &syntax.ArithmCmd{X: e},
		})
		s := b.String()
		s = strings.TrimSpace(s)
		s = strings.TrimPrefix(s, "((")
		s = strings.TrimSuffix(s, "))")
		// Collapse "\\\n" (printer's line-continuation) and any
		// surrounding whitespace into a single space.
		s = strings.ReplaceAll(s, "\\\n", " ")
		s = strings.Join(strings.Fields(s), " ")
		return s
	}
	exprText := printArithm(expr)

	tokenText := "0"
	if b, ok := expr.(*syntax.BinaryArithm); ok {
		switch b.Op {
		case syntax.Quo, syntax.Rem, syntax.QuoAssgn, syntax.RemAssgn:
			tokenText = printArithm(b.Y)
		}
	}
	prefix := r.filename
	if prefix == "" {
		prefix = "bashy"
	}
	// If the inner message already carries its own "(error token is ...)"
	// suffix (e.g. the "attempted assignment to non-variable" path for
	// `7++` / `7=4`), skip appending our outer copy — bash emits one
	// instance, not two.
	if strings.Contains(bashMsg, "error token is") {
		return fmt.Errorf("%s: line %d: ((: %s : %s",
			prefix, expr.Pos().Line(), exprText, bashMsg)
	}
	return fmt.Errorf("%s: line %d: ((: %s : %s (error token is \"%s \")",
		prefix, expr.Pos().Line(), exprText, bashMsg, tokenText)
}

func (r *Runner) fields(words ...*syntax.Word) []string {
	strs, err := expand.Fields(r.ecfg, words...)
	r.expandErr(err)
	return strs
}

func (r *Runner) literal(word *syntax.Word) string {
	str, err := expand.Literal(r.ecfg, word)
	r.expandErr(err)
	return str
}

func (r *Runner) document(word *syntax.Word) string {
	str, err := expand.Document(r.ecfg, word)
	r.expandErr(err)
	return str
}

func (r *Runner) pattern(word *syntax.Word) string {
	str, err := expand.Pattern(r.ecfg, word)
	r.expandErr(err)
	return str
}

// expandEnviron exposes [Runner]'s variables to the expand package.
type expandEnv struct {
	r *Runner
}

var _ expand.WriteEnviron = expandEnv{}

func (e expandEnv) Get(name string) expand.Variable {
	return e.r.lookupVar(name)
}

func (e expandEnv) Set(name string, vr expand.Variable) error {
	e.r.setVar(name, vr)
	return nil // TODO: return any errors
}

func (e expandEnv) Each(fn func(name string, vr expand.Variable) bool) {
	e.r.writeEnv.Each(fn)
}

var todoPos syntax.Pos // for handlerCtx callers where we don't yet have a position

func (r *Runner) handlerCtx(ctx context.Context, kind handlerKind, pos syntax.Pos) context.Context {
	hc := HandlerContext{
		runner: r,
		kind:   kind,
		Env:    &overlayEnviron{parent: r.writeEnv},
		Dir:    r.Dir,
		Pos:    pos,
		Stdout: r.stdout,
		Stderr: r.stderr,
	}
	if r.stdin != nil { // do not leave hc.Stdin as a typed nil
		hc.Stdin = r.stdin
	}
	return context.WithValue(ctx, handlerCtxKey{}, hc)
}

func (r *Runner) out(s string) {
	io.WriteString(r.stdout, s)
}

func (r *Runner) outf(format string, a ...any) {
	fmt.Fprintf(r.stdout, format, a...)
}

func (r *Runner) errf(format string, a ...any) {
	fmt.Fprintf(r.stderr, format, a...)
}

// pureLiteral reports whether all parts of word are literal /
// quoted-literal tokens (no parameter / command / arithmetic /
// process substitution). Used by xtrace formatting to decide
// between "render the source" (`$@`, `$(foo)`) and "expand to a
// value and re-quote" (`$' '`, `\|`).
func pureLiteral(word *syntax.Word) bool {
	if word == nil {
		return true
	}
	for _, p := range word.Parts {
		switch p.(type) {
		case *syntax.Lit, *syntax.SglQuoted:
			// always literal
		case *syntax.DblQuoted:
			dq := p.(*syntax.DblQuoted)
			for _, ip := range dq.Parts {
				switch ip.(type) {
				case *syntax.Lit:
				default:
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

// traceArrayLiteral renders an array assignment for `set -x` in
// bash 5.3's format: each element re-quoted independently via
// `xtraceQuote`, preserving bash-style minimality (single quotes
// where possible; backslash-escapes for single metacharacters).
func traceArrayLiteral(t *tracer, name, op string, elems []*syntax.ArrayElem, r *Runner) {
	t.stringf("%s%s(", name, op)
	for i, el := range elems {
		if i > 0 {
			t.string(" ")
		}
		if el.Index != nil {
			var buf bytes.Buffer
			syntax.NewPrinter(syntax.SingleLine(true)).Print(&buf, &syntax.Word{Parts: []syntax.WordPart{
				&syntax.Lit{Value: "[" + r.literal(el.Index.(*syntax.Word)) + "]"},
			}})
			t.string(buf.String() + "=")
		}
		// bash xtrace re-quotes purely-literal elements but
		// keeps parameter expansions / command subs / arithmetic
		// in their original source form (`$@`, `$(foo)`, …).
		if pureLiteral(el.Value) {
			val, _ := expand.LiteralWithQuoteRemoval(r.ecfg, el.Value)
			t.string(xtraceQuote(val))
		} else {
			var buf bytes.Buffer
			syntax.NewPrinter(syntax.SingleLine(true)).Print(&buf, el.Value)
			t.string(buf.String())
		}
	}
	t.string(")")
}

// xtraceQuote renders s the way bash 5.3 prints command/argument
// values in xtrace output: bare if no shell-meta chars are
// present, backslash-escape for a single metacharacter,
// single-quote (with literal tab/newline inside) for anything
// else with no embedded `'`, otherwise fall back to the default
// `syntax.Quote`.
func xtraceQuote(s string) string {
	if s == "" {
		return "''"
	}
	// Single metacharacter → backslash-escape: `\|`, `\&`, `\;`,
	// `\(`, `\)`, `\<`, `\>`. Bash xtrace picks this form for
	// single-byte shell metas.
	if len(s) == 1 {
		switch s[0] {
		case '|', '&', ';', '(', ')', '<', '>':
			return `\` + s
		}
	}
	// Bare if no chars need quoting.
	needsQuote := false
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r', '"', '\'', '\\', '`',
			'$', '|', '&', ';', '(', ')', '<', '>',
			'*', '?', '[', '{', '!', '#', '~', '=':
			needsQuote = true
		}
		if !unicodeIsPrint(r) && r != '\t' && r != '\n' {
			needsQuote = true
		}
		if needsQuote {
			break
		}
	}
	if !needsQuote {
		return s
	}
	// Otherwise wrap in single quotes if the value has no embedded
	// `'` and no truly unprintable runes; bash uses literal
	// control chars inside single quotes (`'<TAB>'`, `'<NL>'`).
	if !strings.ContainsRune(s, '\'') {
		hasUnprintable := false
		for _, r := range s {
			if r != '\t' && r != '\n' && !unicodeIsPrint(r) {
				hasUnprintable = true
				break
			}
		}
		if !hasUnprintable {
			return "'" + s + "'"
		}
	}
	q, err := syntax.Quote(s, syntax.LangBash)
	if err != nil {
		return s
	}
	return q
}

func unicodeIsPrint(r rune) bool {
	return unicode.IsPrint(r)
}

// compactArithm strips bash-`set -x`-style padding from a printer-
// rendered arithmetic expression: drops spaces around `=`, `+=`,
// `-=`, etc., and around comparison/logical operators so the
// traced form reads `i=0`, `i<5`, `i++` rather than the shfmt
// `i = 0`, `i < 5`, `i ++` rendering. Conservative — leaves
// other whitespace alone.
func compactArithm(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' && i+1 < len(s) {
			// drop space before `=`, `+`, `-`, `*`, `/`, `%`,
			// `<`, `>`, `!`, `&`, `|`, `^`, `?`, `:`, `,` —
			// the operators bash's xtrace renders unspaced.
			n := s[i+1]
			switch n {
			case '=', '+', '-', '*', '/', '%', '<', '>', '!', '&', '|', '^', '?', ':', ',':
				continue
			}
		}
		if c == ' ' && len(out) > 0 {
			// drop space after operator chars (the matching
			// side of the rule above).
			switch out[len(out)-1] {
			case '=', '+', '-', '*', '/', '%', '<', '>', '!', '&', '|', '^', '?', ':', ',':
				continue
			}
		}
		out = append(out, c)
	}
	return string(out)
}

// applyCaseAttr folds the variable's value in place when its
// case-modification attributes (`declare -u/-l/-c`) are set.
// Operates on String, Indexed and Associative kinds; the
// scalar / per-element value gets folded.
func applyCaseAttr(vr *expand.Variable) {
	if !(vr.Upper || vr.Lower || vr.Capitalize) {
		return
	}
	fold := func(s string) string {
		switch {
		case vr.Upper:
			return strings.ToUpper(s)
		case vr.Lower:
			return strings.ToLower(s)
		case vr.Capitalize:
			if s == "" {
				return s
			}
			rs := []rune(s)
			rs[0] = unicode.ToUpper(rs[0])
			for i := 1; i < len(rs); i++ {
				rs[i] = unicode.ToLower(rs[i])
			}
			return string(rs)
		}
		return s
	}
	switch vr.Kind {
	case expand.String:
		vr.Str = fold(vr.Str)
	case expand.Indexed:
		for i, v := range vr.List {
			vr.List[i] = fold(v)
		}
	case expand.Associative:
		for k, v := range vr.Map {
			vr.Map[k] = fold(v)
		}
	}
}

// validExportedFuncName reports whether name is acceptable as the
// payload of `export -f <name>`. Bash 5.3 refuses to export
// functions whose names contain `=` or `/` (or are otherwise
// incompatible with the BASH_FUNC_<name>%%= envvar round-trip).
func validExportedFuncName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		switch name[i] {
		case '=', '/':
			return false
		}
	}
	return true
}

// printFuncDecl prints a function definition in bash 5.3's
// `declare -f` shape: `name () \n{ \n    stmt;\n    stmt2\n}` —
// 4-space indent at every nesting level, trailing `;` on every
// simple statement (and inner statements of compound commands),
// no `;` on the last top-level statement or on compound block
// closers (`fi`, `done`, `esac`, `}`).
func (r *Runner) printFuncDecl(name string, body *syntax.Stmt) {
	// bash 5.3 prefixes the `function` keyword only when the
	// `NAME ()` form would fail to reparse as a function decl —
	// i.e. when the name contains `=` (otherwise an assignment),
	// `(`, `)`, or other characters that break the standard
	// declaration syntax. Pure-digit / dash names render as plain
	// `NAME ()` without `function`.
	if funcDeclNeedsKeyword(name) {
		r.outf("function %s () \n", name)
	} else {
		r.outf("%s () \n", name)
	}
	// Body is a syntax.Stmt whose Cmd is a syntax.Block (the `{ }`)
	// in the usual case. Unwrap the block so we can render each
	// inner stmt with the bash-specific trailing-semicolon rule.
	block, ok := body.Cmd.(*syntax.Block)
	if !ok {
		// Subshell bodies (`f() ( ... )`) are wrapped in `{ }` by
		// bash 5.3 declare -f and rendered inline as one stmt.
		// Body redirections are attached to the subshell line.
		if sub, ok := body.Cmd.(*syntax.Subshell); ok {
			r.out("{ \n")
			var buf bytes.Buffer
			printer := syntax.NewPrinter(syntax.SingleLine(true), syntax.SpaceRedirects(true))
			printer.Print(&buf, &syntax.Stmt{Cmd: sub})
			inner := strings.TrimRight(buf.String(), "\n")
			inner = bashSubshellSpace(inner)
			for _, rd := range body.Redirs {
				text := formatRedirect(rd)
				if strings.HasPrefix(text, ">&") {
					text = "1" + text
				} else if strings.HasPrefix(text, "<&") {
					text = "0" + text
				}
				inner += " " + text
			}
			r.outf("    %s\n", inner)
			r.out("}\n")
			return
		}
		// Other Non-Block bodies (rare). Fall back to the printer.
		var buf bytes.Buffer
		syntax.NewPrinter(syntax.Indent(4), syntax.SpaceRedirects(true)).Print(&buf, body)
		r.out(buf.String())
		r.out("\n")
		return
	}
	r.out("{ \n")
	printer := syntax.NewPrinter(syntax.Indent(4), syntax.SpaceRedirects(true))
	// bash 5.3 declare -f groups a `cmd &` with the following
	// simple stmt onto one line. Skip ahead when we emit a
	// background stmt and merge the buffer of the next.
	skipNext := false
	for i, st := range block.Stmts {
		if skipNext {
			skipNext = false
			continue
		}
		var buf bytes.Buffer
		// Nested function declaration: render with bash 5.3's
		// `function NAME () { ... }` form rather than the
		// printer's `NAME() { ... }`. Pass redirs from BOTH the
		// surrounding stmt (Stmt.Redirs) and the body block
		// (FuncDecl.Body.Redirs) — bash attaches the body-level
		// ones to the closing brace.
		if fd, ok := st.Cmd.(*syntax.FuncDecl); ok {
			redirs := append([]*syntax.Redirect(nil), st.Redirs...)
			redirs = append(redirs, fd.Body.Redirs...)
			renderNestedFuncDecl(&buf, printer, fd, redirs)
		} else {
			printer.Print(&buf, st)
		}
		body := strings.TrimRight(buf.String(), "\n")
		if tc, ok := st.Cmd.(*syntax.TimeClause); ok && tc.Stmt == nil {
			body += " "
		}
		// bash 5.3 groups `cmd & nextStmt` onto one line when
		// the next stmt is a simple/subshell stmt (not a
		// compound). The merged line gets a trailing `;` only
		// when it's not the last stmt of the function body.
		if st.Background && i+1 < len(block.Stmts) {
			nxt := block.Stmts[i+1]
			if isSimpleForAmpJoin(nxt) {
				var nbuf bytes.Buffer
				printer.Print(&nbuf, nxt)
				nbody := strings.TrimRight(nbuf.String(), "\n")
				// Pre-pad the subshell in nbody so the merged
				// line gets `( EXPR )` instead of `(EXPR)`.
				nbody = bashSubshellSpace(nbody)
				body = body + " " + nbody
				skipNext = true
			}
		}
		isLast := (i == len(block.Stmts)-1) ||
			(skipNext && i+1 == len(block.Stmts)-1)
		if skipNext && !isLast && !strings.HasSuffix(body, ";") {
			body += ";"
		}
		rendered := bashDeclareFmt(body, isLast)
		r.out(rendered)
		r.out("\n")
		// bash 5.3 inserts a blank line between two top-level
		// stmts in a function body when the prior stmt ended
		// with a heredoc terminator. The blank also appears
		// before the closing `}` of the function when the last
		// stmt ends with a terminator.
		if endsWithHeredocTerminator(body) {
			r.out("\n")
		}
	}
	// bash 5.3 attaches function-level redirections (`f() { ... } >file`)
	// to the closing brace. Render via the printer for each Redir and
	// rewrite implicit fd1/fd0 to explicit `1>&N` / `0<&N` to match
	// bash's normalisation.
	if len(body.Redirs) > 0 {
		r.out("}")
		for _, rd := range body.Redirs {
			text := formatRedirect(rd)
			// bash 5.3 normalises only the `&N`-duplicate form
			// (file redirects `> file` / `< file` stay as-is).
			if strings.HasPrefix(text, ">&") {
				text = "1" + text
			} else if strings.HasPrefix(text, "<&") {
				text = "0" + text
			}
			r.outf(" %s", text)
		}
		r.out("\n")
	} else {
		r.out("}\n")
	}
}

// formatRedirect renders a single redirect node as text. Walks the
// node's fields directly instead of going through the printer (the
// printer needs a wrapping stmt that can panic on empty CallExpr).
func formatRedirect(r *syntax.Redirect) string {
	var b strings.Builder
	if r.N != nil {
		b.WriteString(r.N.Value)
	}
	b.WriteString(r.Op.String())
	if r.Hdoc != nil {
		// Heredoc — rare in function redir; just render the word.
	}
	if r.Word != nil {
		var wb bytes.Buffer
		syntax.NewPrinter().Print(&wb, &syntax.Stmt{Cmd: &syntax.CallExpr{Args: []*syntax.Word{r.Word}}})
		s := strings.TrimSpace(wb.String())
		b.WriteString(s)
	}
	return b.String()
}

// endsWithHeredocTerminator returns true if the printer-output body
// for a single stmt ends with a heredoc terminator line (i.e. last
// stmt was something like `cat <<EOF\\n...\\nEOF`). Used by
// printFuncDecl to insert a blank line between such a stmt and the
// next stmt, matching bash 5.3 declare -f formatting.
func endsWithHeredocTerminator(body string) bool {
	lines := strings.Split(body, "\n")
	// Walk forward, track heredoc terminator tag, and check if
	// the LAST non-empty line is a terminator.
	inH := ""
	lastIsTerm := false
	for _, raw := range lines {
		trim := strings.TrimSpace(raw)
		if trim == "" {
			continue
		}
		if inH != "" {
			if trim == inH {
				inH = ""
				lastIsTerm = true
			}
			continue
		}
		lastIsTerm = false
		if idx := findHeredocOp(trim); idx >= 0 {
			afterOp := trim[idx+2:]
			rest := strings.TrimLeft(afterOp, "-")
			rest = strings.TrimLeft(rest, " \t")
			end := strings.IndexAny(rest, " \t")
			if end < 0 {
				end = len(rest)
			}
			tag := strings.Trim(rest[:end], "'\"")
			if tag != "" {
				inH = tag
			}
		}
	}
	return lastIsTerm
}

// fieldsAllAssignments reports whether every element of fields is
// shaped like `name=value` with name being a valid identifier. Used
// by the cmd dispatch to promote a leftover expansion to plain
// assignments when the command word expanded to nothing.
func fieldsAllAssignments(fields []string) bool {
	for _, f := range fields {
		eq := strings.IndexByte(f, '=')
		if eq <= 0 {
			return false
		}
		if !syntax.ValidName(f[:eq]) {
			return false
		}
	}
	return true
}

// renderNestedFuncDecl writes a nested function decl into buf in
// bash 5.3's declare -f shape:
//
//	function NAME ()
//	{
//	    body
//	} <redirs>
// funcDeclNeedsKeyword reports whether the bash 5.3 declare -f
// renderer must prefix the `function` keyword for a function with
// this name. The plain `NAME ()` form would fail to reparse when
// the name contains characters that break the declaration syntax —
// `=` (parsed as assignment), `(`, `)`, whitespace, etc. Names that
// are pure-identifier or pure-digit (`11111`) don't need it.
func funcDeclNeedsKeyword(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch c {
		case '=', '(', ')', '<', '>', '"', '\'', '`', '$', '\\',
			' ', '\t', '\n', ';', '&', '|', '*', '?', '[', ']', '{', '}':
			return true
		}
	}
	return false
}

func renderNestedFuncDecl(buf *bytes.Buffer, printer *syntax.Printer, fd *syntax.FuncDecl, redirs []*syntax.Redirect) {
	buf.WriteString("function ")
	buf.WriteString(fd.Name.Value)
	buf.WriteString(" () \n{\n")
	// Print the function body (which is a Stmt — usually a Block).
	var bodyBuf bytes.Buffer
	if block, ok := fd.Body.Cmd.(*syntax.Block); ok {
		for _, st := range block.Stmts {
			var sb bytes.Buffer
			printer.Print(&sb, st)
			line := strings.TrimRight(sb.String(), "\n")
			// Each body stmt indented by 4 spaces in declare -f.
			bodyBuf.WriteString("    ")
			bodyBuf.WriteString(line)
			bodyBuf.WriteString("\n")
		}
	} else {
		// Non-Block body (rare) — pass through printer.
		printer.Print(&bodyBuf, fd.Body)
	}
	buf.WriteString(bodyBuf.String())
	buf.WriteString("}")
	// Function-level redirections from the surrounding Stmt.
	for _, rd := range redirs {
		text := formatRedirect(rd)
		text = strings.TrimSpace(text)
		if strings.HasPrefix(text, ">&") {
			text = "1" + text
		} else if strings.HasPrefix(text, "<&") {
			text = "0" + text
		}
		buf.WriteString(" ")
		buf.WriteString(text)
	}
}

// isSimpleForAmpJoin reports whether stmt can be joined onto the
// preceding `&` stmt's line in declare -f rendering — true for plain
// CallExpr or subshell, false for compound openers / declarations.
func isSimpleForAmpJoin(s *syntax.Stmt) bool {
	switch s.Cmd.(type) {
	case *syntax.CallExpr, *syntax.Subshell:
		return true
	}
	return false
}

// bashSplitCompound expands single-line `for/while/until/if/case`
// compound commands to multi-line layout matching bash 5.3's
// `declare -f` rendering. The mvdan/sh printer compresses them onto
// one line; bash always splits across lines, indenting the body.
//
// Pattern (single-line for/while/until):
//   while EXPR; do BODY; done    →    while EXPR; do
//                                         BODY
//                                     done
//
// Pattern (if):
//   if X; then A; elif Y; then B; else C; fi
//     → if X; then
//            A
//        elif Y; then
//            B
//        else
//            C
//        fi
//
// Pattern (case):
//   case X in PAT) BODY ;; PAT2) BODY2 ;; esac  → multi-line.
//
// We operate on already-arith-padded text. The splitting walks each
// line looking for openers (`do `, `then `, etc. as substrings) and
// closers (`done`, `fi`, `esac`); the body between them gets one
// statement per line at +4 indent relative to the opener.
func bashSplitCompound(body string) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines)*2)
	for _, line := range lines {
		out = append(out, splitCompoundLine(line)...)
	}
	return strings.Join(out, "\n")
}

// splitCompoundLine splits a single printer-output line on compound
// boundaries (`do `, `then `, `else `, `elif `, `fi`, `done`, `;;`),
// returning the new multi-line layout as a slice of lines.
func splitCompoundLine(line string) []string {
	indent := 0
	for indent < len(line) && line[indent] == ' ' {
		indent++
	}
	trim := line[indent:]
	// Detect a compound-keyword opener (for/while/until/if/case at
	// the start of the trimmed line). Only split if the line ALSO
	// contains the matching closer (otherwise it's already
	// multi-line from the printer).
	switch {
	case strings.HasPrefix(trim, "for ") ||
		strings.HasPrefix(trim, "while ") ||
		strings.HasPrefix(trim, "until "):
		// A for-in header that ends with `; do` (no body on this
		// line — body is already on subsequent lines from the
		// printer) needs `do` split to its own line. bash 5.3
		// renders for-in as `for X in Y;\n    do`. while/until
		// keep `do` glued so leave them alone.
		if strings.HasSuffix(trim, "; do") {
			ind := strings.Repeat(" ", indent)
			isFor := strings.HasPrefix(trim, "for ")
			isArithFor := strings.HasPrefix(trim, "for ((")
			if isFor {
				header := strings.TrimSuffix(trim, "; do")
				if isArithFor {
					return []string{ind + header, ind + "do"}
				}
				return []string{ind + header + ";", ind + "do"}
			}
			return []string{line}
		}
		if !strings.Contains(trim, "; do ") {
			return []string{line}
		}
		// Match `; done` either at end of line or followed by
		// trailing redirections (`done > /dev/null`); capture
		// trailing so it lands on the closing `done` line.
		doneIdx := -1
		for i := 0; i+len("; done") <= len(trim); i++ {
			if trim[i:i+len("; done")] == "; done" &&
				(i+len("; done") == len(trim) ||
					trim[i+len("; done")] == ' ' ||
					trim[i+len("; done")] == ';') {
				doneIdx = i
			}
		}
		if doneIdx < 0 {
			return []string{line}
		}
		trailing := strings.TrimSpace(trim[doneIdx+len("; done"):])
		trim = trim[:doneIdx+len("; done")]
		// Split into header + (do on its own line for `for`, same
		// line for `while`/`until` — that's bash 5.3's
		// declare -f convention) + body + done. We always emit
		// `do` on a NEW line for `for` (whether `for X in Y` or
		// arith-for `for ((;;))`); for while/until we keep `do`
		// on the same line as the condition.
		doIdx := strings.Index(trim, "; do ")
		var opener string
		isFor := strings.HasPrefix(trim, "for ")
		// bash 5.3 arith-for `for ((...))` drops the trailing `;`
		// before the `do` line; the other forms keep it.
		isArithFor := strings.HasPrefix(trim, "for ((")
		if isFor {
			opener = trim[:doIdx]
			if isArithFor {
				opener = strings.TrimSuffix(opener, ";")
				opener = strings.TrimRight(opener, " ")
			}
		} else {
			opener = trim[:doIdx] + "; do"
		}
		body := trim[doIdx+len("; do ") : len(trim)-len("; done")]
		bodyLines := splitTopLevel(body, ";")
		ind := strings.Repeat(" ", indent)
		inner := strings.Repeat(" ", indent+4)
		out := []string{ind + opener}
		if isFor {
			out = append(out, ind+"do")
		}
		for _, b := range bodyLines {
			b = strings.TrimSpace(b)
			if b == "" {
				continue
			}
			for _, sub := range splitCompoundLine(inner + b) {
				out = append(out, sub)
			}
		}
		closer := ind + "done"
		if trailing != "" {
			closer += " " + trailing
		}
		out = append(out, closer)
		return out
	case strings.HasPrefix(trim, "if "):
		if !strings.Contains(trim, "; then ") {
			return []string{line}
		}
		// Match `; fi` either at end of line or followed by a
		// trailing redirection (`fi > /dev/null`). Capture the
		// trailing portion so it can be appended to the closing
		// `fi` line.
		fiIdx := -1
		for i := 0; i+len("; fi") <= len(trim); i++ {
			if trim[i:i+len("; fi")] == "; fi" &&
				(i+len("; fi") == len(trim) ||
					trim[i+len("; fi")] == ' ' ||
					trim[i+len("; fi")] == ';') {
				fiIdx = i
			}
		}
		if fiIdx < 0 {
			return []string{line}
		}
		ifChain := trim[:fiIdx+len("; fi")]
		trailing := strings.TrimSpace(trim[fiIdx+len("; fi"):])
		out := splitIfLine(line, indent, ifChain)
		if trailing != "" && len(out) > 0 {
			// Append trailing redirection to the closing `fi`.
			out[len(out)-1] += " " + trailing
		}
		return out
	case strings.HasPrefix(trim, "case "):
		if !strings.HasSuffix(trim, " esac") {
			// `case X in` (no body on this line — printer left
			// the patterns and esac for following lines). bash
			// 5.3 emits this with a trailing space: `case X in `.
			if strings.HasSuffix(trim, " in") {
				return []string{line + " "}
			}
			return []string{line}
		}
		return splitCaseLine(line, indent, trim)
	}
	// Nested function declaration: `NAME() { BODY; } [trailing]` →
	//   function NAME ()
	//   {
	//       BODY
	//   } trailing
	// bash 5.3 declare -f renders nested function declarations
	// using the `function NAME ()` form. Only fires when the line
	// matches the pattern `<ident>() { ... }`.
	if idx := strings.Index(trim, "() { "); idx > 0 {
		name := trim[:idx]
		if syntax.ValidName(name) {
			rest := trim[idx+len("() { "):]
			// Find matching closing `}` at top level.
			closer := -1
			depthC := 1
			depthP := 0
			inSgl, inDbl := false, false
			for k := 0; k < len(rest); k++ {
				c := rest[k]
				if c == '\\' && k+1 < len(rest) {
					k++
					continue
				}
				switch {
				case inSgl:
					if c == '\'' {
						inSgl = false
					}
				case inDbl:
					if c == '"' {
						inDbl = false
					}
				case c == '\'':
					inSgl = true
				case c == '"':
					inDbl = true
				case c == '(':
					depthP++
				case c == ')':
					if depthP > 0 {
						depthP--
					}
				case c == '{':
					depthC++
				case c == '}':
					depthC--
					if depthC == 0 {
						closer = k
					}
				}
				if closer >= 0 {
					break
				}
			}
			if closer >= 0 {
				body := strings.TrimSpace(rest[:closer])
				body = strings.TrimSuffix(body, ";")
				body = strings.TrimSpace(body)
				trailing := strings.TrimSpace(rest[closer+1:])
				ind := strings.Repeat(" ", indent)
				inner := strings.Repeat(" ", indent+4)
				out := []string{
					ind + "function " + name + " () ",
					ind + "{ ",
				}
				for _, b := range splitTopLevel(body, ";") {
					b = strings.TrimSpace(b)
					if b == "" {
						continue
					}
					for _, sub := range splitCompoundLine(inner + b) {
						out = append(out, sub)
					}
				}
				closeBrace := ind + "}"
				if trailing != "" {
					closeBrace += " " + trailing
				}
				out = append(out, closeBrace)
				return out
			}
		}
	}
	// Subshell wrapping a brace group: `( { X; } ) [trailing]` →
	//   ( {
	//       X
	//   } ) trailing
	// Detect the `( { ` prefix and `} )` suffix (with possible
	// trailing ops after the `)`).
	if strings.HasPrefix(trim, "( { ") {
		// Find `} )` at top level (with whatever trailing).
		braceEnd := -1
		for k := 0; k+2 < len(trim); k++ {
			if trim[k] == '}' && trim[k+1] == ' ' && trim[k+2] == ')' {
				braceEnd = k
				break
			}
		}
		if braceEnd > 4 {
			body := strings.TrimSpace(trim[4:braceEnd])
			body = strings.TrimSuffix(body, ";")
			body = strings.TrimSpace(body)
			trailing := strings.TrimSpace(trim[braceEnd+3:])
			ind := strings.Repeat(" ", indent)
			inner := strings.Repeat(" ", indent+4)
			out := []string{ind + "( { "}
			for _, b := range splitTopLevel(body, ";") {
				b = strings.TrimSpace(b)
				if b == "" {
					continue
				}
				for _, sub := range splitCompoundLine(inner + b) {
					out = append(out, sub)
				}
			}
			closer := ind + "} )"
			if trailing != "" {
				closer += " " + trailing
			}
			out = append(out, closer)
			return out
		}
	}
	// Single-line brace-group `{ X; Y; }` → multi-line bash form:
	//
	//	{
	//	    X
	//	    Y
	//	}
	//
	// Only triggers when the line STARTS with `{ ` and contains
	// the matching `}` at the top level. The closing brace may
	// be followed by trailing ops (`}; foo` or `} > file`).
	if strings.HasPrefix(trim, "{ ") {
		// Find matching `}` at top level.
		closer := indexUnnestedBraceClose(trim)
		if closer > 0 {
			body := strings.TrimSpace(trim[2:closer])
			trailing := strings.TrimSpace(trim[closer+1:])
			body = strings.TrimSuffix(body, ";")
			body = strings.TrimSpace(body)
			ind := strings.Repeat(" ", indent)
			inner := strings.Repeat(" ", indent+4)
			out := []string{ind + "{ "}
			for _, b := range splitTopLevel(body, ";") {
				b = strings.TrimSpace(b)
				if b == "" {
					continue
				}
				for _, sub := range splitCompoundLine(inner + b) {
					out = append(out, sub)
				}
			}
			closeBrace := ind + "}"
			if trailing != "" {
				closeBrace += trailing
			}
			out = append(out, closeBrace)
			return out
		}
	}
	// Per-line case item: `PAT) BODY ;;` — the printer emits each
	// case item on its own line when the case is multi-line. Split
	// into pattern / body / ;; lines, indented one level deeper
	// than the surrounding `case ... in` (bash 5.3 convention).
	if strings.HasSuffix(trim, " ;;") || strings.HasSuffix(trim, ";;") {
		patEnd := indexUnnestedRune(trim, ')')
		if patEnd > 0 && patEnd < len(trim)-1 {
			pat := trim[:patEnd]
			body := strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(trim[patEnd+1:], ";;"), " ;"))
			// Indent patterns +4 relative to the printer's
			// position so they sit inside the surrounding
			// `case ... in` after bashDeclareFmt's outer +4
			// prepend.
			patInd := strings.Repeat(" ", indent+4)
			bodyInd := strings.Repeat(" ", indent+8)
			out := []string{patInd + pat + ")"}
			for _, b := range splitTopLevel(body, ";") {
				b = strings.TrimSpace(b)
				if b == "" {
					continue
				}
				for _, sub := range splitCompoundLine(bodyInd + b) {
					out = append(out, sub)
				}
			}
			out = append(out, patInd+";;")
			return out
		}
	}
	return []string{line}
}

// splitCaseLine handles `case X in PAT) BODY ;; PAT2) BODY2 ;; esac` →
// multi-line bash 5.3 declare -f format:
//
//	case X in
//	    a)
//	        BODY
//	    ;;
//	    b)
//	        BODY2
//	    ;;
//	esac
func splitCaseLine(orig string, indent int, trim string) []string {
	ind := strings.Repeat(" ", indent)
	inner := strings.Repeat(" ", indent+4)
	innerBody := strings.Repeat(" ", indent+8)
	// trim = `case X in PATS esac`. Strip prefix `case ` and suffix ` esac`.
	rest := strings.TrimSuffix(strings.TrimPrefix(trim, "case "), " esac")
	// rest = `X in PATS`. Find ` in ` to separate subject.
	inIdx := strings.Index(rest, " in ")
	if inIdx < 0 {
		return []string{orig}
	}
	subject := rest[:inIdx]
	pats := rest[inIdx+len(" in "):]
	// Trim leading/trailing space and a trailing `;` from the
	// patterns block (sometimes the printer leaves one).
	pats = strings.TrimSpace(pats)
	// Bash 5.3 includes a trailing space after `in `: "case x in ".
	out := []string{ind + "case " + subject + " in "}
	// Walk pats looking for each PAT) BODY ;; (or `;&`, `;;&`, etc.)
	// We rely on splitTopLevel to break at `;;` separators.
	items := splitCaseItems(pats)
	for _, it := range items {
		patEnd := indexUnnestedRune(it, ')')
		if patEnd < 0 {
			return []string{orig}
		}
		pat := strings.TrimSpace(it[:patEnd])
		body := strings.TrimSpace(it[patEnd+1:])
		// Body may end with `;;` — splitCaseItems already stripped
		// the separator, so trim defensively.
		body = strings.TrimSuffix(body, ";;")
		body = strings.TrimSpace(body)
		out = append(out, inner+pat+")")
		// Body may have multiple stmts.
		for _, b := range splitTopLevel(body, ";") {
			b = strings.TrimSpace(b)
			if b == "" {
				continue
			}
			for _, sub := range splitCompoundLine(innerBody + b) {
				out = append(out, sub)
			}
		}
		out = append(out, inner+";;")
	}
	out = append(out, ind+"esac")
	return out
}

// splitCaseItems splits a case-pattern body string at top-level `;;`
// boundaries (respecting nested parens/quotes). Returns items WITHOUT
// the trailing `;;`.
func splitCaseItems(s string) []string {
	var out []string
	depthP := 0
	inSgl, inDbl := false, false
	last := 0
	for i := 0; i+1 < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			i++
			continue
		}
		switch {
		case inSgl:
			if c == '\'' {
				inSgl = false
			}
		case inDbl:
			if c == '"' {
				inDbl = false
			}
		case c == '\'':
			inSgl = true
		case c == '"':
			inDbl = true
		case c == '(':
			depthP++
		case c == ')':
			if depthP > 0 {
				depthP--
			}
		case depthP == 0 && c == ';' && s[i+1] == ';':
			out = append(out, s[last:i])
			last = i + 2
			// Skip a possible third char for `;;&`.
			if last < len(s) && s[last] == '&' {
				last++
			}
			i = last - 1
		}
	}
	if last < len(s) {
		tail := strings.TrimSpace(s[last:])
		if tail != "" {
			out = append(out, tail)
		}
	}
	return out
}

// indexUnnestedBraceClose returns the index of the matching `}` for a
// brace group that starts at s[0] (assumed to be `{`). Tracks nested
// braces, parens, and quoted regions. -1 if not found.
func indexUnnestedBraceClose(s string) int {
	depthC := 0
	depthP := 0
	inSgl, inDbl := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			i++
			continue
		}
		switch {
		case inSgl:
			if c == '\'' {
				inSgl = false
			}
			continue
		case inDbl:
			if c == '"' {
				inDbl = false
			}
			continue
		case c == '\'':
			inSgl = true
			continue
		case c == '"':
			inDbl = true
			continue
		case c == '(':
			depthP++
			continue
		case c == ')':
			if depthP > 0 {
				depthP--
			}
			continue
		case c == '{':
			depthC++
			continue
		case c == '}':
			depthC--
			if depthC == 0 && depthP == 0 {
				return i
			}
		}
	}
	return -1
}

// indexUnnestedRune returns the index of the first occurrence of r in
// s that is not inside parens/brackets/quotes. -1 if not found.
func indexUnnestedRune(s string, r byte) int {
	depthP, depthB := 0, 0
	inSgl, inDbl := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			i++
			continue
		}
		switch {
		case inSgl:
			if c == '\'' {
				inSgl = false
			}
			continue
		case inDbl:
			if c == '"' {
				inDbl = false
			}
			continue
		case c == '\'':
			inSgl = true
			continue
		case c == '"':
			inDbl = true
			continue
		case c == '(':
			depthP++
			continue
		case c == '[':
			depthB++
			continue
		case c == ']':
			if depthB > 0 {
				depthB--
			}
			continue
		}
		if c == r && depthP == 0 && depthB == 0 {
			return i
		}
		if c == ')' && depthP > 0 {
			depthP--
		}
	}
	return -1
}

// splitIfLine handles `if X; then A; elif Y; then B; else C; fi` →
// bash 5.3's multi-line declare -f form, where elif chains are
// rendered as NESTED `else; if; fi` (each elif level adds an
// indented `if/fi` pair inside the prior `else`):
//
//	if X; then
//	    A
//	else
//	    if Y; then
//	        B
//	    else
//	        C
//	    fi
//	fi
// ifClause is one (condition, body) pair from an if/elif chain.
// Used by splitIfLine + renderNestedIf to convert flat elif chains
// to the bash 5.3 nested-if rendering.
type ifClause struct{ cond, body string }

func splitIfLine(orig string, indent int, trim string) []string {
	rest := strings.TrimSuffix(trim[len("if "):], "; fi")
	var clauses []ifClause
	var elseBody string
	for {
		thenIdx := strings.Index(rest, "; then ")
		if thenIdx < 0 {
			return []string{orig}
		}
		cond := rest[:thenIdx]
		afterThen := rest[thenIdx+len("; then "):]
		elifIdx := strings.Index(afterThen, "; elif ")
		elseIdx := strings.Index(afterThen, "; else ")
		switch {
		case elifIdx >= 0 && (elseIdx < 0 || elifIdx < elseIdx):
			clauses = append(clauses, ifClause{cond, afterThen[:elifIdx]})
			rest = afterThen[elifIdx+len("; elif "):]
		case elseIdx >= 0:
			clauses = append(clauses, ifClause{cond, afterThen[:elseIdx]})
			elseBody = afterThen[elseIdx+len("; else "):]
			goto done
		default:
			clauses = append(clauses, ifClause{cond, afterThen})
			goto done
		}
	}
done:
	return renderNestedIf(indent, clauses, elseBody)
}

// renderNestedIf emits the bash 5.3 nested-if form: each clause after
// the first becomes an `else { if ... fi }` block nested one
// indentation level deeper. lastIndent positions the surrounding
// `if`/`fi`; the recursion handles deeper levels.
func renderNestedIf(indent int, clauses []ifClause, elseBody string) []string {
	if len(clauses) == 0 {
		return nil
	}
	ind := strings.Repeat(" ", indent)
	inner := strings.Repeat(" ", indent+4)
	first := clauses[0]
	out := []string{ind + "if " + first.cond + "; then"}
	for _, b := range splitTopLevel(first.body, ";") {
		b = strings.TrimSpace(b)
		if b == "" {
			continue
		}
		for _, sub := range splitCompoundLine(inner + b) {
			out = append(out, sub)
		}
	}
	rest := clauses[1:]
	if len(rest) > 0 || elseBody != "" {
		out = append(out, ind+"else")
		if len(rest) > 0 {
			for _, sub := range renderNestedIf(indent+4, rest, elseBody) {
				out = append(out, sub)
			}
		} else {
			for _, b := range splitTopLevel(elseBody, ";") {
				b = strings.TrimSpace(b)
				if b == "" {
					continue
				}
				for _, sub := range splitCompoundLine(inner + b) {
					out = append(out, sub)
				}
			}
		}
	}
	out = append(out, ind+"fi")
	return out
}

// splitTopLevel splits s on sep that is NOT inside nested parens,
// brackets, braces, or quoted strings.
func splitTopLevel(s, sep string) []string {
	var out []string
	var b strings.Builder
	depthP, depthB, depthC := 0, 0, 0
	inSgl, inDbl := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			b.WriteByte(c)
			b.WriteByte(s[i+1])
			i++
			continue
		}
		switch {
		case inSgl:
			if c == '\'' {
				inSgl = false
			}
		case inDbl:
			if c == '"' {
				inDbl = false
			}
		case c == '\'':
			inSgl = true
		case c == '"':
			inDbl = true
		case c == '(':
			depthP++
		case c == ')':
			depthP--
		case c == '[':
			depthB++
		case c == ']':
			depthB--
		case c == '{':
			depthC++
		case c == '}':
			depthC--
		case depthP == 0 && depthB == 0 && depthC == 0 && c == sep[0]:
			out = append(out, b.String())
			b.Reset()
			continue
		}
		b.WriteByte(c)
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// bashNestElifChain converts multi-line if-elif-else-fi chains in
// declare -f output to bash 5.3's nested else/if/fi rendering. The
// printer emits the source-level form (with the `elif` keyword); bash
// rewrites it to nested ifs.
//
// Example transformation:
//
//	if X; then       →    if X; then
//	    A                     A
//	elif Y; then          else
//	    B                     if Y; then
//	else                          B
//	    C                     else
//	fi                            C
//	                          fi
//	                      fi
//
// The closing `fi` line keeps any trailing redirection.
// bashCollapseSubshellHdoc collapses the multi-line subshell-with-
// heredoc layout to bash 5.3's declare -f form. The input has
// already been through the +4 indent pass:
//
//	    (                       →    ( cat <<EOF
//	        cat <<EOF                body
//	    body                         EOF
//	    EOF                         );
//	    )
//
// Only fires when the subshell contains exactly one statement and
// that statement opens a heredoc. The closing `)` becomes ` );`
// at column 1 (one leading space), matching bash 5.3 quirks.
func bashCollapseSubshellHdoc(body string) string {
	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		trim := strings.TrimSpace(l)
		if trim != "(" || i+1 >= len(lines) {
			out = append(out, l)
			continue
		}
		// Find a heredoc opener on the next non-empty line.
		stmt := strings.TrimRight(lines[i+1], "\n")
		stmtTrim := strings.TrimSpace(stmt)
		// Strip a single trailing `;` that the +4/`;` pass may
		// have appended.
		stmtTrim = strings.TrimSuffix(stmtTrim, ";")
		hdIdx := findHeredocOp(stmtTrim)
		if hdIdx < 0 {
			out = append(out, l)
			continue
		}
		// Extract the heredoc tag.
		after := stmtTrim[hdIdx+2:]
		after = strings.TrimLeft(after, "-")
		after = strings.TrimLeft(after, " \t")
		tag := after
		tag = strings.TrimRight(tag, " \t")
		tag = strings.Trim(tag, "'\"")
		if tag == "" {
			out = append(out, l)
			continue
		}
		// Find the terminator line and the closing `)` after it.
		termIdx := -1
		for k := i + 2; k < len(lines); k++ {
			if strings.TrimSpace(lines[k]) == tag {
				termIdx = k
				break
			}
		}
		if termIdx < 0 {
			out = append(out, l)
			continue
		}
		// Find the next non-empty line after the terminator —
		// it must be a lone `)` (possibly with trailing ops).
		closeIdx := -1
		for k := termIdx + 1; k < len(lines); k++ {
			if strings.TrimSpace(lines[k]) == "" {
				continue
			}
			closeIdx = k
			break
		}
		if closeIdx < 0 {
			out = append(out, l)
			continue
		}
		closeTrim := strings.TrimSpace(lines[closeIdx])
		if !strings.HasPrefix(closeTrim, ")") {
			out = append(out, l)
			continue
		}
		trailing := strings.TrimSuffix(closeTrim[1:], ";")
		indent := l[:len(l)-len(trim)]
		// Emit `<indent>( STMT` line, the heredoc body, terminator,
		// then ` );` plus any trailing ops at column 1 (bash 5.3
		// quirk: the close lands one space in regardless of the
		// surrounding indentation).
		out = append(out, indent+"( "+stmtTrim)
		for k := i + 2; k <= termIdx; k++ {
			out = append(out, lines[k])
		}
		out = append(out, " )"+trailing+";")
		i = closeIdx
	}
	return strings.Join(out, "\n")
}

// bashSubshellSpace pads subshell `(EXPR)` openers/closers with one
// space inside the parens to match bash 5.3 declare -f rendering:
// `(exit 1)` → `( exit 1 )`. Per-line: if the trimmed line starts
// with `(` (and not `((` arith) and contains the matching `)` at
// top level, pad. Trailing ops after the closer are preserved.
func bashSubshellSpace(body string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		ind := leadingSpaces(l)
		trim := l[ind:]
		// Only fire when the line begins with `(` but NOT `((`
		// (which is arith, handled separately).
		if !strings.HasPrefix(trim, "(") || strings.HasPrefix(trim, "((") {
			continue
		}
		// Find matching `)` at top level.
		closer := -1
		depth := 0
		inSgl, inDbl := false, false
		for j := 0; j < len(trim); j++ {
			c := trim[j]
			if c == '\\' && j+1 < len(trim) {
				j++
				continue
			}
			switch {
			case inSgl:
				if c == '\'' {
					inSgl = false
				}
			case inDbl:
				if c == '"' {
					inDbl = false
				}
			case c == '\'':
				inSgl = true
			case c == '"':
				inDbl = true
			case c == '(':
				depth++
			case c == ')':
				depth--
				if depth == 0 {
					closer = j
				}
			}
			if closer >= 0 {
				break
			}
		}
		if closer < 0 {
			continue
		}
		inner := strings.TrimSpace(trim[1:closer])
		trailing := trim[closer+1:]
		if inner == "" {
			continue
		}
		lines[i] = strings.Repeat(" ", ind) + "( " + inner + " )" + trailing
	}
	return strings.Join(lines, "\n")
}

// bashCaretCtrlChars rewrites ANSI-C control-char escapes inside
// `$'...'` strings to bash 5.3's caret-notation single-quoted form:
//   $'\001'  →  '^A'
//   $'\037'  →  '^_'
//   $'\177'  →  '^?'
// Only fires when the $'...' content is purely control-char escapes
// (and no other text); mixed-content $'X\001Y' stays as-is.
func bashCaretCtrlChars(body string) string {
	// Pattern: $' [\ + 3 octal digits | \ + 1-2 octal digits] $'
	// → caret form. We do a simple linear scan.
	var b strings.Builder
	b.Grow(len(body))
	for i := 0; i < len(body); i++ {
		if i+1 < len(body) && body[i] == '$' && body[i+1] == '\'' {
			// Look for matching `'` and check if body is all
			// `\NNN` octal escapes that map to control chars.
			end := -1
			for j := i + 2; j < len(body); j++ {
				if body[j] == '\\' && j+1 < len(body) {
					j++
					continue
				}
				if body[j] == '\'' {
					end = j
					break
				}
			}
			if end < 0 {
				b.WriteByte(body[i])
				continue
			}
			content := body[i+2 : end]
			if caret, ok := allOctalControlChars(content); ok {
				b.WriteByte('\'')
				b.WriteString(caret)
				b.WriteByte('\'')
				i = end
				continue
			}
		}
		b.WriteByte(body[i])
	}
	return b.String()
}

// allOctalControlChars parses a string composed entirely of `\NNN`
// octal escapes mapping to control characters (0x01–0x1F, 0x7F).
// Returns the caret-notation string and true if so.
func allOctalControlChars(s string) (string, bool) {
	if len(s) < 2 || s[0] != '\\' {
		return "", false
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '\\' {
			return "", false
		}
		// Read 1-3 octal digits.
		end := i + 1
		for end < len(s) && end < i+4 && s[end] >= '0' && s[end] <= '7' {
			end++
		}
		if end == i+1 {
			return "", false
		}
		var v int
		for k := i + 1; k < end; k++ {
			v = v*8 + int(s[k]-'0')
		}
		switch {
		case v >= 0x01 && v <= 0x1F:
			b.WriteByte('^')
			b.WriteByte(byte('A' - 1 + v))
		case v == 0x7F:
			b.WriteString("^?")
		default:
			return "", false
		}
		i = end
	}
	return b.String(), true
}

// bashFdExplicit walks each line and rewrites implicit redirections
// (no leading fd digit) to bash 5.3's explicit form. Only fires when
// the redirection is at the boundary of words (preceded by a space or
// at line start), to avoid mangling things inside strings.
func bashFdExplicit(body string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		lines[i] = fdExplicitLine(l)
	}
	return strings.Join(lines, "\n")
}

// fdExplicitLine rewrites ` >&N` → ` 1>&N` and ` <&N` → ` 0<&N` in
// a single line. Only fires on the `&`-duplicate forms (bash 5.3
// does NOT normalise plain file `> file` / `< file` to explicit fd).
// Skips already-explicit (`1>&N`), arith-comparison contexts (`<`
// inside `(( ))`), heredocs (`<<`), `<>`, `>>`, `>|`, and strings.
func fdExplicitLine(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 4)
	inSgl, inDbl := false, false
	arithDepth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			b.WriteByte(c)
			b.WriteByte(s[i+1])
			i++
			continue
		}
		switch {
		case inSgl:
			b.WriteByte(c)
			if c == '\'' {
				inSgl = false
			}
			continue
		case inDbl:
			b.WriteByte(c)
			if c == '"' {
				inDbl = false
			}
			continue
		case c == '\'':
			inSgl = true
			b.WriteByte(c)
			continue
		case c == '"':
			inDbl = true
			b.WriteByte(c)
			continue
		}
		// Track `((` / `))` so we don't mangle arith comparison
		// operators (`i < 3`, `7 > 40`).
		if c == '(' && i+1 < len(s) && s[i+1] == '(' {
			arithDepth++
			b.WriteByte(c)
			b.WriteByte(s[i+1])
			i++
			continue
		}
		if arithDepth > 0 && c == ')' && i+1 < len(s) && s[i+1] == ')' {
			arithDepth--
			b.WriteByte(c)
			b.WriteByte(s[i+1])
			i++
			continue
		}
		if arithDepth > 0 {
			b.WriteByte(c)
			continue
		}
		// Word-boundary check: only rewrite when not part of a
		// preceding word (so `2>&3` isn't touched).
		boundary := i == 0 || s[i-1] == ' ' || s[i-1] == '\t' ||
			s[i-1] == ';' || s[i-1] == '&' || s[i-1] == '|' ||
			s[i-1] == '\n'
		if !boundary {
			b.WriteByte(c)
			continue
		}
		// `>&` (no leading fd) → `1>&`.
		if c == '>' && i+1 < len(s) && s[i+1] == '&' {
			b.WriteString("1>")
			continue
		}
		// `<&` → `0<&`.
		if c == '<' && i+1 < len(s) && s[i+1] == '&' {
			b.WriteString("0<")
			continue
		}
		b.WriteByte(c)
	}
	return normalizeCloseRedir(b.String())
}

// normalizeCloseRedir rewrites the `<&-` close-read form to `>&-`
// (close-write) to match bash 5.3's declare -f normalisation: both
// directions are equivalent for closing a file descriptor, and bash
// renders the close-write form regardless of source.
func normalizeCloseRedir(s string) string {
	if !strings.Contains(s, "<&-") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	inSgl, inDbl := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			b.WriteByte(c)
			b.WriteByte(s[i+1])
			i++
			continue
		}
		switch {
		case inSgl:
			if c == '\'' {
				inSgl = false
			}
		case inDbl:
			if c == '"' {
				inDbl = false
			}
		case c == '\'':
			inSgl = true
		case c == '"':
			inDbl = true
		case c == '<' && i+2 < len(s) && s[i+1] == '&' && s[i+2] == '-':
			b.WriteString(">&-")
			i += 2
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// bashBraceTrailSpace adds the bash 5.3 trailing space after any line
// that ends with a `{` opener in declare -f output. Covers both:
//   - bare standalone `{`
//   - prefixed forms like `coproc a {`, `function f () {`
// Idempotent — skips lines already ending in `{ ` (trailing space).
func bashBraceTrailSpace(body string) string {
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if strings.HasSuffix(l, "{") {
			lines[i] = l + " "
		}
	}
	return strings.Join(lines, "\n")
}

func bashNestElifChain(body string) string {
	lines := strings.Split(body, "\n")
	// Find if-chain bounds: groups of lines starting with `if ` and
	// ending with `fi[ <redir>]` at the same indent that contain
	// at least one `elif `. Convert each found chain.
	for i := 0; i < len(lines); i++ {
		ind := leadingSpaces(lines[i])
		trim := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(trim, "if ") || !strings.HasSuffix(trim, "; then") {
			continue
		}
		// Walk forward to find the matching `fi` at the same indent.
		fiIdx := -1
		hasElif := false
		for j := i + 1; j < len(lines); j++ {
			jInd := leadingSpaces(lines[j])
			if jInd != ind {
				continue
			}
			jTrim := strings.TrimSpace(lines[j])
			if jTrim == "elif" || strings.HasPrefix(jTrim, "elif ") {
				hasElif = true
				continue
			}
			if jTrim == "fi" || strings.HasPrefix(jTrim, "fi ") || strings.HasPrefix(jTrim, "fi;") {
				fiIdx = j
				break
			}
			if strings.HasPrefix(jTrim, "if ") {
				// nested same-indent if — give up; let recursion
				// handle inner chains separately.
				break
			}
		}
		if fiIdx < 0 || !hasElif {
			continue
		}
		// Convert lines [i..fiIdx] in place.
		converted := convertElifBlock(lines[i:fiIdx+1], ind)
		newLines := make([]string, 0, len(lines)-(fiIdx-i+1)+len(converted))
		newLines = append(newLines, lines[:i]...)
		newLines = append(newLines, converted...)
		newLines = append(newLines, lines[fiIdx+1:]...)
		lines = newLines
		// Re-process from the same i to handle remaining elifs in
		// chains converted into nested form (recursive nesting).
		// The outer i still points at the converted `if X; then`
		// line; the inner elif chain (now nested) needs another
		// pass. Decrement i so the loop re-examines this line.
		i = -1
	}
	return strings.Join(lines, "\n")
}

// convertElifBlock takes the lines of an if-elif-...-fi block (at
// indent `ind`) and returns the bash 5.3 nested form. The original
// block's first line is `if COND; then` at indent `ind`, and the
// last is `fi[ <redir>]` at indent `ind`.
func convertElifBlock(block []string, ind int) []string {
	// Locate the FIRST `elif` line at indent ind; nest it.
	for j := 1; j < len(block); j++ {
		jInd := leadingSpaces(block[j])
		if jInd != ind {
			continue
		}
		jTrim := strings.TrimSpace(block[j])
		if !strings.HasPrefix(jTrim, "elif ") {
			continue
		}
		// Replace `elif COND; then` with `else` + nested
		// `    if COND; then`. Then indent the rest of the
		// block (until the trailing `fi`) by +4, and add an
		// extra `fi` at indent+4 before the original `fi`.
		out := make([]string, 0, len(block)+3)
		out = append(out, block[:j]...) // up to (not including) the elif
		out = append(out, strings.Repeat(" ", ind)+"else")
		// Convert `elif COND; then` → `if COND; then` at ind+4.
		condThen := strings.TrimPrefix(jTrim, "elif ")
		out = append(out, strings.Repeat(" ", ind+4)+"if "+condThen)
		// Re-indent the remainder (lines j+1 .. end-1) by +4.
		for k := j + 1; k < len(block)-1; k++ {
			out = append(out, "    "+block[k])
		}
		// Add nested `fi` at indent+4, then the original `fi`
		// (with any trailing redir).
		out = append(out, strings.Repeat(" ", ind+4)+"fi")
		out = append(out, block[len(block)-1])
		return out
	}
	return block
}

// leadingSpaces counts the number of leading space chars in s.
func leadingSpaces(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}

// bashArithSpace pads the contents of `((...))` and `$((...))` runs
// with single spaces inside the parens to match bash 5.3's
// `declare -f` rendering: `((i<3))` → `(( i<3 ))`. Skips already-
// padded constructs to avoid `(( (( x )) ))` blowing up to
// `(((  ( ( x )  ) ))`. Handles nested parens by counting depth.
func bashArithSpace(body string) string {
	var b strings.Builder
	b.Grow(len(body) + 32)
	i := 0
	for i < len(body) {
		// Detect `$((` or `((` at top level. Skip `for ((...))` —
		// bash 5.3 declare -f preserves the source's arith-for
		// header spacing (sometimes spaced, sometimes not).
		start, prefix := -1, ""
		switch {
		case i+2 < len(body) && body[i] == '$' && body[i+1] == '(' && body[i+2] == '(':
			start, prefix = i, "$(("
		case i+1 < len(body) && body[i] == '(' && body[i+1] == '(':
			// Skip arith-for: look back for "for " before the `((`.
			//   - skip optional whitespace
			//   - check for trailing "for"
			j := i - 1
			for j >= 0 && (body[j] == ' ' || body[j] == '\t') {
				j--
			}
			if j >= 2 && body[j] == 'r' && body[j-1] == 'o' && body[j-2] == 'f' &&
				(j == 2 || body[j-3] == ' ' || body[j-3] == '\t' || body[j-3] == '\n') {
				// `for ((`: leave the contents alone.
				b.WriteByte(body[i])
				i++
				continue
			}
			start, prefix = i, "(("
		}
		if start < 0 {
			b.WriteByte(body[i])
			i++
			continue
		}
		// Walk forward to find the position of the FIRST `)` of
		// the closing `))`. Track depth (open `((` counts as 2).
		// Stop when depth would drop to 0 — that means the current
		// `)` is the inner of the pair and the next `)` closes the
		// outer.
		j := start + len(prefix)
		depth := 2
		found := false
		for j < len(body) {
			switch body[j] {
			case '(':
				depth++
			case ')':
				if depth == 2 && j+1 < len(body) && body[j+1] == ')' {
					// `))` closing the outer arith.
					found = true
				} else {
					depth--
				}
			}
			if found {
				break
			}
			j++
		}
		if !found {
			// Couldn't find a matching `))` — emit as-is, skip just
			// the opening to avoid infinite loop.
			b.WriteString(prefix)
			i = start + len(prefix)
			continue
		}
		// content is between start+len(prefix) and j (exclusive) and j+1
		// is the second `)` of the closing `))`.
		content := body[start+len(prefix) : j]
		trimmed := strings.TrimSpace(content)
		if trimmed == "" {
			// `(())` — leave it as-is.
			b.WriteString(body[start : j+2])
		} else {
			b.WriteString(prefix)
			b.WriteByte(' ')
			b.WriteString(trimmed)
			b.WriteString(" ))")
		}
		i = j + 2
	}
	return b.String()
}

// compactArithAssign removes single spaces around assignment-family
// operators (`=`, `+=`, `-=`, `*=`, `/=`, `%=`, `&=`, `|=`, `^=`)
// so `i = 0` becomes `i=0` to match bash 5.3's declare -f arith-for
// header. Comparison operators (`==`, `!=`, `<`, `<=`, `>`, `>=`,
// `&&`, `||`) and other non-assign ops are preserved verbatim —
// bash echoes the source spacing for those.
func compactArithAssign(s string) string {
	// Single-pass regex-like rewrite: look for spaces flanking an
	// `=` token where the left-side identifier ends in a valid
	// assignment context. We scan for occurrences of ` = ` or
	// ` += `, ` -= ` … and collapse them. Comparison `==`/`!=`/
	// `<=`/`>=` stay spaced.
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		// Look for `<space>OP<space>` where OP is assignment.
		if c == ' ' && i+2 < len(s) && s[i+2] == ' ' {
			// Single-char op: `=`
			op := s[i+1]
			if op == '=' {
				// Reject comparison `==` (would be ` == `, but
				// then s[i+2] would be `=` not ` `, so we'd
				// never reach here).
				out = trimTrailingSpace(out)
				out = append(out, '=')
				i += 2
				continue
			}
			// Two-char op: `+=`, `-=`, `*=`, `/=`, `%=`, `&=`,
			// `|=`, `^=`, `<<=`, `>>=`. We're scanning a single
			// `<space>OP<space>` pattern (OP being s[i+1] then
			// `=`). Look for that shape: s[i+1] is op char,
			// s[i+2] is `=`, s[i+3] is space.
		}
		if c == ' ' && i+3 < len(s) && s[i+3] == ' ' && s[i+2] == '=' {
			op := s[i+1]
			switch op {
			case '+', '-', '*', '/', '%', '&', '|', '^':
				// Reject `<=`, `>=`, `!=` (comparisons)
				// — handled separately, none reach here
				// because the leading char wouldn't match.
				out = trimTrailingSpace(out)
				out = append(out, op, '=')
				i += 3
				continue
			}
		}
		out = append(out, c)
	}
	return string(out)
}

// trimTrailingSpace strips a single trailing space from buf and
// returns the truncated slice. Helper for compactArithAssign so we
// drop the space we already emitted on the left side of an `=`.
func trimTrailingSpace(buf []byte) []byte {
	if n := len(buf); n > 0 && buf[n-1] == ' ' {
		return buf[:n-1]
	}
	return buf
}

// bashArithForNorm normalises the `for ((init; cond; post))` header in
// declare -f output to match bash 5.3:
//   - empty init/cond/post slots become `1` (a no-op true expression);
//   - a single space is inserted before the closing `))`;
//   - the rest of the contents is left untouched.
//
// `for ((;;))` becomes `for ((1; 1; 1))`. `for (( ; i<3; i++))` becomes
// `for ((1; i<3; i++ ))`.
func bashArithForNorm(body string) string {
	var b strings.Builder
	b.Grow(len(body) + 16)
	i := 0
	for i < len(body) {
		// Look for `for ((` at the start of the trimmed line.
		if !(i+6 <= len(body) && body[i] == 'f' && body[i+1] == 'o' && body[i+2] == 'r' &&
			(body[i+3] == ' ' || body[i+3] == '\t')) {
			b.WriteByte(body[i])
			i++
			continue
		}
		// Need previous char (if any) to be a line start or whitespace.
		if i > 0 {
			prev := body[i-1]
			if prev != '\n' && prev != ' ' && prev != '\t' && prev != ';' {
				b.WriteByte(body[i])
				i++
				continue
			}
		}
		// Find `((` after "for ".
		k := i + 4
		for k < len(body) && (body[k] == ' ' || body[k] == '\t') {
			k++
		}
		if !(k+1 < len(body) && body[k] == '(' && body[k+1] == '(') {
			b.WriteByte(body[i])
			i++
			continue
		}
		// Find matching `))`, tracking paren depth.
		start := k + 2
		j := start
		depth := 2
		found := false
		for j < len(body) {
			switch body[j] {
			case '(':
				depth++
			case ')':
				if depth == 2 && j+1 < len(body) && body[j+1] == ')' {
					found = true
				} else {
					depth--
				}
			}
			if found {
				break
			}
			j++
		}
		if !found {
			b.WriteByte(body[i])
			i++
			continue
		}
		header := body[start:j]
		// Split on `;` at top-level paren depth.
		parts := []string{}
		level := 0
		last := 0
		for x := 0; x < len(header); x++ {
			switch header[x] {
			case '(':
				level++
			case ')':
				level--
			case ';':
				if level == 0 {
					parts = append(parts, header[last:x])
					last = x + 1
				}
			}
		}
		parts = append(parts, header[last:])
		if len(parts) != 3 {
			b.WriteString(body[i : j+2])
			i = j + 2
			continue
		}
		postEmpty := false
		for idx, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				if idx == 2 {
					postEmpty = true
				}
				p = "1"
			} else {
				// bash 5.3 declare -f keeps assignment-family
				// operators (`=`, `+=`, …) compact inside arith-
				// for headers while preserving spaces around
				// comparison ops (`<`, `>`, `&&`, `||`). Source
				// `i=0` stays `i=0` after a round trip.
				p = compactArithAssign(p)
			}
			parts[idx] = p
		}
		b.WriteString(body[i:k])
		b.WriteString("((")
		b.WriteString(parts[0])
		b.WriteString("; ")
		b.WriteString(parts[1])
		b.WriteString("; ")
		b.WriteString(parts[2])
		if postEmpty {
			b.WriteString("))")
		} else {
			b.WriteString(" ))")
		}
		i = j + 2
	}
	return b.String()
}

// findHeredocOp returns the index of the first `<<` in line that is
// NOT part of `<<<` (here-string), or -1 if none. Used by
// bashDeclareFmt to distinguish heredoc openers from here-string ops.
func findHeredocOp(line string) int {
	for i := 0; i+1 < len(line); i++ {
		if line[i] != '<' || line[i+1] != '<' {
			continue
		}
		// `<<<` (here-string) — skip past all the `<`s.
		if i+2 < len(line) && line[i+2] == '<' {
			for i+1 < len(line) && line[i+1] == '<' {
				i++
			}
			continue
		}
		return i
	}
	return -1
}

// bashDeclareFmt reformats a printer-produced statement body to match
// bash 5.3's `declare -f` rendering. Per-stmt the printer emits the
// opener line at column 0, nested-block bodies at column 4 (via
// Indent(4)), nested-block closers (fi/done/esac/}) at column 0, and
// any heredoc body+terminator at column 0 (heredocs always anchor
// left).
//
// We need to:
//   - prepend 4 spaces to every printer line *except* heredoc body
//     and heredoc terminator lines (those stay at column 0 to match
//     bash);
//   - append `;` to each simple statement except heredoc openers
//     (`cat <<TAG`), heredoc body / terminator, block openers
//     (ending in then/do/in/{/else/operators), block closers
//     (fi/done/esac/}), and the last top-level statement of the
//     function body.
//
// Heredoc context is tracked explicitly via inHdoc: when we see a
// line ending with `<<TAG` we capture TAG; subsequent lines up to and
// including the TAG-only line are heredoc content.
func bashDeclareFmt(body string, lastTop bool) string {
	// Bash 5.3 renders `((expr))` as `(( expr ))` (space-padded
	// inside the double-parens) in `declare -f` output. Same for
	// arith expansion `$((expr))` → `$(( expr ))`. The printer
	// emits the compact form; pad with regex.
	body = bashArithSpace(body)
	// bash 5.3 pads subshell parens: `(expr)` → `( expr )`. Run
	// BEFORE the compound splitter so subshell-of-block patterns
	// like `( { X; } )` are detectable.
	body = bashSubshellSpace(body)
	// bash 5.3 normalises `for ((;;))` headers: empty parts become
	// `1` and a space is added before `))`.
	body = bashArithForNorm(body)
	// Bash 5.3 always renders for/while/until/if as multi-line in
	// `declare -f` even when the source was single-line; the
	// printer collapses to single-line. Split here.
	body = bashSplitCompound(body)
	// bash 5.3 converts multi-line elif chains to nested else/if/fi.
	body = bashNestElifChain(body)
	// bash 5.3 emits standalone `{` openers with a trailing space.
	body = bashBraceTrailSpace(body)
	// bash 5.3 normalises implicit fd1/fd0 to explicit form in
	// declare -f output: `>&2` → `1>&2`, `<&3` → `0<&3`,
	// `> file` → `1> file`, `< file` → `0< file`.
	body = bashFdExplicit(body)
	// bash 5.3 declare -f renders control chars (`$'\001'`) using
	// caret notation (`'^A'`) instead of ANSI-C.
	body = bashCaretCtrlChars(body)
	// Pre-pass: track heredoc terminators so we can insert a blank
	// line after each one (bash 5.3 declare -f convention) when we
	// emit the final joined string.
	preLines := strings.Split(body, "\n")
	hdocTermAt := make(map[int]bool)
	{
		inH := ""
		for i, raw := range preLines {
			trim := strings.TrimSpace(raw)
			if inH != "" {
				if trim == inH {
					inH = ""
					hdocTermAt[i] = true
				}
				continue
			}
			if idx := findHeredocOp(trim); idx >= 0 {
				afterOp := trim[idx+2:]
				rest := strings.TrimLeft(afterOp, "-")
				rest = strings.TrimLeft(rest, " \t")
				end := strings.IndexAny(rest, " \t")
				if end < 0 {
					end = len(rest)
				}
				tag := strings.Trim(rest[:end], "'\"")
				if tag != "" {
					inH = tag
				}
			}
		}
	}

	lines := strings.Split(body, "\n")
	inHdoc := ""
	hdocStripTabs := false
	for i, raw := range lines {
		trim := strings.TrimSpace(raw)
		// Inside a heredoc: leave body / terminator untouched
		// (no re-indent, no `;`). If the opener was `<<-`, strip
		// leading tabs from body and terminator (bash 5.3
		// declare -f matches the runtime tab-stripping).
		if inHdoc != "" {
			if hdocStripTabs {
				lines[i] = strings.TrimLeft(raw, "\t")
			}
			if trim == inHdoc {
				inHdoc = ""
				hdocStripTabs = false
			}
			continue
		}
		// Outside a heredoc: every line gets the 4-space lift.
		lines[i] = "    " + raw
		if trim == "" {
			continue
		}
		// Heredoc opener: line contains `<<TAG`, `<<-TAG`,
		// `<<'TAG'`, or `<<"TAG"`. NOT to be confused with `<<<`
		// (here-strings) which are simple-statement-like and get
		// a trailing `;`. Capture the terminator and skip the `;`
		// so the next line parses as the heredoc body. We scan for
		// the FIRST `<<` that isn't part of `<<<`.
		if idx := findHeredocOp(trim); idx >= 0 {
			// After `<<` (optionally `-` for `<<-`), the next word
			// is the tag. The line may continue with redirections
			// (`<<TAG > file`) or a following compound — strip
			// quotes around the tag and stop at first whitespace.
			afterOp := trim[idx+2:]
			stripTabs := strings.HasPrefix(afterOp, "-")
			rest := strings.TrimLeft(afterOp, "-")
			rest = strings.TrimLeft(rest, " \t")
			end := strings.IndexAny(rest, " \t")
			if end < 0 {
				end = len(rest)
			}
			tag := strings.Trim(rest[:end], "'\"")
			if tag != "" {
				inHdoc = tag
				hdocStripTabs = stripTabs
				continue
			}
		}
		// Block openers / mid-clauses: never add `;`.
		switch trim {
		case "{", "do", "then", "else":
			continue
		}
		// Block closers (fi / done / esac / `}`) DO get `;` when
		// followed by more statements in the body — only the last
		// line of the function (lastTop && last i) is bare. Also
		// bare for `done` / `fi` when the PRECEDING non-empty
		// line was a heredoc terminator (bash 5.3 omits `;` on
		// for/while/until/if closers in that case but KEEPS it
		// on `}` and `esac`).
		switch trim {
		case "fi", "done", "esac", "}":
			lastSkip := false
			if trim == "done" || trim == "fi" {
				for k := i - 1; k >= 0; k-- {
					ptrim := strings.TrimSpace(lines[k])
					if ptrim == "" {
						continue
					}
					if hdocTermAt[k] {
						lastSkip = true
					}
					break
				}
			}
			if !(lastTop && i == len(lines)-1) && !lastSkip {
				lines[i] += ";"
			}
			continue
		}
		// Bare-paren subshell lines (`(` opener, `)` closer) get
		// no `;`; check those before the suffix loop since `)`
		// would otherwise match `))` (arith-exp closing) which
		// IS a simple statement and DOES need `;`.
		if trim == "(" || trim == ")" {
			continue
		}
		skip := false
		for _, suffix := range []string{
			" then", " do", " in", " {", " else",
			";", "&", "|", ";;", ";&", "&&", "||",
		} {
			if strings.HasSuffix(trim, suffix) {
				skip = true
				break
			}
		}
		// Case-pattern line: trimmed text ends with bare `)`
		// (single right paren NOT being `))` from arith). Skip
		// when this looks like a case pattern: doesn't begin with
		// `(` (a subshell stmt like `( foo )` should still get a
		// trailing `;` inside a function body).
		if !skip && strings.HasSuffix(trim, ")") && !strings.HasSuffix(trim, "))") && !strings.HasPrefix(trim, "(") {
			skip = true
		}
		// Skip the trailing `;` when the next non-empty line is
		// `;;` (case-pattern separator) — bash 5.3 emits bare
		// body lines before `;;` in declare -f output.
		//
		// Also skip when next line is `do` AND the current line
		// ends with `))` — that's the bash 5.3 arith-for header
		// (`for ((init; cond; post))` with no trailing `;`).
		// `for X in Y; do` keeps the `;` after `Y` (matches bash).
		//
		// Also skip when next line is `}` (or `};` etc.) — body
		// lines inside `{ ... }` blocks omit `;` on the last
		// stmt, matching the function-body rule one level deeper.
		if !skip {
			for k := i + 1; k < len(lines); k++ {
				nxt := strings.TrimSpace(lines[k])
				if nxt == "" {
					continue
				}
				switch {
				case nxt == ";;" || strings.HasPrefix(nxt, ";;"):
					skip = true
				case nxt == "do" && strings.HasSuffix(trim, "))"):
					skip = true
				case nxt == "}" || strings.HasPrefix(nxt, "} ") ||
					strings.HasPrefix(nxt, "};"):
					skip = true
				}
				break
			}
		}
		if skip {
			continue
		}
		if !(lastTop && i == len(lines)-1) {
			lines[i] += ";"
		}
	}
	// Insert a blank line after each heredoc terminator (bash 5.3
	// declare -f puts a blank line between the EOF closer and the
	// next stmt or the function close). Skip when the next line
	// is the collapsed ` );` subshell-close (bash 5.3 keeps the
	// terminator and the close adjacent in that case).
	if len(hdocTermAt) > 0 {
		out := make([]string, 0, len(lines)+len(hdocTermAt))
		for i, l := range lines {
			out = append(out, l)
			if hdocTermAt[i] && i+1 < len(lines) {
				nxtTrim := strings.TrimSpace(lines[i+1])
				if strings.HasPrefix(nxtTrim, ")") {
					continue
				}
				out = append(out, "")
			}
		}
		lines = out
	}
	result := strings.Join(lines, "\n")
	// bash 5.3 collapses `(\n    CMD <<TAG\n...\nTAG\n    )` to
	// `( CMD <<TAG\n...\nTAG\n );` when the subshell body is a
	// single heredoc-bearing statement.
	return bashCollapseSubshellHdoc(result)
}

// isPosixSpecialBuiltin reports whether name is a POSIX "special
// builtin" (POSIX 1003.1 § 2.14). In bash's POSIX mode, an assignment
// preceding a special-builtin invocation persists after the command
// returns rather than being reverted.
func isPosixSpecialBuiltin(name string) bool {
	switch name {
	case "break", ":", "continue", ".", "eval", "exec", "exit",
		"export", "readonly", "return", "set", "shift",
		"source", "times", "trap", "unset":
		return true
	}
	return false
}

// bashErrPrefix returns the bash-style `<filename>: line <N>: ` prefix
// when [WithBashCompatErrors] is on; the empty string otherwise. The
// filename comes from the parsed script (set when running a File) or
// falls back to "bashy" for `-c` / stdin / interactive invocations.
func (r *Runner) bashErrPrefix(pos syntax.Pos) string {
	if !r.bashCompatErrors {
		return ""
	}
	name := r.filename
	if name == "" {
		name = "bashy"
	}
	line := int(pos.Line())
	// When executing a multi-stmt alias body the AST positions are
	// from the alias-body parse (line N within the body), not from
	// the call site in the script. r.aliasLineOverride is set by the
	// alias-expansion code in cmd() to make runtime diagnostics
	// (`command not found`, etc.) report the invocation line.
	if r.aliasLineOverride > 0 {
		line = r.aliasLineOverride
	}
	return fmt.Sprintf("%s: line %d: ", name, line)
}

// bashOSError formats an os.PathError (or any error) the way bash 5.3
// formats file-open failures: the syscall reason with its first letter
// capitalised (`No such file or directory`, `Permission denied`, …),
// stripped of Go's `open <path>: ` prefix.
func bashOSError(err error) string {
	var pe *os.PathError
	msg := err.Error()
	if errors.As(err, &pe) {
		msg = pe.Err.Error()
	}
	if msg == "" {
		return msg
	}
	return strings.ToUpper(msg[:1]) + msg[1:]
}

func (r *Runner) stop(ctx context.Context) bool {
	// `returning` is a function-scoped flag (set by the `return`
	// builtin); honour it even inside a trap so a function called
	// from a DEBUG/ERR trap can exit early. `exiting` is the
	// script-level exit flag — some traps trigger on exit so we
	// only honour that one outside trap handlers.
	if r.exit.returning {
		return true
	}
	if !r.handlingTrap && r.exit.exiting {
		return true
	}
	if err := ctx.Err(); err != nil {
		r.exit.fatal(err)
		return true
	}
	if r.opts[optNoExec] {
		return true
	}
	return false
}

func (r *Runner) stmt(ctx context.Context, st *syntax.Stmt) {
	if r.stop(ctx) {
		return
	}
	r.exit = exitStatus{}
	if st.Background || st.Disown {
		r2 := r.subshell(true)
		st2 := *st
		st2.Background = false
		st2.Disown = false
		bg := &bgProc{
			done:        make(chan struct{}),
			exit:        new(exitStatus),
			pidReady:    make(chan struct{}),
			pidCallback: r.bgPidCallback, // see WithBgPidCallback
		}
		r.bgProcs = append(r.bgProcs, bg)
		// Stash a pointer to the freshly-appended bgProc on the
		// goroutine's ctx so the exec handlers (DefaultExecHandler,
		// runDetachedExec) can publish the real OS PID into it via
		// publishBgPid. `$!` reads that PID back via bgProc.pidReady.
		bgCtx := context.WithValue(ctx, bgProcCtxKey{}, bg)
		go func() {
			defer func() {
				// Ensure pidReady is closed even if no real exec ever
				// happened (e.g. `(true) &`). The reader of `$!` waits
				// on this channel — leaving it open would hang forever.
				select {
				case <-bg.pidReady:
				default:
					close(bg.pidReady)
				}
			}()
			r2.Run(bgCtx, &st2)
			r2.exit.exiting = false // subshells don't exit the parent shell
			*bg.exit = r2.exit
			close(bg.done)
		}()
	} else {
		r.stmtSync(ctx, st)
	}
	r.lastExit = r.exit
}

func (r *Runner) stmtSync(ctx context.Context, st *syntax.Stmt) {
	// keepRedirs is a per-stmt flag: only exec inside *this* stmt may
	// set it (to opt out of restoring this stmt's redirects). Reset it
	// at return so the next stmt starts with proper scoping. Registered
	// first so it fires LAST (LIFO) — the file-close defers below still
	// see the in-stmt value and skip closing for exec's persistent fds.
	defer func() { r.keepRedirs = false }()

	r.curStmtPos = st.Pos()

	oldIn, oldOut, oldErr := r.stdin, r.stdout, r.stderr
	// Snapshot fdTable only when this statement has redirects that
	// might mutate it. A coproc statement registers fds in fdTable from
	// inside cmd() itself, not via redir(), and those changes must
	// persist past stmtSync; restoring unconditionally would wipe them.
	var oldFdTable map[int]*os.File
	if len(st.Redirs) > 0 {
		oldFdTable = maps.Clone(r.fdTable)
	}
	// bash 5.3 caps the number of here-documents per simple command
	// (the historical compile-time limit, 16). Beyond that bash
	// rejects the entire command with "maximum here-document count
	// exceeded" before executing it.
	const maxHeredocs = 16
	hdocCount := 0
	for _, rd := range st.Redirs {
		if rd.Op == syntax.Hdoc || rd.Op == syntax.DashHdoc {
			hdocCount++
		}
	}
	if hdocCount > maxHeredocs {
		r.errf("%smaximum here-document count exceeded\n", r.bashErrPrefix(st.Pos()))
		r.exit.code = 1
		// bash treats the limit as a fatal parser error and
		// stops executing the rest of the script.
		r.exit.exiting = true
		return
	}
	for _, rd := range st.Redirs {
		cls, err := r.redir(ctx, rd)
		if err != nil {
			r.exit.code = 1
			break
		}
		if cls != nil {
			// Skip the close when keepRedirs is set (exec). The opened
			// file is now owned by fdTable / stdio and must outlive
			// this stmtSync call. Read keepRedirs at defer time, not
			// here, because exec sets it during cmd execution.
			defer func(c io.Closer) {
				if !r.keepRedirs {
					c.Close()
				}
			}(cls)
		}
	}
	if r.exit.ok() && st.Cmd != nil {
		// A negated stmt suppresses `set -e`-driven exit for the
		// command it wraps — bash treats `! cmd` like `cmd || true`
		// for errexit purposes.
		if st.Negated {
			oldNoErrExit := r.noErrExit
			r.noErrExit = true
			r.cmd(ctx, st.Cmd)
			r.noErrExit = oldNoErrExit
			// Clear any pending exit propagated by inner stmts
			// under errexit; the outer `!` will set the final
			// success/failure below.
			r.exit.exiting = false
		} else {
			r.cmd(ctx, st.Cmd)
		}
	}
	if st.Negated {
		if r.exit.ok() {
			r.exit.code = 1
		} else {
			r.exit.clear()
		}
	} else if b, ok := st.Cmd.(*syntax.BinaryCmd); ok && (b.Op == syntax.AndStmt || b.Op == syntax.OrStmt) {
	} else if !r.exit.ok() && !r.noErrExit {
		r.trapCallback(ctx, r.trapCallbacks["ERR"], "error")
		// If the "errexit" option is set and a command failed, exit the shell. Exceptions:
		//
		//   conditions (if <cond>, while <cond>, etc)
		//   part of && or || lists; excluded via "else" above
		//   preceded by !; excluded via "else" above
		if r.opts[optErrExit] {
			r.exit.exiting = true
		}
	}
	if !r.keepRedirs {
		r.stdin, r.stdout, r.stderr = oldIn, oldOut, oldErr
		if len(st.Redirs) > 0 {
			r.fdTable = oldFdTable
		}
	}
}

func (r *Runner) cmd(ctx context.Context, cm syntax.Command) {
	if r.stop(ctx) {
		return
	}

	tracingEnabled := r.opts[optXTrace]
	trace := r.tracer()

	switch cm := cm.(type) {
	case *syntax.Block:
		r.stmts(ctx, cm.Stmts)
	case *syntax.Subshell:
		r2 := r.subshell(false)
		r2.stmts(ctx, cm.Stmts)
		// Subshells don't exit or return from the surrounding
		// function: `(return 5)` makes the subshell exit with
		// status 5, but the outer function/script keeps running.
		r2.exit.exiting = false
		r2.exit.returning = false
		r.exit = r2.exit
	case *syntax.CallExpr:
		// Bash sets $BASH_COMMAND to the command's source text BEFORE
		// expansion, so a command can reference its own line via
		// $BASH_COMMAND. Capture it now via the printer; the later
		// setVarString in r.call() will overwrite with the post-
		// expansion form for the benefit of DEBUG traps.
		{
			var cmdBuf strings.Builder
			syntax.NewPrinter().Print(&cmdBuf, cm)
			r.setVarString("BASH_COMMAND", strings.TrimRight(cmdBuf.String(), "\n"))
		}
		// Bash fires DEBUG before each simple command, including
		// assignment-only forms (`x=2`). With `shopt -s extdebug`,
		// a trap that returns 2 skips the next command. Fire here
		// so the assignment-only branch below honors the skip.
		if len(cm.Args) == 0 && len(cm.Assigns) > 0 && r.trapCallbacks["DEBUG"] != "" {
			prevLineno := r.ecfg.OverrideLineno
			r.ecfg.OverrideLineno = int(cm.Assigns[0].Pos().Line())
			debugCode := r.trapCallback(ctx, r.trapCallbacks["DEBUG"], "debug")
			r.ecfg.OverrideLineno = prevLineno
			if opt, _ := r.bashOptByName("extdebug"); opt != nil && *opt && debugCode == 2 {
				return
			}
		}
		// Use a new slice, to not modify the slice in the alias map.
		args := cm.Args
		for i := 0; i < len(args); {
			if !r.opts[optExpandAliases] {
				break
			}
			als, ok := r.alias[args[i].Lit()]
			if !ok {
				break
			}
			// Multi-stmt alias (`alias foo=$'echo a\necho b'`):
			// execute the parsed file in place of the surrounding
			// call. Only kicks in when the alias word is at i==0
			// (alias position); otherwise treat it as plain text.
			// Override the runtime line so diagnostics
			// (`command not found`, etc.) report bash's invocation
			// line rather than the line within the alias body.
			if als.file != nil && i == 0 {
				prevOverride := r.aliasLineOverride
				r.aliasLineOverride = int(cm.Pos().Line())
				r.stmts(ctx, als.file.Stmts)
				r.aliasLineOverride = prevOverride
				return
			}
			args = slices.Replace(args, i, i+1, als.args...)
			if !als.blank {
				break
			}
			i += len(als.args)
		}
		r.lastExpandExit = exitStatus{}
		fields := r.fields(args...)
		// bash 5.3: when a CallExpr already has assignment prefixes
		// (`a=5 b=6 $CMD ...`) AND the remaining words after
		// expansion are ALL assignment-shaped (typically because
		// the command word expanded to nothing), promote them to
		// additional plain assignments. e.g.
		//   a=5 b=6 $UNSET c=7 d=8
		// becomes assignments a=5, b=6, c=7, d=8.
		// Also promote when `set -k` (keyword mode) is on: bash
		// treats any `name=value` word anywhere on the line as
		// part of the env, and when the command word disappears
		// the assignments become plain global assignments.
		// Without -k, only fires when cm.Assigns is non-empty —
		// keeps the brace-expanded `{a,b}=value` case (no leading
		// assigns) on the cmd path so it errors as bash 3.x did.
		setK := r.opts[optKeyword]
		if (len(cm.Assigns) > 0 || setK) && len(fields) > 0 && fieldsAllAssignments(fields) {
			for _, f := range fields {
				eq := strings.IndexByte(f, '=')
				name := f[:eq]
				val := f[eq+1:]
				vr := expand.Variable{Set: true, Kind: expand.String, Str: val}
				r.setVar(name, vr)
			}
			fields = nil
		}
		if len(fields) == 0 {
			for _, as := range cm.Assigns {
				name := as.Name.Value

				prev := r.lookupVar(name)
				// Here we have a naked "foo=bar", so if we inherited a local var from a parent
				// function we want to signal that we are modifying the parent var rather than
				// creating a new local var via "local foo=bar".
				// TODO: there is likely a better way to do this.
				prev.Local = false

				name, vr := r.assignVal(name, prev, as, "")
				r.setVarWithIndex(prev, name, as.Index, vr)

				if !tracingEnabled {
					continue
				}

				// Strangely enough, it seems like Bash prints original
				// source for arrays, but the expanded value otherwise.
				// TODO: add test cases for x[i]=y.
				op := "="
				if as.Append {
					op = "+="
				}
				if as.Array != nil {
					// bash xtrace re-quotes each array element
					// rather than rendering the original source
					// literal — `$' '` becomes `' '`, `\|` stays
					// as `\|` (since the printer keeps backslash-
					// escapes), and tab/newline use single-quote
					// literals.
					traceArrayLiteral(trace, name, op, as.Array.Elems, r)
				} else if as.Value != nil {
					// Bash 5.3 traces the *raw* RHS for `+=` (so
					// the trace shows the appended chunk, not the
					// pre-append concatenated value). For `=` we
					// keep the expanded form.
					val := vr.String()
					if as.Append {
						val, _ = expand.Literal(r.ecfg, as.Value)
					}
					quoted, err := syntax.Quote(val, syntax.LangBash)
					if err != nil { // should never happen
						panic(err)
					}
					trace.stringf("%s%s%s", name, op, quoted)
				}
				trace.newLineFlush()
			}
			// If interpreting the last expansion like $(foo) failed,
			// and the expansion and assignments otherwise succeeded,
			// we need to surface that last exit code.
			if r.exit.ok() {
				r.exit = r.lastExpandExit
			}
			break
		}

		type restoreVar struct {
			name string
			vr   expand.Variable
		}
		var restores []restoreVar

		for _, as := range cm.Assigns {
			name := as.Name.Value
			prev := r.lookupVar(name)
			// Resolve any nameref so we can restore the original final value later on.
			if n, v := prev.Resolve(r.writeEnv); n != "" {
				name, prev = n, v
			}

			name, vr := r.assignVal(name, prev, as, "")
			// Inline command vars are always exported.
			vr.Exported = true

			restores = append(restores, restoreVar{name, prev})

			r.setVar(name, vr)
			if tracingEnabled && as.Value != nil {
				op := "="
				if as.Append {
					op = "+="
				}
				val := vr.String()
				if as.Append {
					val, _ = expand.Literal(r.ecfg, as.Value)
				}
				quoted, err := syntax.Quote(val, syntax.LangBash)
				if err != nil {
					panic(err)
				}
				trace.stringf("%s%s%s", name, op, quoted)
				trace.newLineFlush()
			}
		}

		trace.call(fields[0], fields[1:]...)
		trace.newLineFlush()

		r.call(ctx, cm.Args[0].Pos(), fields)
		// Bash POSIX mode (or inside a function): assignments
		// preceding a special builtin (`return`, `export`, `eval`,
		// `readonly`, `set`, …) persist after the command returns.
		// Outside POSIX mode at top level, the temporary is
		// restored so `v=inline source ./f` reverts v afterwards.
		special := isPosixSpecialBuiltin(fields[0])
		persistInline := special && (r.opts[optPosix] || r.inFunc)
		if !persistInline {
			for _, restore := range restores {
				r.setVar(restore.name, restore.vr)
			}
		}
	case *syntax.BinaryCmd:
		switch cm.Op {
		case syntax.AndStmt, syntax.OrStmt:
			oldNoErrExit := r.noErrExit
			r.noErrExit = true
			r.stmt(ctx, cm.X)
			r.noErrExit = oldNoErrExit
			if r.exit.ok() == (cm.Op == syntax.AndStmt) {
				r.stmt(ctx, cm.Y)
			}
		case syntax.Pipe, syntax.PipeAll:
			pr, pw, err := os.Pipe()
			if err != nil {
				r.exit.fatal(err) // not being able to create a pipe is rare but critical
				return
			}
			// Duplicate pipe fds for use by the goroutines, then close
			// the originals. This ensures the parent process does not
			// hold extra references to the pipe, so that:
			//   - EOF propagates when the writer closes its end
			//   - SIGPIPE is delivered when the reader closes its end
			// Builtins and external commands in the goroutines use the
			// duplicated fds, which remain valid until explicitly closed.
			// See https://github.com/mvdan/sh/issues/1142
			pwDup, err := dupPipeFd(pw)
			if err != nil {
				pw.Close()
				pr.Close()
				r.exit.fatal(err)
				return
			}
			prDup, err := dupPipeFd(pr)
			if err != nil {
				pw.Close()
				pr.Close()
				pwDup.Close()
				r.exit.fatal(err)
				return
			}
			pw.Close()
			pr.Close()

			r2 := r.subshell(true)
			r2.stdout = pwDup
			if cm.Op == syntax.PipeAll {
				r2.stderr = pwDup
			} else {
				r2.stderr = r.stderr
			}
			// bash 5.3: the last command in a pipeline runs in a
			// subshell unless `shopt -s lastpipe` is enabled (and
			// job control is off). Without lastpipe, assignments
			// in `... | { IFS=:; read line; }` MUST NOT leak to
			// the parent shell. Decide subshell-vs-current here.
			lastpipe := false
			if opt, _ := r.bashOptByName("lastpipe"); opt != nil && *opt {
				lastpipe = true
			}
			oldStdin := r.stdin
			r.stdin = prDup
			var wg sync.WaitGroup
			wg.Go(func() {
				r2.stmt(ctx, cm.X)
				r2.exit.exiting = false // subshells don't exit the parent shell
				pwDup.Close()
			})
			var r3 *Runner
			if lastpipe {
				r.stmt(ctx, cm.Y)
			} else {
				// background=false: r3 lazily walks up to r.writeEnv,
				// matching `(...)` subshell semantics so callers that
				// supply a sparse parent env (e.g. expand.FuncEnviron
				// with no Each enumeration) still resolve variables.
				r3 = r.subshell(false)
				r3.stdin = prDup
				r3.stdout = r.stdout
				r3.stderr = r.stderr
				r3.stmt(ctx, cm.Y)
				r3.exit.exiting = false
				r.exit = r3.exit
			}
			prDup.Close()
			wg.Wait()
			r.stdin = oldStdin
			_ = r3 // suppress unused when lastpipe is on
			// Track PIPESTATUS. mvdan/sh parses pipes left-associative,
			// so `a | b | c` is (a | b) | c — X is the nested pipeline
			// and runs in r2. If r2 itself ran a pipeline, its segment
			// statuses are in r2.pipeStatus; we extend that with Y's
			// status to form the full chain. Otherwise it's a simple
			// X | Y pair.
			yCode := strconv.Itoa(int(r.exit.code))
			if len(r2.pipeStatus) > 0 {
				r.pipeStatus = append(append([]string(nil), r2.pipeStatus...), yCode)
			} else {
				r.pipeStatus = []string{strconv.Itoa(int(r2.exit.code)), yCode}
			}
			if r.opts[optPipeFail] && !r2.exit.ok() && r.exit.ok() {
				r.exit = r2.exit
			}
			if r2.exit.fatalExit {
				r.exit.fatal(r2.exit.err) // surface fatal errors immediately
			}
		}
	case *syntax.IfClause:
		oldNoErrExit := r.noErrExit
		r.noErrExit = true
		r.stmts(ctx, cm.Cond)
		r.noErrExit = oldNoErrExit

		if r.exit.ok() {
			r.stmts(ctx, cm.Then)
			break
		}
		r.exit.clear()
		if cm.Else != nil {
			r.cmd(ctx, cm.Else)
		}
	case *syntax.WhileClause:
		for !r.stop(ctx) {
			oldNoErrExit := r.noErrExit
			r.noErrExit = true
			r.stmts(ctx, cm.Cond)
			r.noErrExit = oldNoErrExit

			stop := r.exit.ok() == cm.Until
			r.exit.clear()
			if stop || r.loopStmtsBroken(ctx, cm.Do) {
				break
			}
		}
	case *syntax.ForClause:
		switch y := cm.Loop.(type) {
		case *syntax.WordIter:
			name := y.Name.Value
			// Bash 5.3 rejects invalid identifier names at the
			// `for`/`select` loop step before any iteration runs.
			// `for invalid-name in a b c; do …` emits a
			// "not a valid identifier" diagnostic and aborts the
			// loop.
			if !syntax.ValidName(name) {
				r.errf("%s`%s': not a valid identifier\n",
					r.bashErrPrefix(y.Pos()), name)
				r.exit.code = 1
				return
			}
			items := r.Params // for i; do ...

			inToken := y.InPos.IsValid()
			if inToken {
				items = r.fields(y.Items...) // for i in ...; do ...
			}

			if cm.Select {
				r.selectLoop(ctx, name, items, cm.Do)
				break
			}

			for _, field := range items {
				r.setVarString(name, field)
				trace.stringf("for %s in", y.Name.Value)
				if inToken {
					for _, item := range y.Items {
						trace.string(" ")
						trace.expr(item)
					}
				} else {
					trace.string(` "$@"`)
				}
				trace.newLineFlush()
				if r.loopStmtsBroken(ctx, cm.Do) {
					break
				}
			}
		case *syntax.CStyleLoop:
			// bash `set -x` traces each of the C-style for-loop
			// expressions as a separate `+ (( ... ))` line.
			traceArith := func(expr syntax.ArithmExpr) {
				if !tracingEnabled || expr == nil {
					return
				}
				var inner bytes.Buffer
				syntax.NewPrinter(syntax.SingleLine(true)).Print(&inner, &syntax.ArithmCmd{X: expr})
				rendered := inner.String()
				rendered = strings.TrimPrefix(rendered, "((")
				rendered = strings.TrimSuffix(rendered, "))")
				rendered = strings.TrimSpace(rendered)
				rendered = compactArithm(rendered)
				trace.string("(( ")
				trace.string(rendered)
				// bash 5.3 quirk: trailing `++` / `--` get a
				// double space before `))` in the xtrace line.
				if strings.HasSuffix(rendered, "++") || strings.HasSuffix(rendered, "--") {
					trace.string(" ")
				}
				trace.string(" ))")
				trace.newLineFlush()
			}
			// bash 5.3: a runtime arith error in init/cond/post
			// terminates the for-loop (and sets exit status 1).
			// Clear lastArithErr at each call site we care about so
			// we can detect *this* invocation's failure.
			r.lastArithErr = nil
			if y.Init != nil {
				traceArith(y.Init)
				r.arithm(y.Init)
				if r.lastArithErr != nil {
					r.exit.code = 1
					break
				}
			}
			for {
				r.lastArithErr = nil
				if y.Cond != nil {
					traceArith(y.Cond)
					if r.arithm(y.Cond) == 0 {
						break
					}
					if r.lastArithErr != nil {
						r.exit.code = 1
						break
					}
				}
				if r.exit.exiting || r.exit.returning || r.exit.fatalExit {
					break
				}
				if r.loopStmtsBroken(ctx, cm.Do) {
					break
				}
				if y.Post != nil {
					r.lastArithErr = nil
					traceArith(y.Post)
					r.arithm(y.Post)
					if r.lastArithErr != nil {
						r.exit.code = 1
						break
					}
				}
				if y.Cond == nil {
					// infinite loop; need an explicit break
					// path — already handled above.
				}
			}
		}
	case *syntax.FuncDecl:
		// POSIX mode: a function name that shadows a special
		// builtin (`return`, `break`, `export`, etc.) is a fatal
		// error. Reject before stashing in the function table.
		name := cm.Name.Value
		if r.opts[optPosix] && isPosixSpecialBuiltin(name) {
			r.errf("%s`%s': is a special builtin\n",
				r.bashErrPrefix(cm.Position), name)
			r.exit.code = 1
			r.exit.exiting = true
			return
		}
		r.setFunc(name, cm.Body)
	case *syntax.ArithmCmd:
		if tracingEnabled {
			// bash `set -x` traces `((expr))` as
			// `+ (( <printed-expr> ))` with spaces inside the
			// double-parens and the inner expression in
			// compact, no-space-around-operator form.
			var inner bytes.Buffer
			syntax.NewPrinter(syntax.SingleLine(true)).Print(&inner, cm)
			rendered := inner.String()
			rendered = strings.TrimPrefix(rendered, "((")
			rendered = strings.TrimSuffix(rendered, "))")
			rendered = strings.TrimSpace(rendered)
			trace.string("(( ")
			trace.string(compactArithm(rendered))
			trace.string(" ))")
			trace.newLineFlush()
		}
		r.exit.oneIf(r.arithm(cm.X) == 0)
	case *syntax.LetClause:
		var val int
		for _, expr := range cm.Exprs {
			val = r.arithm(expr)

			if !tracingEnabled {
				continue
			}

			switch expr := expr.(type) {
			case *syntax.Word:
				qs, err := syntax.Quote(r.literal(expr), syntax.LangBash)
				if err != nil {
					return
				}
				trace.stringf("let %v", qs)
			case *syntax.BinaryArithm, *syntax.UnaryArithm:
				trace.expr(cm)
			case *syntax.ParenArithm:
				// TODO
			}
		}

		trace.newLineFlush()
		r.exit.oneIf(val == 0)
	case *syntax.CaseClause:
		trace.string("case ")
		trace.expr(cm.Word)
		trace.string(" in")
		trace.newLineFlush()
		// Case subject undergoes full quote-removal (per POSIX): an
		// unquoted `\X` collapses to `X` so it can be compared against
		// patterns that have their own backslash semantics.
		subj, err := expand.LiteralWithQuoteRemoval(r.ecfg, cm.Word)
		r.expandErr(err)
		str := subj
		noCaseMatch := false
		if opt, _ := r.bashOptByName("nocasematch"); opt != nil && *opt {
			noCaseMatch = true
		}
		// fallthrough is set when the previous item ended with `;&`,
		// meaning we run this item's stmts unconditionally.
		fallthroughActive := false
		for i, ci := range cm.Items {
			matched := fallthroughActive
			if !matched {
				for _, word := range ci.Patterns {
					prevCode := r.exit.code
					pat := r.pattern(word)
					// Bash: if pattern evaluation (e.g. an inner
					// `$((expr))` arithmetic) failed, bail the case
					// entirely with the resulting exit code rather
					// than running any branch.
					if r.exit.code != prevCode && r.exit.code != 0 {
						return
					}
					matchStr := str
					if noCaseMatch {
						pat = strings.ToLower(pat)
						matchStr = strings.ToLower(matchStr)
					}
					if match(pat, matchStr) {
						matched = true
						break
					}
				}
			}
			if !matched {
				continue
			}
			r.stmts(ctx, ci.Stmts)
			switch ci.Op {
			case syntax.Fallthrough:
				// `;&` — fall into the next item's body unless we're
				// already at the last one.
				if i+1 < len(cm.Items) {
					fallthroughActive = true
					continue
				}
				return
			case syntax.Resume:
				// `;;&` — keep evaluating remaining patterns.
				fallthroughActive = false
				continue
			default:
				// `;;` (Break) or trailing-item-without-op: done.
				return
			}
		}
	case *syntax.TestClause:
		r.testIntErr = ""
		r.testArithErr = ""
		result := r.bashTest(ctx, cm.X, false)
		if r.testArithErr != "" {
			r.errf("%s[[: %s: %s\n", r.bashErrPrefix(cm.Left), r.testIntErr, r.testArithErr)
			r.testIntErr = ""
			r.testArithErr = ""
			r.exit.code = 1
		} else if r.testIntErr != "" {
			r.errf(r.bashErrPrefix(cm.Left)+"[[: %s: integer expected\n", r.testIntErr)
			r.testIntErr = ""
			r.exit.code = 2
		} else if result == "" && r.exit.ok() {
			// to preserve exit status code 2 for regex errors, etc
			r.exit.code = 1
		}
	case *syntax.DeclClause:
		local, global := false, false
		var modes []string
		valType := ""
		declQuery := "" // "-f" or "-p" for query mode
		switch cm.Variant.Value {
		case "declare", "typeset":
			// When used in a function, "declare"/"typeset" act as
			// "local" unless the "-g" option is used.
			local = r.inFunc
		case "local":
			if !r.inFunc {
				r.errf("%slocal: can only be used in a function\n", r.bashErrPrefix(r.curStmtPos))
				r.exit.code = 1
				return
			}
			local = true
		case "export":
			modes = append(modes, "-x")
		case "readonly":
			modes = append(modes, "-r")
		case "nameref":
			valType = "-n"
		}
		declHadNames := false
		oldDeclCtx := r.declAssignContext
		r.declAssignContext = true
		defer func() { r.declAssignContext = oldDeclCtx }()
	assignLoop:
		for as, fromString := range r.flattenAssigns(cm.Args) {
			// Bash attributes assignment failures from a declare-
			// family builtin's string-parsed arg path
			// (`readonly 'a=v'`) to the builtin rather than the
			// enclosing function. Set the runner-level flag so the
			// setVar error printer picks it up; an inner loop
			// guarantees we clear it once this Assign is done.
			r.setVarFromBuiltin = ""
			r.setVarStringParsed = false
			r.setVarArrayLiteral = false
			// Bash 5.3 attribution for declare-family failures:
			//   no -a/-A flag       → no extra prefix
			//   -a/-A + array literal → function name
			//   -a/-A + scalar (or string-form) → builtin name
			hasArrayFlag := slices.Contains(modes, "-a") || slices.Contains(modes, "-A") || valType == "-a" || valType == "-A"
			switch {
			case !hasArrayFlag:
				r.setVarStringParsed = true // suppress any prefix
			case as.Array != nil && !fromString:
				r.setVarArrayLiteral = true // function-name attribution
			default:
				r.setVarFromBuiltin = cm.Variant.Value
			}
			fp := flagParser{remaining: []string{as.Name.Value}}
			for fp.more() {
				switch flag := fp.flag(); flag {
				case "-x", "-r":
					modes = append(modes, flag)
				case "+x", "+r":
					modes = append(modes, flag)
				case "-a", "-A", "-n", "-i":
					valType = flag
				case "+i", "+a", "+A":
					// `+X` removes attribute X. Tracked via modes
					// so setVar can clear the flag on the
					// destination variable.
					modes = append(modes, flag)
				case "+n":
					// `+n` strips the nameref attribute. Also
					// flagged via valType so assignVal targets
					// the nameref var itself, not the resolved
					// reference target.
					valType = "+n"
					modes = append(modes, flag)
				case "-u", "-l", "-c":
					// Case-conversion attributes (`declare -u/-l/-c`).
					// Tracked as additional modes; applied at assign
					// time via `setVar` and surfaced in `declare -p`
					// output via `expand.Variable.Flags`.
					modes = append(modes, flag)
				case "+u", "+l", "+c":
					modes = append(modes, flag)
				case "-g":
					global = true
				case "-f", "-F", "-p":
					declQuery = flag
				default:
					r.errf("%sdeclare: %s: invalid option\n", r.bashErrPrefix(r.curStmtPos), flag)
					r.exit.code = 2
					return
				}
				continue assignLoop
			}
			declHadNames = true
			name := as.Name.Value
			// `declare -f <name>` / `export -f <name>` operate on
			// function names; bash allows arbitrary function names
			// (e.g. `foo-a`) so skip the identifier check there.
			if declQuery != "-f" && declQuery != "-F" && !syntax.ValidName(name) {
				if r.bashCompatErrors {
					r.errf("%sdeclare: `%s': not a valid identifier\n",
						r.bashErrPrefix(r.curStmtPos), name)
				} else {
					r.errf("declare: invalid name %q\n", name)
				}
				r.exit.code = 1
				return
			}
			if declQuery == "-F" {
				// declare -F name: print just the function name.
				if body := r.Funcs[name]; body != nil {
					r.outf("%s\n", name)
				} else {
					r.exit.code = 1
				}
				continue
			}
			if declQuery == "-f" {
				// `export -f <name>` marks the function for export
				// to child processes via BASH_FUNC_<name>%%=…
				// envvar. Other `declare -f name` / `typeset -f
				// name` forms print the function definition. Bash
				// silently returns exit 1 for missing functions.
				if cm.Variant.Value == "export" {
					// Bash refuses to export functions whose name
					// can't survive the env-var round-trip — names
					// containing `=` or `/`, etc. — even when the
					// function itself does exist. The diagnostic
					// is `export: <name>: cannot export` and the
					// builtin keeps going (exit 1).
					if !validExportedFuncName(name) {
						r.errf("%sexport: %s: cannot export\n",
							r.bashErrPrefix(r.curStmtPos), name)
						r.exit.code = 1
						continue
					}
					if _, ok := r.Funcs[name]; !ok {
						r.exit.code = 1
						continue
					}
					if r.exportedFuncs == nil {
						r.exportedFuncs = make(map[string]bool)
					}
					r.exportedFuncs[name] = true
					continue
				}
				if body := r.Funcs[name]; body != nil {
					// `readonly -f NAME` marks the function as
					// read-only (it can't be unset or redefined).
					if cm.Variant.Value == "readonly" {
						if r.readonlyFuncs == nil {
							r.readonlyFuncs = make(map[string]bool)
						}
						r.readonlyFuncs[name] = true
						continue
					}
					r.printFuncDecl(name, body)
				} else {
					r.exit.code = 1
				}
				continue
			}
			if declQuery == "-p" {
				// declare -p name: print variable with attributes.
				vr := r.lookupVar(name)
				if !vr.Declared() {
					r.errf(r.bashErrPrefix(r.curStmtPos)+"declare: %s: not found\n", name)
					r.exit.code = 1
					continue
				}
				flags := vr.Flags()
				if flags == "" {
					flags = "-"
				}
				switch vr.Kind {
				case expand.Indexed:
					r.outf("declare -%s %s=(", flags, name)
					for i, v := range vr.List {
						if i > 0 {
							r.out(" ")
						}
						r.outf("[%d]=%s", i, bashDeclareQuote(v))
					}
					r.out(")\n")
				case expand.Associative:
					r.outf("declare -%s %s=(", flags, name)
					first := true
					for _, k := range expand.AssocKeysInBashOrder(vr.Map) {
						v := vr.Map[k]
						if !first {
							r.out(" ")
						}
						r.outf("[%s]=%s", k, bashDeclareQuote(v))
						first = false
					}
					// Bash 5.3 prints a trailing space before the
					// closing paren of an associative-array literal
					// when the map is non-empty.
					if !first {
						r.out(" ")
					}
					r.out(")\n")
				default:
					r.outf("declare -%s %s=%s\n", flags, name, bashDeclareQuote(vr.Str))
				}
				continue
			}
			vr := r.lookupVar(name)
			// Set the Integer attribute *before* assignVal so the
			// initial assignment can evaluate the RHS as arithmetic.
			if valType == "-i" {
				vr.Integer = true
			}
			if as.Naked {
				switch valType {
				case "-A":
					vr.Kind = expand.Associative
				case "-n":
					// `typeset -n NAME` (no value) on an
					// existing var converts it to a nameref
					// pointing at whatever its current value
					// names (bash 5.3). Preserve the scalar
					// value as the reference target.
					vr.Kind = expand.NameRef
				default:
					vr.Kind = expand.KeepValue
				}
			} else {
				name, vr = r.assignVal(name, vr, as, valType)
			}
			// `typeset +n NAME=value` semantics: the assignment
			// goes to the nameref's resolved target (handled
			// above), then the nameref attribute is stripped
			// from the ORIGINAL name. The original keeps its
			// value (the target name), e.g. `foo→bar` →
			// `foo="bar"` after `+n foo=...`.
			if valType == "+n" {
				origName := as.Name.Value
				if origName != name {
					r.setVar(name, vr) // assign target first
					orig := r.lookupVar(origName)
					if orig.Kind == expand.NameRef {
						orig.Kind = expand.String
						r.setVar(origName, orig)
					}
					continue
				}
			}
			if global {
				vr.Local = false
			} else if local {
				vr.Local = true
				// `typeset OPTIND=N` (or `local OPTIND=N`) inside a
				// function resets bash's getopts internal pointers
				// to track the new value. Without this, the
				// caller's char-position state stays around and
				// reads into the new argv at the old offset, which
				// loops forever in recursive-getopts patterns.
				if name == "OPTIND" {
					r.optState = getopts{}
				}
			}
			for _, mode := range modes {
				switch mode {
				case "-x":
					vr.Exported = true
				case "+x":
					vr.Exported = false
				case "-r":
					vr.ReadOnly = true
				case "+r":
					// Bash refuses to remove the readonly
					// attribute once set; skip silently.
				case "-u":
					vr.Upper, vr.Lower, vr.Capitalize = true, false, false
				case "+u":
					vr.Upper = false
				case "-l":
					vr.Upper, vr.Lower, vr.Capitalize = false, true, false
				case "+l":
					vr.Lower = false
				case "-c":
					vr.Upper, vr.Lower, vr.Capitalize = false, false, true
				case "+c":
					vr.Capitalize = false
				case "+i":
					vr.Integer = false
				case "+n":
					// `+n` strips the nameref attribute and
					// returns the variable to a plain scalar
					// whose value is whatever the nameref was
					// pointing at.
					if vr.Kind == expand.NameRef {
						vr.Kind = expand.String
					}
				}
			}
			// Apply case-conversion attributes to the current value
			// so `declare -u foo; foo=$TEXT` immediately stores the
			// folded form.
			applyCaseAttr(&vr)
			// `typeset -n NAME=target` on an existing readonly NAME
			// is silently a no-op in bash 5.3 (the nameref conversion
			// is treated as a type change, not an assignment, so the
			// readonly attribute doesn't block it; but since we don't
			// model nameref-with-readonly conversions fully, leave
			// the prior readonly scalar untouched and skip the error).
			if valType == "-n" {
				if prev := r.lookupVar(name); prev.ReadOnly {
					continue
				}
			}
			r.setVar(name, vr)
		}
		// Handle declare -F/-f with no arguments: list all functions.
		// Bash sorts the listing by function name. `readonly -f`
		// (no args) lists only read-only functions, each suffixed
		// with `declare -fr NAME`.
		if !declHadNames && (declQuery == "-F" || declQuery == "-f") {
			readonlyOnly := cm.Variant.Value == "readonly"
			names := make([]string, 0, len(r.Funcs))
			for name := range r.Funcs {
				if readonlyOnly && !r.readonlyFuncs[name] {
					continue
				}
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				if declQuery == "-F" {
					r.outf("declare -f %s\n", name)
					continue
				}
				r.printFuncDecl(name, r.Funcs[name])
				if readonlyOnly {
					r.outf("declare -fr %s\n", name)
				}
			}
		}
		// Bash `local` with no args lists every variable local to
		// the current function scope in `name=value` form (arrays
		// rendered as `name=([0]="..." [1]="...")`).
		if !declHadNames && cm.Variant.Value == "local" && r.inFunc {
			r.printLocalVars()
		}
		// `typeset -n` / `declare -n` with no name lists every
		// nameref variable in `declare -n NAME="target"` form.
		if !declHadNames && valType == "-n" && declQuery == "" {
			r.printNamerefVars()
		}
		// `declare -A` / `declare -a` with no name lists every
		// array variable of that kind, including bash's built-in
		// arrays (BASH_ALIASES, BASH_CMDS, BASH_ARGC, …).
		if !declHadNames && (valType == "-A" || valType == "-a") && declQuery == "" {
			r.printArrayVars(valType)
		}
	case *syntax.TimeClause:
		// bash 5.3 only prints timing output for the outermost
		// `time` keyword in a stack of nested `time` clauses;
		// inner ones are absorbed by the outer measurement.
		outer := !r.inTimeClause
		r.inTimeClause = true
		start := time.Now()
		if cm.Stmt != nil {
			r.stmt(ctx, cm.Stmt)
		}
		real := time.Since(start)
		if !outer {
			break
		}
		r.inTimeClause = false
		var user, sys time.Duration // not tracked
		if cm.PosixFormat {
			r.outf("real %s\n", elapsedString(real, true))
			r.outf("user %s\n", elapsedString(user, true))
			r.outf("sys %s\n", elapsedString(sys, true))
		} else if format := r.envGet("TIMEFORMAT"); format != "" {
			r.outf("%s\n", formatTIMEFORMAT(format, real, user, sys))
		} else {
			r.outf("\nreal\t%s\nuser\t%s\nsys\t%s\n",
				elapsedString(real, false),
				elapsedString(user, false),
				elapsedString(sys, false))
		}
	case *syntax.CoprocClause:
		// Coproc runs a command in the background with stdin/stdout connected via pipes.
		// Note: bash coproc exposes the child's pipes as ${NAME[0]} / ${NAME[1]},
		// which are numeric fds usable in `<&N` / `>&N` redirects. This runner's
		// redirect layer only handles fds 0/1/2, so those redirect forms won't
		// work yet, but the basic coproc + read/write via the pipe object works.
		pr, pw, err := os.Pipe()
		if err != nil {
			r.exit.fatal(err)
			break
		}
		pr2, pw2, err := os.Pipe()
		if err != nil {
			pr.Close()
			pw.Close()
			r.exit.fatal(err)
			break
		}
		r2 := r.subshell(true)
		r2.stdin = pr2
		r2.stdout = pw

		// Set COPROC array with read and write file descriptor numbers.
		// Also register them in fdTable so `<&"${COPROC[0]}"` and
		// `>&"${COPROC[1]}"` work in scripts that use the array. Without
		// this, the redirect layer would see the numeric arg, fail the
		// fd lookup, and return "bad fd number".
		varName := "COPROC"
		if cm.Name != nil {
			varName = r.literal(cm.Name)
		}
		readFd := int(pr.Fd())
		writeFd := int(pw2.Fd())
		r.setVar(varName, expand.Variable{
			Set:  true,
			Kind: expand.Indexed,
			List: []string{
				strconv.Itoa(readFd),
				strconv.Itoa(writeFd),
			},
		})
		if r.fdTable == nil {
			r.fdTable = make(map[int]*os.File)
		}
		r.fdTable[readFd] = pr
		r.fdTable[writeFd] = pw2

		bg := &bgProc{
			done: make(chan struct{}),
			exit: new(exitStatus),
		}
		r.bgProcs = append(r.bgProcs, bg)
		go func() {
			defer func() {
				pw.Close()
				pr2.Close()
				*bg.exit = r2.exit
				close(bg.done)
			}()
			r2.Run(ctx, cm.Stmt)
			r2.exit.exiting = false
		}()
	default:
		// Should only happen if we forgot a case above.
		r.errf("unhandled command node: %T\n", cm)
		r.exit.code = 1
	}
}

func (r *Runner) trapCallback(ctx context.Context, callback, name string) uint8 {
	if callback == "" {
		return 0 // nothing to do
	}
	if r.handlingTrap {
		return 0 // don't recurse, as that could lead to cycles
	}
	r.handlingTrap = true

	p := syntax.NewParser()
	// TODO: do this parsing when "trap" is called?
	file, err := p.Parse(strings.NewReader(callback), name+" trap")
	if err != nil {
		r.errf(name+"trap: %v\n", err)
		// ignore errors in the callback
		r.handlingTrap = false
		return 0
	}
	oldExit := r.exit
	r.exit = exitStatus{} // start fresh so we can capture the trap's exit
	r.stmts(ctx, file.Stmts)
	trapCode := r.exit.code
	r.exit = oldExit // traps on EXIT or ERR should not modify the result

	r.handlingTrap = false
	return trapCode
}

// flattenAssigns yields each effective syntax.Assign from a declare-
// family clause's args. The second return value, fromString, is true
// when the Assign was synthesized from a string-form arg (`readonly
// 'name=val'`) rather than parsed as a syntax-level assignment
// (`readonly name=val`). Bash 5.3 attributes assignment-failure error
// messages differently for the two paths, so the caller needs to know.
//
// Pre-scans args for `-f` / `-F` so a function-mode invocation keeps
// the full string as the function name (functions can be named
// `foo=bar` or `/bin/echo`) instead of splitting on `=`.
func (r *Runner) flattenAssigns(args []*syntax.Assign) iter.Seq2[*syntax.Assign, bool] {
	funcMode := false
	for _, as := range args {
		if as.Name != nil || as.Value == nil {
			continue
		}
		if lit := as.Value.Lit(); lit == "-f" || lit == "-F" {
			funcMode = true
			break
		}
	}
	return func(yield func(*syntax.Assign, bool) bool) {
		for _, as := range args {
			// Convert "declare $x" into "declare value".
			// Don't use syntax.Parser here, as we only want the basic
			// splitting by '='.
			if as.Name != nil {
				if !yield(as, false) {
					return
				}
				continue
			}
			for _, field := range r.fields(as.Value) {
				as := &syntax.Assign{}
				name, val, ok := strings.Cut(field, "=")
				if funcMode && !strings.HasPrefix(field, "-") {
					// `export -f NAME` / `declare -f NAME` —
					// keep the full field as the function name,
					// even if it contains `=`. Option flags
					// (starting with `-`) still get parsed by
					// the option loop below.
					name = field
					ok = false
				}
				as.Name = &syntax.Lit{Value: name}
				if !ok {
					as.Naked = true
				} else {
					as.Value = &syntax.Word{Parts: []syntax.WordPart{
						&syntax.Lit{Value: val},
					}}
				}
				if !yield(as, true) {
					return
				}
			}
		}
	}
}

func match(pat, name string) bool {
	matcher, err := internal.ExtendedPatternMatcher(pat, pattern.EntireString|pattern.ExtendedOperators|pattern.LenientRanges)
	_ = err // TODO: report these errors
	return matcher != nil && matcher(name)
}

// formatTIMEFORMAT renders the durations against bash's TIMEFORMAT
// directives: `%[l][p]{R,U,S}` for real/user/sys with optional `l`
// (mins+secs) prefix and a 0-3 precision digit; `%P` for %CPU; `%%`
// for literal `%`; backslash escapes `\n` `\t` `\\` `\?` mapped to
// their C equivalents (with unknown sequences emitted verbatim).
func formatTIMEFORMAT(format string, real, user, sys time.Duration) string {
	var sb strings.Builder
	emit := func(d time.Duration, longForm bool, prec int) {
		if longForm {
			min := int(d.Minutes())
			sec := math.Mod(d.Seconds(), 60.0)
			fmt.Fprintf(&sb, "%dm%.*fs", min, prec, sec)
			return
		}
		fmt.Fprintf(&sb, "%.*f", prec, d.Seconds())
	}
	for i := 0; i < len(format); i++ {
		switch format[i] {
		case '\\':
			if i+1 >= len(format) {
				sb.WriteByte('\\')
				continue
			}
			i++
			switch format[i] {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case '\\':
				sb.WriteByte('\\')
			default:
				sb.WriteByte('\\')
				sb.WriteByte(format[i])
			}
		case '%':
			i++
			longForm := false
			prec := 3
			if i < len(format) && format[i] == 'l' {
				longForm = true
				i++
			}
			if i < len(format) && format[i] >= '0' && format[i] <= '9' {
				prec = int(format[i] - '0')
				i++
				if i < len(format) && format[i] == 'l' {
					longForm = true
					i++
				}
			}
			if i >= len(format) {
				sb.WriteByte('%')
				return sb.String()
			}
			switch format[i] {
			case 'R':
				emit(real, longForm, prec)
			case 'U':
				emit(user, longForm, prec)
			case 'S':
				emit(sys, longForm, prec)
			case 'P':
				// %CPU — bash computes (user+sys)/real*100. We
				// don't track user/sys, so emit "0.00" so the
				// directive expands to something parseable.
				fmt.Fprintf(&sb, "%.*f", prec, 0.0)
			case '%':
				sb.WriteByte('%')
			default:
				sb.WriteByte('%')
				sb.WriteByte(format[i])
			}
		default:
			sb.WriteByte(format[i])
		}
	}
	return sb.String()
}

func elapsedString(d time.Duration, posix bool) string {
	if posix {
		return fmt.Sprintf("%.2f", d.Seconds())
	}
	min := int(d.Minutes())
	sec := math.Mod(d.Seconds(), 60.0)
	return fmt.Sprintf("%dm%.3fs", min, sec)
}

func (r *Runner) stmts(ctx context.Context, stmts []*syntax.Stmt) {
	for _, stmt := range stmts {
		r.stmt(ctx, stmt)
	}
}

func (r *Runner) hdocReader(rd *syntax.Redirect) (*os.File, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	// We write to the pipe in a new goroutine,
	// as pipe writes may block once the buffer gets full.
	// We still construct and buffer the entire heredoc first,
	// as doing it concurrently would lead to different semantics and be racy.
	if rd.Op != syntax.DashHdoc {
		hdoc := r.document(rd.Hdoc)
		go func() {
			pw.WriteString(hdoc)
			pw.Close()
		}()
		return pr, nil
	}
	var buf bytes.Buffer
	var cur []syntax.WordPart
	flushLine := func() {
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(r.document(&syntax.Word{Parts: cur}))
		cur = cur[:0]
	}
	for _, wp := range rd.Hdoc.Parts {
		lit, ok := wp.(*syntax.Lit)
		if !ok {
			cur = append(cur, wp)
			continue
		}
		first := true
		for part := range strings.SplitSeq(lit.Value, "\n") {
			if !first {
				flushLine()
				cur = cur[:0]
			}
			first = false
			part = strings.TrimLeft(part, "\t")
			cur = append(cur, &syntax.Lit{Value: part})
		}
	}
	flushLine()
	go func() {
		pw.Write(buf.Bytes())
		pw.Close()
	}()
	return pr, nil
}

// allocateFd returns the next unused fd number >= 10, suitable for
// {varname} named-fd allocations. Bash picks fd numbers starting at
// 10 to avoid colliding with the conventional stdio range (0/1/2) and
// with fds a script may have explicitly assigned (3-9).
func (r *Runner) allocateFd() int {
	for n := 10; ; n++ {
		if _, ok := r.fdTable[n]; !ok {
			return n
		}
	}
}

// setReadFd binds f as a readable source for the given target fd.
// targetFd == -1 means "use the input default (fd 0 / r.stdin)". For 0
// we set r.stdin; for N >= 3 we store in fdTable. 1/2 are not valid
// input targets in bash and are rejected.
func (r *Runner) setReadFd(targetFd int, f *os.File) error {
	switch targetFd {
	case -1, 0:
		r.stdin = f
	case 1, 2:
		return fmt.Errorf("cannot use fd %d as input target", targetFd)
	default:
		if r.fdTable == nil {
			r.fdTable = make(map[int]*os.File)
		}
		r.fdTable[targetFd] = f
	}
	return nil
}

// setWriteFd binds w as an output sink for the given target fd.
// targetFd == -1 means "use the output default (fd 1 / r.stdout)".
// For 1 we set r.stdout, for 2 r.stderr (both accept any io.Writer).
// For N >= 3 we store in fdTable, which requires *os.File since a
// numbered fd must back a real OS handle.
func (r *Runner) setWriteFd(targetFd int, w io.Writer) error {
	switch targetFd {
	case -1, 1:
		r.stdout = w
	case 2:
		r.stderr = w
	case 0:
		return fmt.Errorf("cannot use fd 0 as output target")
	default:
		f, ok := w.(*os.File)
		if !ok {
			return fmt.Errorf("non-file writer cannot be redirected to fd %d", targetFd)
		}
		if r.fdTable == nil {
			r.fdTable = make(map[int]*os.File)
		}
		r.fdTable[targetFd] = f
	}
	return nil
}

func (r *Runner) redir(ctx context.Context, rd *syntax.Redirect) (io.Closer, error) {
	if rd.Hdoc != nil {
		pr, err := r.hdocReader(rd)
		if err != nil {
			return nil, err
		}
		// `exec {v}<<EOF` form: route the heredoc reader through a
		// fresh fd and stash that fd in $v (globally — bash 5.3
		// makes named-fd assignments visible outside any enclosing
		// function).
		if rd.N != nil {
			val := rd.N.Value
			if strings.HasPrefix(val, "{") && strings.HasSuffix(val, "}") {
				name := val[1 : len(val)-1]
				fd := r.allocateFd()
				if err := r.setReadFd(fd, pr); err != nil {
					return nil, err
				}
				r.setGlobalVarString(name, strconv.Itoa(fd))
				return pr, nil
			}
		}
		r.stdin = pr
		return pr, nil
	}

	// Bash: when the target word of a non-heredoc redirect expands
	// to zero or more than one field, emit "ambiguous redirect".
	// Skip the check for here-string (`<<<`) since the entire word
	// is treated as the body, not a filename.
	if rd.Op != syntax.WordHdoc {
		fields := r.fields(rd.Word)
		if len(fields) != 1 {
			var b bytes.Buffer
			syntax.NewPrinter().Print(&b, rd.Word)
			r.errf("%s%s: ambiguous redirect\n", r.bashErrPrefix(rd.Word.Pos()), b.String())
			return nil, fmt.Errorf("ambiguous redirect")
		}
	}
	arg := r.literal(rd.Word)
	// targetFd is the fd this redirect operates on. -1 means "use the
	// op's natural default" (fd 0 for input, fd 1 for output). N >= 3
	// goes through fdTable; 1/2 are stdin/stdout/stderr.
	targetFd := -1
	var namedFDVar string // non-empty if this is a {varname} redirect to be written back
	if rd.N != nil {
		val := rd.N.Value
		// Named FD redirection: {varname}> or {varname}<
		if strings.HasPrefix(val, "{") && strings.HasSuffix(val, "}") {
			name := val[1 : len(val)-1]
			// `{var}>&-` and `{var}<&-` are the close form: read the
			// fd already stored in $var, target it for deletion, and
			// don't write back (we keep $var with its stale number,
			// matching bash).
			if (rd.Op == syntax.DplOut || rd.Op == syntax.DplIn) && arg == "-" {
				fdStr := r.lookupVar(name).String()
				n, err := strconv.Atoi(fdStr)
				if err != nil || n < 0 {
					return nil, fmt.Errorf("invalid fd in $%s: %q", name, fdStr)
				}
				targetFd = n
			} else {
				// Bash 5.3 refuses `{var}>...` when var is readonly,
				// emitting two diagnostics — `<file>: line N: <var>:
				// readonly variable` and `<file>: line N: <var>:
				// cannot assign fd to variable` — before abandoning
				// the redirect.
				if r.lookupVar(name).ReadOnly {
					prefix := r.bashErrPrefix(rd.Pos())
					r.errf("%s%s: readonly variable\n", prefix, name)
					r.errf("%s%s: cannot assign fd to variable\n", prefix, name)
					return nil, fmt.Errorf("%s: cannot assign fd to variable", name)
				}
				// Open form: pick a fresh fd for the script.
				targetFd = r.allocateFd()
				namedFDVar = name
			}
		} else {
			n, err := strconv.Atoi(val)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("unsupported redirect fd: %v", val)
			}
			targetFd = n
		}
	}
	switch rd.Op {
	case syntax.WordHdoc:
		pr, pw, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		// hdoc routes to the input slot (default fd 0); allow N<<EOF too.
		if err := r.setReadFd(targetFd, pr); err != nil {
			return nil, err
		}
		if namedFDVar != "" {
			r.setGlobalVarString(namedFDVar, strconv.Itoa(targetFd))
		}
		// We write to the pipe in a new goroutine,
		// as pipe writes may block once the buffer gets full.
		go func() {
			pw.WriteString(arg)
			pw.WriteString("\n")
			pw.Close()
		}()
		return pr, nil
	case syntax.DplOut:
		// >&M — point the target fd at whatever fd M references.
		switch arg {
		case "-":
			// Closing form: >&- removes the fd binding rather than
			// pointing it elsewhere. For default (stdout) we plug
			// io.Discard; for stderr we plug io.Discard too; for
			// fdTable entries we delete the entry.
			switch targetFd {
			case -1, 1:
				r.stdout = io.Discard
			case 2:
				r.stderr = io.Discard
			default:
				delete(r.fdTable, targetFd)
			}
			return nil, nil
		}
		var w io.Writer
		switch arg {
		case "1":
			w = r.stdout
		case "2":
			w = r.stderr
		default:
			n, err := strconv.Atoi(arg)
			if err == nil && n < 0 {
				// Bash: negative fd in `>&N` is an
				// "ambiguous redirect" before any open.
				r.errf("%s%s: ambiguous redirect\n", r.bashErrPrefix(rd.Pos()), arg)
				return nil, fmt.Errorf("ambiguous redirect")
			}
			if err != nil {
				return nil, fmt.Errorf("unhandled %v arg: %q", rd.Op, arg)
			}
			f, ok := r.fdTable[n]
			if !ok {
				return nil, fmt.Errorf("%v: bad fd number %q", rd.Op, arg)
			}
			w = f
		}
		if err := r.setWriteFd(targetFd, w); err != nil {
			return nil, err
		}
		if namedFDVar != "" {
			r.setGlobalVarString(namedFDVar, strconv.Itoa(targetFd))
		}
		return nil, nil
	case syntax.DplIn:
		// <&M — point the target input fd at fd M's reader.
		if arg == "-" {
			switch targetFd {
			case -1, 0:
				r.stdin = nil
			default:
				delete(r.fdTable, targetFd)
			}
			return nil, nil
		}
		var f *os.File
		switch arg {
		case "0":
			f = r.stdin
		default:
			n, err := strconv.Atoi(arg)
			if err == nil && n < 0 {
				// Bash: negative fd in `<&N` is an
				// "ambiguous redirect".
				r.errf("%s%s: ambiguous redirect\n", r.bashErrPrefix(rd.Pos()), arg)
				return nil, fmt.Errorf("ambiguous redirect")
			}
			if err != nil {
				return nil, fmt.Errorf("unhandled %v arg: %q", rd.Op, arg)
			}
			var ok bool
			f, ok = r.fdTable[n]
			if !ok {
				return nil, fmt.Errorf("%v: bad fd number %q", rd.Op, arg)
			}
		}
		if err := r.setReadFd(targetFd, f); err != nil {
			return nil, err
		}
		if namedFDVar != "" {
			r.setGlobalVarString(namedFDVar, strconv.Itoa(targetFd))
		}
		return nil, nil
	case syntax.RdrIn, syntax.RdrOut, syntax.AppOut,
		syntax.RdrAll, syntax.AppAll,
		syntax.RdrClob, syntax.AppClob,
		syntax.RdrAllClob, syntax.AppAllClob,
		syntax.RdrInOut:
		// File-opening fall through.
		// The "Clob" variants (>|, >>|, &>|, &>>|) bypass the noclobber
		// shell option (set -C). Since this interpreter does not enforce
		// noclobber on file redirects, they are functionally identical to
		// their plain counterparts.
		// RdrInOut (<>) opens the target file for read+write and binds it
		// to the input fd (default 0); we read from the resulting file as
		// stdin. Writes back through fd 0 are not propagated to the file
		// since stdin is plumbed as io.Reader internally.
	default:
		return nil, fmt.Errorf("unhandled redirect op: %v", rd.Op)
	}
	mode := os.O_RDONLY
	switch rd.Op {
	case syntax.AppOut, syntax.AppAll, syntax.AppClob, syntax.AppAllClob:
		mode = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	case syntax.RdrOut, syntax.RdrAll, syntax.RdrClob, syntax.RdrAllClob:
		mode = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	case syntax.RdrInOut:
		mode = os.O_RDWR | os.O_CREATE
	}
	f, err := r.open(ctx, arg, mode, 0o644, true)
	if err != nil {
		return nil, err
	}
	switch rd.Op {
	case syntax.RdrIn, syntax.RdrInOut:
		stdin, err := stdinFile(f)
		if err != nil {
			return nil, err
		}
		if err := r.setReadFd(targetFd, stdin); err != nil {
			return nil, err
		}
	case syntax.RdrOut, syntax.AppOut, syntax.RdrClob, syntax.AppClob:
		if err := r.setWriteFd(targetFd, f); err != nil {
			return nil, err
		}
	case syntax.RdrAll, syntax.AppAll, syntax.RdrAllClob, syntax.AppAllClob:
		// &> and &>> redirect both stdout and stderr; rd.N is ignored.
		r.stdout = f
		r.stderr = f
	default:
		return nil, fmt.Errorf("unhandled redirect op: %v", rd.Op)
	}
	// For named FD redirections that opened a real fd, write the
	// allocated fd number to the variable. Bash callers then use it
	// via `>&$var` / `<&$var` (which hit the numeric arg branch above).
	if namedFDVar != "" {
		r.setVarString(namedFDVar, strconv.Itoa(targetFd))
	}
	return f, nil
}

// selectLoop implements bash's `select var in items; do ...; done`.
// Each iteration prints the numbered menu to stderr, prompts with PS3,
// reads a line into REPLY, sets var to items[N-1] when the reply is a
// valid integer 1..len(items) (otherwise var becomes empty), and runs
// the body. An empty reply re-displays the menu without running the
// body. EOF (Ctrl-D) exits the loop with exit code 1, matching bash.
func (r *Runner) selectLoop(ctx context.Context, name string, items []string, do []*syntax.Stmt) {
	// Bash 5.3: a `select` with an empty item list (because the
	// optional `in <list>` was omitted and `$@` is empty, or the
	// list expanded to nothing) exits immediately without
	// prompting.
	if len(items) == 0 {
		return
	}
	ps3 := shellDefaultPS3
	if e := r.envGet(shellReplyPS3Var); e != "" {
		ps3 = e
	}
	for {
		var reply string
		// Re-display menu until the user supplies a non-empty reply
		// (matching bash, which suppresses the body run on empty input).
		for {
			for i, word := range items {
				r.errf("%d) %s\n", i+1, word)
			}
			r.errf("%s", ps3)
			line, err := r.readLine(ctx, true, '\n')
			if err != nil {
				// EOF: exit the loop. Bash exits with status 1.
				r.exit.code = 1
				return
			}
			reply = string(line)
			r.setVarString(shellReplyVar, reply)
			if reply != "" {
				break
			}
		}
		c, _ := strconv.Atoi(reply)
		if c > 0 && c <= len(items) {
			r.setVarString(name, items[c-1])
		} else {
			r.setVarString(name, "")
		}
		if r.loopStmtsBroken(ctx, do) {
			return
		}
	}
}

func (r *Runner) loopStmtsBroken(ctx context.Context, stmts []*syntax.Stmt) bool {
	oldInLoop := r.inLoop
	r.inLoop = true
	defer func() { r.inLoop = oldInLoop }()
	for _, stmt := range stmts {
		r.stmt(ctx, stmt)
		if r.contnEnclosing > 0 {
			r.contnEnclosing--
			return r.contnEnclosing > 0
		}
		if r.breakEnclosing > 0 {
			r.breakEnclosing--
			return true
		}
	}
	return false
}

func (r *Runner) call(ctx context.Context, pos syntax.Pos, args []string) {
	if r.stop(ctx) {
		return
	}
	// Set BASH_COMMAND and fire DEBUG trap before each simple command.
	r.setVarString("BASH_COMMAND", strings.Join(args, " "))
	// While the DEBUG trap body is being expanded, $LINENO should
	// resolve to the line of the command that triggered the trap.
	prevLineno := r.ecfg.OverrideLineno
	r.ecfg.OverrideLineno = int(pos.Line())
	debugCode := r.trapCallback(ctx, r.trapCallbacks["DEBUG"], "debug")
	r.ecfg.OverrideLineno = prevLineno
	// Bash: with `shopt -s extdebug`, a DEBUG trap that returns 2
	// skips execution of the next command (but doesn't terminate
	// the shell). The trap-callback already restored r.exit, so we
	// just bail out of call() before dispatch.
	if opt, _ := r.bashOptByName("extdebug"); opt != nil && *opt && debugCode == 2 {
		return
	}
	if r.callHandler != nil {
		var err error
		args, err = r.callHandler(r.handlerCtx(ctx, handlerKindCall, pos), args)
		if err != nil {
			// handler's custom fatal error
			r.exit.fatal(err)
			return
		}
	}
	name := args[0]
	// Bash POSIX mode: POSIX special builtins outrank shell
	// functions during command lookup. Skip the function dispatch
	// so a function defined before POSIX mode was enabled doesn't
	// shadow the builtin (`break`, `return`, `exit`, …).
	if r.opts[optPosix] && isPosixSpecialBuiltin(name) && IsBuiltin(name) {
		// fall through to builtin/exec dispatch below
	} else if body := r.Funcs[name]; body != nil {
		// Honor $FUNCNEST: when set to a positive integer, bash aborts
		// once nesting reaches that depth. An unset, empty, zero, or
		// non-numeric value disables the limit.
		if limit, _ := strconv.Atoi(r.envGet("FUNCNEST")); limit > 0 && len(r.callStack) >= limit {
			r.errf("%s%s: maximum function nesting level exceeded (%d)\n",
				r.bashErrPrefix(pos), name, limit)
			r.exit.code = 1
			return
		}

		// stack them to support nested func calls
		oldParams := r.Params
		r.Params = args[1:]
		oldInFunc := r.inFunc
		r.inFunc = true
		// Bash 5.3: if OPTIND is local in the called function (via
		// `typeset OPTIND=1` or similar), getopts processes the
		// nested args independently of the caller, and on return the
		// caller's getopts state is restored. We model that by
		// snapshotting r.optState and restoring it at return.
		oldOptState := r.optState
		// $LINENO override only applies to the trap text itself,
		// not to functions called from the trap — those should see
		// their own body line numbers.
		oldOverrideLineno := r.ecfg.OverrideLineno
		r.ecfg.OverrideLineno = 0

		// Push call stack frame.
		r.callStack = append(r.callStack, callFrame{
			line:     pos.Line(),
			source:   r.filename,
			funcName: name,
		})

		// Functions run in a nested scope.
		// Note that [Runner.exec] below does something similar.
		origEnv := r.writeEnv
		r.writeEnv = &overlayEnviron{parent: r.writeEnv, funcScope: true}

		r.stmt(ctx, body)

		r.writeEnv = origEnv

		r.trapCallback(ctx, r.trapCallbacks["RETURN"], "return")
		r.callStack = r.callStack[:len(r.callStack)-1]
		r.Params = oldParams
		r.inFunc = oldInFunc
		r.optState = oldOptState
		r.ecfg.OverrideLineno = oldOverrideLineno
		r.exit.returning = false
		return
	}
	if IsBuiltin(name) && !r.disabledBuiltins[name] {
		r.exit = r.builtin(ctx, pos, name, args[1:])
		return
	}
	// autocd: if command not found but is a directory, cd to it.
	if opt, _ := r.bashOptByName("autocd"); opt != nil && *opt {
		if info, err := r.stat(ctx, name); err == nil && info.IsDir() {
			r.exit = r.builtin(ctx, pos, "cd", []string{name})
			return
		}
	}
	r.exec(ctx, pos, args)
}

func (r *Runner) exec(ctx context.Context, pos syntax.Pos, args []string) {
	r.execAs(ctx, pos, "", args)
}

// execAs is like exec but advertises argv0 to the exec handler via
// [HandlerContext.ExecAs], so handlers can launch the spawned process
// under a different argv[0] (the "exec -a NAME CMD" form in bash).
// An empty argv0 means no override.
func (r *Runner) execAs(ctx context.Context, pos syntax.Pos, argv0 string, args []string) {
	hctx := r.handlerCtx(ctx, handlerKindExec, pos)
	if argv0 != "" {
		hc := HandlerCtx(hctx)
		hc.ExecAs = argv0
		hctx = context.WithValue(hctx, handlerCtxKey{}, hc)
	}
	// Audit hook fires before exec, after all resolution and
	// expansion. Builtins are dispatched elsewhere; this is the
	// real-process boundary.
	if r.auditHandler != nil && len(args) > 0 {
		r.auditHandler(AuditEvent{
			Args:      args,
			Pos:       pos,
			Filename:  r.filename,
			IsBuiltin: false,
		})
	}
	r.exit.fromHandlerError(r.execHandler(hctx, args))
}

func (r *Runner) open(ctx context.Context, path string, flags int, mode os.FileMode, print bool) (io.ReadWriteCloser, error) {
	// Apply this Runner's virtual umask when creating a file. The
	// process-wide syscall umask is never touched (see Runner.umask),
	// so we have to mask the mode here before passing it down.
	if flags&os.O_CREATE != 0 {
		mode &^= os.FileMode(r.umask)
	}
	// If we are opening a FIFO temporary file created by the interpreter itself,
	// don't pass this along to the open handler as it will not work at all
	// unless [os.OpenFile] is used directly with it.
	// Matching by directory and basename prefix isn't perfect, but works.
	//
	// If we want FIFOs to use a handler in the future, they probably
	// need their own separate handler API matching Unix-like semantics.
	dir, name := filepath.Split(path)
	dir = strings.TrimSuffix(dir, "/")
	if dir == r.tempDir && strings.HasPrefix(name, fifoNamePrefix) {
		return os.OpenFile(path, flags, mode)
	}

	f, err := r.openHandler(r.handlerCtx(ctx, handlerKindOpen, todoPos), path, flags, mode)
	// TODO: support wrapped PathError returned from openHandler.
	switch err.(type) {
	case nil:
		return f, nil
	case *os.PathError:
		if print {
			if r.bashCompatErrors {
				r.errf("%s%s: %s\n", r.bashErrPrefix(r.curStmtPos), path, bashOSError(err))
			} else {
				r.errf("%v\n", err)
			}
		}
	default: // handler's custom fatal error
		r.exit.fatal(err)
	}
	return nil, err
}

func (r *Runner) stat(ctx context.Context, name string) (fs.FileInfo, error) {
	path := absPath(r.Dir, name)
	return r.statHandler(ctx, path, true)
}

func (r *Runner) lstat(ctx context.Context, name string) (fs.FileInfo, error) {
	path := absPath(r.Dir, name)
	return r.statHandler(ctx, path, false)
}
