// Copyright (c) 2026, the bash++ authors
// See LICENSE for licensing information

package lower

import (
	"fmt"
	"go/constant"
	"go/token"
	"strconv"

	"mvdan.cc/sh/v3/syntax"
)

// This file is the emitter's value-projection helper: it decides what shell
// text a typed binding produces when it is used as a word. It holds no global
// state, resolves nothing by inspecting a value's runtime shape, and never
// executes source at compile time. It also never renders a value to text and
// reparses that text to pick a language.

// projectionKind records how a binding's root must be rendered. It is a
// compiler fact carried from the binding site, not something recovered later
// from the value. That distinction is the whole point: an imported package
// call returning a string still has an object root and therefore projects
// JSON-quoted, while a field selected out of a rich value is a plain scalar
// even though its parent is JSON.
type projectionKind int

const (
	// projectScalar renders plain interpreter scalar text. Used for native
	// scalar bindings and for a scalar reached by selection or indexing.
	projectScalar projectionKind = iota
	// projectObject renders the shell's object coercion, which is JSON. Used
	// for rich native roots and for every imported-call result.
	projectObject
)

// projection is one binding's retained provenance.
type projection struct {
	kind projectionKind
	// text is the exact shell spelling when the binding is a constant whose
	// source literal was retained. hasText distinguishes a known empty string
	// from an unknown value.
	text    string
	hasText bool
}

// scalarProjection marks a native scalar root: a plain value, or a scalar
// reached through a selector or index out of a rich value. Its projection is
// the interpreter's plain text, never the enclosing root's JSON.
func scalarProjection() projection { return projection{kind: projectScalar} }

// objectProjection marks a rich root. The caller passes this for native
// struct, map, slice and pointer roots, and for the result of every
// imported-package call regardless of that result's Go type. A nil rich root
// projects as "null"; a rich value projects as its deterministic JSON.
func objectProjection() projection { return projection{kind: projectObject} }

// projectionFromLiteral retains a constant literal's exact spelling, using the
// same mechanism the interpreter uses on this path — go/constant over the
// source text, with no float64 round trip anywhere. That is what keeps an
// untyped 1.5 projecting as the exact rational 3/2 and 0x1.8p+1 as 3.
func projectionFromLiteral(lit *syntax.BashPPBasicLit) (projection, error) {
	if lit == nil || lit.Value == nil {
		return projection{}, projectionError{CodeExpr, "missing constant literal"}
	}
	kind, ok := map[string]token.Token{
		"INT":    token.INT,
		"FLOAT":  token.FLOAT,
		"CHAR":   token.CHAR,
		"STRING": token.STRING,
	}[lit.Kind]
	if !ok {
		return projection{}, projectionError{CodeExpr, fmt.Sprintf("unsupported constant kind %q", lit.Kind)}
	}
	v := constant.MakeFromLiteral(lit.Value.Value, kind, 0)
	if v.Kind() == constant.Unknown {
		return projection{}, projectionError{CodeExpr, fmt.Sprintf("invalid literal %s", lit.Value.Value)}
	}
	return projection{kind: projectScalar, text: constantShellText(v), hasText: true}, nil
}

// projectionFromBool retains the two constant identifiers the scalar reader
// treats as literals.
func projectionFromBool(b bool) projection {
	return projection{kind: projectScalar, text: strconv.FormatBool(b), hasText: true}
}

// projectionFromFloat64 always fails. It exists so the refusal is a named,
// tested boundary rather than an omission somebody later "fixes" by reaching
// for constant.MakeFloat64. A float64 cannot recover the exact rational the
// interpreter prints: 1.5 is 3/2, but 1.1 is not 11/10, and pretending
// otherwise would silently diverge on the second value while looking correct
// on the first. Retain the literal and use projectionFromLiteral instead.
func projectionFromFloat64(float64) (projection, error) {
	return projection{}, projectionError{
		CodeExpr,
		"floating scalar has no retained constant provenance; project it from its source literal, not a float64",
	}
}

// constantShellText mirrors the interpreter's scalar rendering: strings render
// as their contents, booleans as true/false, and every numeric constant as its
// exact spelling rather than a rounded decimal.
func constantShellText(v constant.Value) string {
	switch v.Kind() {
	case constant.String:
		return constant.StringVal(v)
	case constant.Bool:
		return strconv.FormatBool(constant.BoolVal(v))
	default:
		return v.ExactString()
	}
}

// projector is the emitter's projection scope stack. Its zero value is ready
// to use, so wiring it into the emitter costs one struct field and no change
// to the emitter's constructor.
type projector struct {
	scopes []map[string]projection
}

// projectionPush enters a lexical scope. It mirrors the emitter's own push.
func (p *projector) projectionPush() {
	p.scopes = append(p.scopes, map[string]projection{})
}

// projectionPop leaves a lexical scope. Popping the root scope is a no-op so a
// mismatched pair cannot panic mid-compile.
func (p *projector) projectionPop() {
	if len(p.scopes) > 0 {
		p.scopes = p.scopes[:len(p.scopes)-1]
	}
}

// projectionBind records a binding's provenance in the innermost scope.
func (p *projector) projectionBind(name string, pr projection) {
	if name == "" || name == "_" {
		return
	}
	if len(p.scopes) == 0 {
		p.projectionPush()
	}
	p.scopes[len(p.scopes)-1][name] = pr
}

// projectionLookup resolves a name innermost-first.
func (p *projector) projectionLookup(name string) (projection, bool) {
	for i := len(p.scopes) - 1; i >= 0; i-- {
		if pr, ok := p.scopes[i][name]; ok {
			return pr, true
		}
	}
	return projection{}, false
}

// projectValue returns Go source of type string that renders the binding name
// as shell text. expression is the Go expression the emitter already produced
// for that binding.
//
// A binding with retained constant provenance projects to a plain Go string
// literal and emits no runtime call at all, which is how the exact rational
// spelling survives into the generated program. Everything else defers to the
// runtime with the kind chosen here, so the runtime never has to guess.
//
// An unknown name is a positioned error rather than a shape guess.
func (p *projector) projectValue(name, expression string) (string, error) {
	pr, ok := p.projectionLookup(name)
	if !ok {
		return "", projectionError{CodeUndefined, fmt.Sprintf("undefined projection for %s", name)}
	}
	if pr.hasText {
		return strconv.Quote(pr.text), nil
	}
	if expression == "" {
		return "", projectionError{CodeExpr, fmt.Sprintf("missing expression for %s", name)}
	}
	kind := "shellrt.KindScalar"
	if pr.kind == projectObject {
		kind = "shellrt.KindObject"
	}
	return fmt.Sprintf("shellrt.Project(%s, %s)", expression, kind), nil
}

// projectionError carries a diagnostic code out of the helper without needing
// a syntax node; the emitter attaches the position it already has.
type projectionError struct{ code, msg string }

func (e projectionError) Error() string { return e.msg }

// projectionDiagnostic converts a helper error into the package's positioned
// diagnostic. Errors from other sources keep CodeExpr.
func projectionDiagnostic(n syntax.Node, err error) error {
	if err == nil {
		return nil
	}
	code := CodeExpr
	if pe, ok := err.(projectionError); ok {
		code = pe.code
	}
	d := Diagnostic{Code: code, Msg: err.Error()}
	if n != nil {
		d.Pos = n.Pos()
	}
	return d
}
