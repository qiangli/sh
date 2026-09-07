// Package lower emits ordinary Go from the positioned Bash++ syntax tree.
package lower

import (
	"fmt"
	"mvdan.cc/sh/v3/syntax"
	"strings"
)

const DefaultRuntime = "mvdan.cc/sh/v3/lower/shellrt"
const (
	CodeUnsupported = "LOWER-EUNSUPPORTED"
	CodeType        = "LOWER-ETYPE"
	CodeUndefined   = "LOWER-EUNDEFINED"
	CodeExpr        = "LOWER-EEXPR"
	CodeResult      = "LOWER-ERESULT"
	CodeBridge      = "LOWER-EBRIDGE"
)

type Options struct {
	Package, Runtime, Origin string
	// Entry optionally names an exported entry accepting runtime SessionOptions.
	// Empty preserves the hygienic private entry and runtime-free native units.
	Entry string
}
type Result struct {
	Source   []byte
	Package  string
	Entry    string // emitted callable entry name, or empty for a runtime-free unit
	Imports  []string
	Origin   string
	Mappings []Mapping
}
type Mapping struct {
	GoLine, GoCol int
	Pos           syntax.Pos
	Node          string
}

func (r *Result) LookupLine(line int) (Mapping, bool) {
	for _, m := range r.Mappings {
		if m.GoLine == line {
			return m, true
		}
	}
	return Mapping{}, false
}

type Diagnostic struct {
	Code, Msg, Node string
	Pos             syntax.Pos
}

func (d Diagnostic) Error() string {
	return fmt.Sprintf("%d:%d: %s: %s", d.Pos.Line(), d.Pos.Col(), d.Code, d.Msg)
}

type ErrorList []Diagnostic

func (e ErrorList) Error() string {
	a := make([]string, len(e))
	for i, d := range e {
		a[i] = d.Error()
	}
	return strings.Join(a, "\n")
}
