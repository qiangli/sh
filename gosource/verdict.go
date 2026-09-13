package gosource

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/scanner"
	"go/token"
	"go/types"
	"sort"
	"strings"

	gcsyntax "mvdan.cc/sh/v3/gosource/internal/gcsyntax"
)

// gcError is one diagnostic from the vendored gc parser, rendered exactly as
// gc prints it on stderr: the relative position (so //line directives are
// honored, and the column is omitted when a line directive gave none),
// followed by gc's message verbatim, including its "syntax error: " prefix.
type gcError struct {
	pos gcsyntax.Pos
	msg string
}

func (e gcError) Error() string {
	name, line, col := e.pos.RelFilename(), e.pos.RelLine(), e.pos.RelCol()
	if line == 0 {
		return fmt.Sprintf("%s: %s", name, e.msg)
	}
	if col == 0 {
		return fmt.Sprintf("%s:%d: %s", name, line, e.msg)
	}
	return fmt.Sprintf("%s:%d:%d: %s", name, line, col, e.msg)
}

// gcSyntaxVerdict parses src with gc's own parser (the vendored
// cmd/compile/internal/syntax) and returns its diagnostics. Branch checking is
// enabled for gc behavior unless checkerBranchErrors leaves it to go/types.
// Diagnostics are filtered the way gc's base.ErrorfAt filters them before they
// reach stderr (cmd/compile/internal/base/print.go, Go 1.27.0):
//
//	if strings.HasPrefix(msg, "syntax error") {
//		// only one syntax error per line, no matter what error
//		if sameline(lasterror.syntax, pos) { return }
//		lasterror.syntax = pos
//	} else {
//		// only one of multiple equal non-syntax errors per line
//		if sameline(lasterror.other, pos) && msg == lasterror.msg { return }
//		lasterror.other = pos
//		lasterror.msg = msg
//	}
//
// where sameline compares position base and line. Errors are returned in the
// order gc emits them (source order). An empty result is gc accepting the
// file at syntax stage.
func gcSyntaxVerdict(name string, src []byte, checkerBranchErrors bool) (ErrorList, *gcsyntax.File) {
	var out ErrorList
	var lastSyntax, lastOther gcsyntax.Pos
	lastMsg := ""
	sameline := func(a, b gcsyntax.Pos) bool {
		return a.Base() == b.Base() && a.Line() == b.Line()
	}
	errh := func(err error) {
		e, ok := err.(gcsyntax.Error)
		if !ok {
			out = append(out, err)
			return
		}
		if strings.HasPrefix(e.Msg, "syntax error") {
			if sameline(lastSyntax, e.Pos) {
				return
			}
			lastSyntax = e.Pos
		} else {
			if sameline(lastOther, e.Pos) && e.Msg == lastMsg {
				return
			}
			lastOther, lastMsg = e.Pos, e.Msg
		}
		out = append(out, gcError{pos: e.Pos, msg: e.Msg})
	}
	mode := gcsyntax.CheckBranches
	if checkerBranchErrors {
		mode = 0
	}
	file, _ := gcsyntax.Parse(gcsyntax.NewFileBase(name), bytes.NewReader(src), errh, nil, mode)
	return out, file
}

// syntaxVerdict runs gcSyntaxVerdict over every source. A non-empty result is
// gc's complete syntax-stage diagnostic set; Load decides whether policy stops
// there or continues with go/parser recovery and go/types. An empty result
// means every file passed gc's parser. The trees are gc's own, index-aligned
// with sources (nil where gc produced none); mirrorGCTree reads them.
func syntaxVerdict(sources []Source, checkerBranchErrors bool) (ErrorList, []*gcsyntax.File) {
	var out ErrorList
	files := make([]*gcsyntax.File, len(sources))
	for i, s := range sources {
		var errs ErrorList
		errs, files[i] = gcSyntaxVerdict(s.Name, s.Data, checkerBranchErrors)
		out = append(out, errs...)
	}
	return out, files
}

// checksAfterSyntaxVerdict reports whether gc type-checks the files after a
// non-empty syntax verdict. gc counts only diagnostics that begin with
// "syntax error" (base.SyntaxErrors, cmd/compile/internal/base/print.go)
// and exits before the checker when there is one
// (cmd/compile/internal/noder/irgen.go checkFiles); every other parser or
// scanner diagnostic — a missing 3-index slice bound, a parenthesized go/defer
// expression, an invalid NUL or UTF-8 byte, a malformed literal — is
// reported and the checker still runs over the tree and reports its own.
func checksAfterSyntaxVerdict(verdict ErrorList) bool {
	for _, err := range verdict {
		if e, ok := err.(gcError); ok && strings.HasPrefix(e.msg, "syntax error") {
			return false
		}
	}
	return true
}

