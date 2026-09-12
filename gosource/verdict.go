package gosource

import (
	"bytes"
	"fmt"
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
func gcSyntaxVerdict(name string, src []byte, checkerBranchErrors bool) ErrorList {
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
	gcsyntax.Parse(gcsyntax.NewFileBase(name), bytes.NewReader(src), errh, nil, mode)
	return out
}

// syntaxVerdict runs gcSyntaxVerdict over every source. A non-empty result
// is the complete diagnostic set for the load: gc runs no type checker after
// a syntax error, so neither go/parser nor go/types output is ever appended
// to it. An empty result means every file passed gc's parser and loading
// proceeds with go/parser + go/types exactly as before.
func syntaxVerdict(sources []Source, checkerBranchErrors bool) ErrorList {
	var out ErrorList
	for _, s := range sources {
		out = append(out, gcSyntaxVerdict(s.Name, s.Data, checkerBranchErrors)...)
	}
	return out
}
