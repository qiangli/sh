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

	fifoNamePrefix = "sh-interp-"
)

func (r *Runner) fillExpandConfig(ctx context.Context) {
	r.ectx = ctx
	r.ecfg = &expand.Config{
		Env: expandEnv{r},
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
			r2 := r.subshell(false)
			r2.stdout = w
			// inherit_errexit: command substitutions inherit the errexit option.
			if opt, _ := r.bashOptByName("inherit_errexit"); opt != nil && *opt {
				r2.opts[optErrExit] = r.opts[optErrExit]
			}
			r2.stmts(ctx, cs.Stmts)
			r2.exit.exiting = false // subshells don't exit the parent shell
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
	r.expandErr(err)
	return n
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

func (r *Runner) stop(ctx context.Context) bool {
	// Some traps trigger on exit, so we do want those to run.
	if !r.handlingTrap && (r.exit.returning || r.exit.exiting) {
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

	oldIn, oldOut, oldErr := r.stdin, r.stdout, r.stderr
	// Snapshot fdTable only when this statement has redirects that
	// might mutate it. A coproc statement registers fds in fdTable from
	// inside cmd() itself, not via redir(), and those changes must
	// persist past stmtSync; restoring unconditionally would wipe them.
	var oldFdTable map[int]*os.File
	if len(st.Redirs) > 0 {
		oldFdTable = maps.Clone(r.fdTable)
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
		r.cmd(ctx, st.Cmd)
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
		r2.exit.exiting = false // subshells don't exit the parent shell
		r.exit = r2.exit
	case *syntax.CallExpr:
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
			args = slices.Replace(args, i, i+1, als.args...)
			if !als.blank {
				break
			}
			i += len(als.args)
		}
		r.lastExpandExit = exitStatus{}
		fields := r.fields(args...)
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
				// TODO: add test cases for x[i]=y and x+=y.
				if as.Array != nil {
					trace.expr(as)
				} else if as.Value != nil {
					val, err := syntax.Quote(vr.String(), syntax.LangBash)
					if err != nil { // should never happen
						panic(err)
					}
					trace.stringf("%s=%s", name, val)
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
		}

		trace.call(fields[0], fields[1:]...)
		trace.newLineFlush()

		r.call(ctx, cm.Args[0].Pos(), fields)
		for _, restore := range restores {
			r.setVar(restore.name, restore.vr)
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
			oldStdin := r.stdin
			r.stdin = prDup
			var wg sync.WaitGroup
			wg.Go(func() {
				r2.stmt(ctx, cm.X)
				r2.exit.exiting = false // subshells don't exit the parent shell
				pwDup.Close()
			})
			r.stmt(ctx, cm.Y)
			prDup.Close()
			wg.Wait()
			r.stdin = oldStdin
			// Track PIPESTATUS.
			r.pipeStatus = []string{
				strconv.Itoa(int(r2.exit.code)),
				strconv.Itoa(int(r.exit.code)),
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
			if y.Init != nil {
				r.arithm(y.Init)
			}
			for y.Cond == nil || r.arithm(y.Cond) != 0 {
				if !r.exit.ok() || r.loopStmtsBroken(ctx, cm.Do) {
					break
				}
				if y.Post != nil {
					r.arithm(y.Post)
				}
			}
		}
	case *syntax.FuncDecl:
		r.setFunc(cm.Name.Value, cm.Body)
	case *syntax.ArithmCmd:
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
		str := r.literal(cm.Word)
		noCaseMatch := false
		if opt, _ := r.bashOptByName("nocasematch"); opt != nil && *opt {
			noCaseMatch = true
		}
		for _, ci := range cm.Items {
			for _, word := range ci.Patterns {
				pat := r.pattern(word)
				matchStr := str
				if noCaseMatch {
					pat = strings.ToLower(pat)
					matchStr = strings.ToLower(matchStr)
				}
				if match(pat, matchStr) {
					r.stmts(ctx, ci.Stmts)
					return
				}
			}
		}
	case *syntax.TestClause:
		if r.bashTest(ctx, cm.X, false) == "" && r.exit.ok() {
			// to preserve exit status code 2 for regex errors, etc
			r.exit.code = 1
		}
	case *syntax.DeclClause:
		local, global := false, false
		var modes []string
		valType := ""
		declQuery := "" // "-f" or "-p" for query mode
		switch cm.Variant.Value {
		case "declare":
			// When used in a function, "declare" acts as "local"
			// unless the "-g" option is used.
			local = r.inFunc
		case "local":
			if !r.inFunc {
				r.errf("local: can only be used in a function\n")
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
	assignLoop:
		for as := range r.flattenAssigns(cm.Args) {
			fp := flagParser{remaining: []string{as.Name.Value}}
			for fp.more() {
				switch flag := fp.flag(); flag {
				case "-x", "-r":
					modes = append(modes, flag)
				case "-a", "-A", "-n", "-i":
					valType = flag
				case "-g":
					global = true
				case "-f", "-F", "-p":
					declQuery = flag
				default:
					r.errf("declare: invalid option %q\n", flag)
					r.exit.code = 2
					return
				}
				continue assignLoop
			}
			declHadNames = true
			name := as.Name.Value
			if !syntax.ValidName(name) {
				r.errf("declare: invalid name %q\n", name)
				r.exit.code = 1
				return
			}
			if declQuery == "-F" {
				// declare -F name: print just function name.
				if body := r.Funcs[name]; body != nil {
					r.outf("declare -f %s\n", name)
				} else {
					r.exit.code = 1
				}
				continue
			}
			if declQuery == "-f" {
				// declare -f name: print function definition.
				// Bash silently returns exit 1 for missing functions.
				if body := r.Funcs[name]; body != nil {
					r.outf("%s()\n", name)
					printer := syntax.NewPrinter()
					var buf bytes.Buffer
					printer.Print(&buf, body)
					r.outf("%s\n", buf.String())
				} else {
					r.exit.code = 1
				}
				continue
			}
			if declQuery == "-p" {
				// declare -p name: print variable with attributes.
				vr := r.lookupVar(name)
				if !vr.Declared() {
					r.errf("declare: %s: not found\n", name)
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
						r.outf("[%d]=%q", i, v)
					}
					r.out(")\n")
				case expand.Associative:
					r.outf("declare -%s %s=(", flags, name)
					first := true
					for k, v := range vr.Map {
						if !first {
							r.out(" ")
						}
						r.outf("[%s]=%q", k, v)
						first = false
					}
					r.out(")\n")
				default:
					r.outf("declare -%s %s=%q\n", flags, name, vr.Str)
				}
				continue
			}
			vr := r.lookupVar(name)
			if as.Naked {
				if valType == "-A" {
					vr.Kind = expand.Associative
				} else {
					vr.Kind = expand.KeepValue
				}
			} else {
				name, vr = r.assignVal(name, vr, as, valType)
			}
			if global {
				vr.Local = false
			} else if local {
				vr.Local = true
			}
			for _, mode := range modes {
				switch mode {
				case "-x":
					vr.Exported = true
				case "-r":
					vr.ReadOnly = true
				}
			}
			r.setVar(name, vr)
		}
		// Handle declare -F/-f with no arguments: list all functions.
		if !declHadNames && declQuery == "-F" {
			for name := range r.Funcs {
				r.outf("declare -f %s\n", name)
			}
		} else if !declHadNames && declQuery == "-f" {
			for name, body := range r.Funcs {
				r.outf("%s()\n", name)
				printer := syntax.NewPrinter()
				var buf bytes.Buffer
				printer.Print(&buf, body)
				r.outf("%s\n", buf.String())
			}
		}
	case *syntax.TimeClause:
		start := time.Now()
		if cm.Stmt != nil {
			r.stmt(ctx, cm.Stmt)
		}
		format := "%s\t%s\n"
		if cm.PosixFormat {
			format = "%s %s\n"
		} else {
			r.outf("\n")
		}
		real := time.Since(start)
		r.outf(format, "real", elapsedString(real, cm.PosixFormat))
		// TODO: can we do these?
		r.outf(format, "user", elapsedString(0, cm.PosixFormat))
		r.outf(format, "sys", elapsedString(0, cm.PosixFormat))
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

func (r *Runner) trapCallback(ctx context.Context, callback, name string) {
	if callback == "" {
		return // nothing to do
	}
	if r.handlingTrap {
		return // don't recurse, as that could lead to cycles
	}
	r.handlingTrap = true

	p := syntax.NewParser()
	// TODO: do this parsing when "trap" is called?
	file, err := p.Parse(strings.NewReader(callback), name+" trap")
	if err != nil {
		r.errf(name+"trap: %v\n", err)
		// ignore errors in the callback
		return
	}
	oldExit := r.exit
	r.stmts(ctx, file.Stmts)
	r.exit = oldExit // traps on EXIT or ERR should not modify the result

	r.handlingTrap = false
}

func (r *Runner) flattenAssigns(args []*syntax.Assign) iter.Seq[*syntax.Assign] {
	return func(yield func(*syntax.Assign) bool) {
		for _, as := range args {
			// Convert "declare $x" into "declare value".
			// Don't use syntax.Parser here, as we only want the basic
			// splitting by '='.
			if as.Name != nil {
				if !yield(as) {
					return
				}
				continue
			}
			for _, field := range r.fields(as.Value) {
				as := &syntax.Assign{}
				name, val, ok := strings.Cut(field, "=")
				as.Name = &syntax.Lit{Value: name}
				if !ok {
					as.Naked = true
				} else {
					as.Value = &syntax.Word{Parts: []syntax.WordPart{
						&syntax.Lit{Value: val},
					}}
				}
				if !yield(as) {
					return
				}
			}
		}
	}
}

func match(pat, name string) bool {
	matcher, err := internal.ExtendedPatternMatcher(pat, pattern.EntireString|pattern.ExtendedOperators)
	_ = err // TODO: report these errors
	return matcher != nil && matcher(name)
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
		r.stdin = pr
		return pr, nil
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
			r.setVarString(namedFDVar, strconv.Itoa(targetFd))
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
			if err != nil || n < 0 {
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
			r.setVarString(namedFDVar, strconv.Itoa(targetFd))
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
			if err != nil || n < 0 {
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
			r.setVarString(namedFDVar, strconv.Itoa(targetFd))
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
			line, err := r.readLine(ctx, true)
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
	r.trapCallback(ctx, r.trapCallbacks["DEBUG"], "debug")
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
	if body := r.Funcs[name]; body != nil {
		// stack them to support nested func calls
		oldParams := r.Params
		r.Params = args[1:]
		oldInFunc := r.inFunc
		r.inFunc = true

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
			r.errf("%v\n", err)
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