// checkerDiagnostics collects one go/types Check's diagnostics under gc's
// division of labour. gc's parser checks branches (syntax.CheckBranches) and
// types2 is configured with IgnoreBranchErrors, so a label or branch error is
// reported once, by the parser. go/types has no such switch; when the gc
// verdict carried the branch check, the checker's branch diagnostics are
// dropped here. They are recognised structurally, not by wording: go/types
// anchors every one of them (labels.go, stmt.go BranchStmt) at a labeled
// statement's label, a branch statement's keyword or a branch statement's
// label, and nothing else the checker reports is anchored there.
//
// With gcStderr set the collector also applies gc's stderr filter to the
// checker's diagnostics, as base.ErrorfAt does to every non-syntax error:
// only one of multiple equal messages per line is printed (the position base
// and line are compared, so //line directives are honored).
//
// go/types reports the continuation lines of a message ("\tother declaration
// of x") as separate errors that follow their primary; they stay with it:
// dropped with it, and never compared or sorted on their own.
type checkerDiagnostics struct {
	list        ErrorList
	dropped     int
	anchors     map[token.Pos]bool
	gcStderr    bool
	fset        *token.FileSet
	lastLine    token.Position
	lastMsg     string
	lastDropped bool
}

func newCheckerDiagnostics(fset *token.FileSet, files []*ast.File, gcBranchVerdict, gcStderr bool) *checkerDiagnostics {
	d := &checkerDiagnostics{gcStderr: gcStderr, fset: fset}
	if !gcBranchVerdict {
		return d
	}
	d.anchors = map[token.Pos]bool{}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.LabeledStmt:
				d.anchors[n.Label.Pos()] = true
			case *ast.BranchStmt:
				d.anchors[n.Pos()] = true
				if n.Label != nil {
					d.anchors[n.Label.Pos()] = true
				}
			}
			return true
		})
	}
	return d
}

// report is the types.Config.Error handler.
func (d *checkerDiagnostics) report(err error) {
	e, ok := err.(types.Error)
	if ok && isContinuation(e) {
		if d.lastDropped {
			d.dropped++
			return
		}
		d.list = append(d.list, err)
		return
	}
	d.lastDropped = false
	if ok && d.anchors[e.Pos] {
		d.dropped++
		d.lastDropped = true
		return
	}
	if ok && d.gcStderr {
		pos := d.fset.Position(e.Pos)
		if pos.Filename == d.lastLine.Filename && pos.Line == d.lastLine.Line && e.Msg == d.lastMsg {
			d.dropped++
			d.lastDropped = true
			return
		}
		d.lastLine, d.lastMsg = pos, e.Msg
	}
	d.list = append(d.list, err)
}

// isContinuation reports whether a go/types error is a continuation line of
// the error reported just before it (go/types errors.go: secondary lines are
// indented with a tab).
func isContinuation(e types.Error) bool {
	return strings.HasPrefix(e.Msg, "\t")
}

// result returns the collected diagnostics. Check's returned first error is
// already reported through Error and is not duplicated; it is added only when
// nothing at all reached the handler.
func (d *checkerDiagnostics) result(first error) ErrorList {
	if first != nil && len(d.list) == 0 && d.dropped == 0 {
		return appendDiagnostics(nil, first)
	}
	return d.list
}

// sortGCStderr orders diagnostics as gc prints them: base.FlushErrors sorts
// stably by position before writing stderr, so the parser's and the checker's
// rows of one file interleave in source order. Files keep their relative
// order (sources, the compiler's operand order); a diagnostic without a
// position sorts first, as an unknown src.XPos does in gc.
func sortGCStderr(fset *token.FileSet, sources []Source, diagnostics ErrorList) ErrorList {
	index := map[string]int{}
	for i, s := range sources {
		index[s.Name] = i + 1
	}
	type key struct{ file, line, col int }
	keyOf := func(err error) key {
		switch e := err.(type) {
		case gcError:
			return key{index[e.pos.FileBase().Filename()], int(e.pos.Line()), int(e.pos.Col())}
		case types.Error:
			p := fset.PositionFor(e.Pos, false)
			return key{index[p.Filename], p.Line, p.Column}
		case *scanner.Error:
			return key{index[e.Pos.Filename], e.Pos.Line, e.Pos.Column}
		case scanner.Error:
			return key{index[e.Pos.Filename], e.Pos.Line, e.Pos.Column}
		}
		return key{}
	}
	// A message and its continuation lines sort as one unit.
	type group struct {
		key  key
		list ErrorList
	}
	var groups []group
	for _, d := range diagnostics {
		if e, ok := d.(types.Error); ok && isContinuation(e) && len(groups) > 0 {
			groups[len(groups)-1].list = append(groups[len(groups)-1].list, d)
			continue
		}
		groups = append(groups, group{keyOf(d), ErrorList{d}})
	}
	sort.SliceStable(groups, func(a, b int) bool {
		ka, kb := groups[a].key, groups[b].key
		if ka.file != kb.file {
			return ka.file < kb.file
		}
		if ka.line != kb.line {
			return ka.line < kb.line
		}
		return ka.col < kb.col
	})
	out := make(ErrorList, 0, len(diagnostics))
	for _, g := range groups {
		out = append(out, g.list...)
	}
	return out
}
