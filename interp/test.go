// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package interp

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/term"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// arithErrMsg returns the bash 5.3 wording for an arithmetic-
// syntax error encountered while evaluating the [[ ]] operand `s`.
// The returned text is shaped like
//
//	arithmetic syntax error: operand expected (error token is "X")
//
// where `X` is the trailing fragment that bash would highlight.
// Best-effort: when we can't determine a token, fall back to the
// full operand.
func arithErrMsg(err error, s string) string {
	tok := s
	// Strip a known prefix like "1:N: " from our parser error.
	msg := err.Error()
	if idx := strings.LastIndex(msg, ": "); idx >= 0 {
		core := msg[idx+2:]
		if core != "" {
			// Heuristic: take the last char of the operand as the
			// "error token" when the parser complained about a
			// trailing operator (`4+`, `4*`, …).
			last := s[len(s)-1]
			switch last {
			case '+', '-', '*', '/', '%', '<', '>', '&', '|', '^', '=', '!':
				tok = string(last)
			}
		}
	}
	return "arithmetic syntax error: operand expected (error token is \"" + tok + "\")"
}

// arithFromString parses s as a bash arithmetic expression and
// returns its int64 value. Empty input is treated as 0 (bash behaves
// the same way). Unset / non-numeric tokens that don't parse as a
// valid expression return a non-nil error so callers can report the
// bash 5.3 "integer expected" / "syntax error" diagnostics.
func (r *Runner) arithFromString(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	// Wrap as `(( s ))` so the parser produces a single ArithmCmd.
	src := "((" + s + "))"
	file, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	if err != nil {
		return 0, err
	}
	if len(file.Stmts) != 1 {
		return 0, fmt.Errorf("not a valid arithmetic expression")
	}
	ac, ok := file.Stmts[0].Cmd.(*syntax.ArithmCmd)
	if !ok || ac.X == nil {
		return 0, fmt.Errorf("not a valid arithmetic expression")
	}
	n, err := expand.Arithm(r.ecfg, ac.X)
	if err != nil {
		return 0, err
	}
	return int64(n), nil
}

// non-empty string is true, empty string is false
func (r *Runner) bashTest(ctx context.Context, expr syntax.TestExpr, classic bool) string {
	switch x := expr.(type) {
	case *syntax.Word:
		if classic {
			// In the classic "test" mode, we already expanded and
			// split the list of words, so don't redo that work.
			return r.document(x)
		}
		return r.literal(x)
	case *syntax.ParenTest:
		return r.bashTest(ctx, x.X, classic)
	case *syntax.BinaryTest:
		// && and || in [[ ]] short-circuit: the rhs must not be evaluated
		// (and so its expansions like ${H*} not run) if the lhs settles
		// the result. Eager evaluation here used to run `${H*}` even when
		// `-n $TDIR || $HOME -ef ${H*}` was already true on the lhs.
		switch x.Op {
		case syntax.AndTest:
			if r.bashTest(ctx, x.X, classic) == "" {
				return ""
			}
			return r.bashTest(ctx, x.Y, classic)
		case syntax.OrTest:
			if lhs := r.bashTest(ctx, x.X, classic); lhs != "" {
				return lhs
			}
			return r.bashTest(ctx, x.Y, classic)
		}
		switch x.Op {
		case syntax.TsMatchShort, syntax.TsMatch, syntax.TsNoMatch:
			str := r.literal(x.X.(*syntax.Word))
			yw := x.Y.(*syntax.Word)
			if classic { // test, [
				lit := r.literal(yw)
				if (str == lit) == (x.Op != syntax.TsNoMatch) {
					return "1"
				}
			} else { // [[
				pat := r.pattern(yw)
				matchStr := str
				// nocasematch: case-insensitive pattern matching
				if opt, _ := r.bashOptByName("nocasematch"); opt != nil && *opt {
					pat = strings.ToLower(pat)
					matchStr = strings.ToLower(matchStr)
				}
				if match(pat, matchStr) == (x.Op != syntax.TsNoMatch) {
					return "1"
				}
			}
			return ""
		}
		yStr := r.bashTest(ctx, x.Y, classic)
		// `[[ X =~ Y ]]` treats quoted segments of Y as literal —
		// re-expand Y through RegexPattern when it's a Word so
		// `[[ a =~ '[[:alpha:]]' ]]` doesn't match (the brackets
		// become literal characters).
		if !classic && x.Op == syntax.TsReMatch {
			if yw, ok := x.Y.(*syntax.Word); ok {
				if s, err := expand.RegexPattern(r.ecfg, yw); err == nil {
					yStr = s
				}
			}
		}
		if r.binTest(ctx, x.Op, r.bashTest(ctx, x.X, classic), yStr, classic) {
			return "1"
		}
		return ""
	case *syntax.UnaryTest:
		if r.unTest(ctx, x.Op, r.bashTest(ctx, x.X, classic)) {
			return "1"
		}
		return ""
	}
	return ""
}

func (r *Runner) binTest(ctx context.Context, op syntax.BinTestOperator, x, y string, classic bool) bool {
	switch op {
	case syntax.TsReMatch:
		pat := y
		if opt, _ := r.bashOptByName("nocasematch"); opt != nil && *opt {
			pat = "(?i)" + pat
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			r.errf("[[: %s\n", err)
			r.exit.code = 2
			return false
		}
		m := re.FindStringSubmatch(x)
		if m == nil {
			r.setVar("BASH_REMATCH", expand.Variable{
				Set:  true,
				Kind: expand.Indexed,
			})
			return false
		}
		vr := expand.Variable{
			Set:  true,
			Kind: expand.Indexed,
			List: m,
		}
		r.setVar("BASH_REMATCH", vr)
		return true
	case syntax.TsNewer:
		// bash: `f1 -nt f2` is true if f1 is newer than f2, OR if
		// f1 exists and f2 does not (a present file is "newer than"
		// a missing one). False if both are missing or only f2 exists.
		info1, err1 := r.stat(ctx, x)
		info2, err2 := r.stat(ctx, y)
		switch {
		case err1 == nil && err2 == nil:
			return info1.ModTime().After(info2.ModTime())
		case err1 == nil && err2 != nil:
			return true
		default:
			return false
		}
	case syntax.TsOlder:
		// bash: `f1 -ot f2` is true if f1 is older than f2, OR if
		// f1 is missing and f2 exists. False if both are missing
		// or only f1 exists.
		info1, err1 := r.stat(ctx, x)
		info2, err2 := r.stat(ctx, y)
		switch {
		case err1 == nil && err2 == nil:
			return info1.ModTime().Before(info2.ModTime())
		case err1 != nil && err2 == nil:
			return true
		default:
			return false
		}
	case syntax.TsDevIno:
		info1, err1 := r.stat(ctx, x)
		info2, err2 := r.stat(ctx, y)
		if err1 != nil || err2 != nil {
			return false
		}
		return os.SameFile(info1, info2)
	case syntax.TsEql, syntax.TsNeq, syntax.TsLeq, syntax.TsGeq, syntax.TsLss, syntax.TsGtr:
		// Classic `test` / `[`: validate operand is an integer
		// and emit bash's "integer expected" diagnostic via the
		// runner-scoped testIntErr field. `[[ ]]` does
		// arithmetic evaluation instead — fall through to the
		// best-effort atoi where unset/non-numeric is 0.
		var xn, yn int64
		if classic {
			var err error
			xn, err = strconv.ParseInt(strings.TrimSpace(x), 10, 64)
			if err != nil {
				r.testIntErr = x
				return false
			}
			yn, err = strconv.ParseInt(strings.TrimSpace(y), 10, 64)
			if err != nil {
				r.testIntErr = y
				return false
			}
		} else {
			// `[[ ]]` evaluates the operands as full arithmetic
			// expressions: `[[ 7 -eq 4+3 ]]` is true. Empty input
			// is treated as 0 (matches bash). Genuine arithmetic
			// syntax errors set testArithErr so TestClause prints
			// bash's "arithmetic syntax error" wording (exit 1)
			// rather than "integer expected" (exit 2).
			var xerr, yerr error
			xn, xerr = r.arithFromString(x)
			if xerr != nil {
				r.testIntErr = x
				r.testArithErr = arithErrMsg(xerr, x)
				return false
			}
			yn, yerr = r.arithFromString(y)
			if yerr != nil {
				r.testIntErr = y
				r.testArithErr = arithErrMsg(yerr, y)
				return false
			}
		}
		switch op {
		case syntax.TsEql:
			return xn == yn
		case syntax.TsNeq:
			return xn != yn
		case syntax.TsLeq:
			return xn <= yn
		case syntax.TsGeq:
			return xn >= yn
		case syntax.TsLss:
			return xn < yn
		case syntax.TsGtr:
			return xn > yn
		}
		return false
	case syntax.AndTest:
		return x != "" && y != ""
	case syntax.OrTest:
		return x != "" || y != ""
	case syntax.TsBefore:
		return x < y
	case syntax.TsAfter:
		return x > y
	default:
		// Should only happen if we forgot a case above.
		panic(fmt.Sprintf("unexpected binary test operator: %q", op))
	}
}

func (r *Runner) statMode(ctx context.Context, name string, mode os.FileMode) bool {
	info, err := r.stat(ctx, name)
	return err == nil && info.Mode()&mode != 0
}

func (r *Runner) testFdPath(name string, mode uint32) (bool, bool) {
	var fd int
	switch name {
	case "/dev/stdin":
		fd = 0
	case "/dev/stdout":
		fd = 1
	case "/dev/stderr":
		fd = 2
	default:
		const prefix = "/dev/fd/"
		if !strings.HasPrefix(name, prefix) {
			return false, false
		}
		n, err := strconv.Atoi(name[len(prefix):])
		if err != nil {
			return true, false
		}
		fd = n
	}
	var f any
	switch fd {
	case 0:
		f = r.stdin
	case 1:
		f = r.stdout
	case 2:
		f = r.stderr
	default:
		f = r.fdTable[fd]
	}
	if f == nil {
		return true, false
	}
	// Bash treats these pseudo-paths as probes of the currently open
	// descriptor. For descriptors we own, existence is the best
	// signal available without performing a destructive read/write.
	return true, mode&(access_R_OK|access_W_OK) != 0
}

// These are copied from x/sys/unix as we can't import it here.
const (
	access_R_OK = 0x4
	access_W_OK = 0x2
	access_X_OK = 0x1
)

func (r *Runner) unTest(ctx context.Context, op syntax.UnTestOperator, x string) bool {
	switch op {
	case syntax.TsExists:
		_, err := r.stat(ctx, x)
		return err == nil
	case syntax.TsRegFile:
		info, err := r.stat(ctx, x)
		return err == nil && info.Mode().IsRegular()
	case syntax.TsDirect:
		return r.statMode(ctx, x, os.ModeDir)
	case syntax.TsCharSp:
		return r.statMode(ctx, x, os.ModeCharDevice)
	case syntax.TsBlckSp:
		info, err := r.stat(ctx, x)
		return err == nil && info.Mode()&os.ModeDevice != 0 &&
			info.Mode()&os.ModeCharDevice == 0
	case syntax.TsNmPipe:
		return r.statMode(ctx, x, os.ModeNamedPipe)
	case syntax.TsSocket:
		return r.statMode(ctx, x, os.ModeSocket)
	case syntax.TsSmbLink:
		info, err := r.lstat(ctx, x)
		return err == nil && info.Mode()&os.ModeSymlink != 0
	case syntax.TsSticky:
		return r.statMode(ctx, x, os.ModeSticky)
	case syntax.TsUIDSet:
		return r.statMode(ctx, x, os.ModeSetuid)
	case syntax.TsGIDSet:
		return r.statMode(ctx, x, os.ModeSetgid)
	case syntax.TsModif:
		// `-N FILE` — true iff the file has been modified
		// since it was last accessed (mtime > atime).
		info, err := r.stat(ctx, r.absPath(x))
		if err != nil {
			return false
		}
		return modifiedSinceAccessed(info)
	case syntax.TsRead:
		if ok, result := r.testFdPath(x, access_R_OK); ok {
			return result
		}
		return r.access(ctx, r.absPath(x), access_R_OK) == nil
	case syntax.TsWrite:
		if ok, result := r.testFdPath(x, access_W_OK); ok {
			return result
		}
		return r.access(ctx, r.absPath(x), access_W_OK) == nil
	case syntax.TsExec:
		return r.access(ctx, r.absPath(x), access_X_OK) == nil
	case syntax.TsNoEmpty:
		info, err := r.stat(ctx, x)
		return err == nil && info.Size() > 0
	case syntax.TsFdTerm:
		// bash 5.3: `-t N` requires N to be an integer; emit
		// "<X>: integer expected" with exit 2 otherwise.
		fd, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		if err != nil {
			r.testIntErr = x
			return false
		}
		var f any
		switch fd {
		case 0:
			f = r.stdin
		case 1:
			f = r.stdout
		case 2:
			f = r.stderr
		}
		if f, ok := f.(interface{ Fd() uintptr }); ok {
			// Support [os.File.Fd] methods such as the one on [*os.File].
			return term.IsTerminal(int(f.Fd()))
		}
		// TODO: allow term.IsTerminal here too if running in the
		// "single process" mode.
		return false
	case syntax.TsEmpStr:
		return x == ""
	case syntax.TsNempStr:
		return x != ""
	case syntax.TsOptSet:
		if opt := r.posixOptByName(x); opt != nil {
			return *opt
		}
		return false
	case syntax.TsVarSet:
		return r.lookupVar(x).IsSet()
	case syntax.TsRefVar:
		return r.lookupVar(x).Kind == expand.NameRef
	case syntax.TsNot:
		return x == ""
	case syntax.TsUsrOwn, syntax.TsGrpOwn:
		return r.unTestOwnOrGrp(ctx, op, x)
	default:
		// Should only happen if we forgot a case above.
		panic(fmt.Sprintf("unexpected unary test op: %v", op))
	}
}
