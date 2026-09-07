// Copyright (c) 2026, the bash++ authors
// See LICENSE for licensing information

package lower

import (
	"go/parser"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

// These are helper-level checks against recorded interpreter output. They do
// not compile or run a whole profile case and make no profile-parity claim.

func literalOf(t *testing.T, kind, text string) *syntax.BashPPBasicLit {
	t.Helper()
	return &syntax.BashPPBasicLit{Kind: kind, Value: &syntax.Lit{Value: text}}
}

// tests/lowering/profile-additional/literal-float.bpp records stdout "3/2 3"
// for `a := 1.5; b := 0x1.8p+1; println(a, b)`. The exact rational spelling is
// the interpreter's, and is preserved here without any float64 round trip.
func TestProjectionRetainsExactFloatSpelling(t *testing.T) {
	for text, want := range map[string]string{
		"1.5":      "3/2",
		"0x1.8p+1": "3",
		"1.1":      "11/10",
		"2.5e0":    "5/2",
	} {
		pr, err := projectionFromLiteral(literalOf(t, "FLOAT", text))
		if err != nil {
			t.Fatalf("%s: %v", text, err)
		}
		if !pr.hasText || pr.text != want {
			t.Fatalf("%s: got %q want %q", text, pr.text, want)
		}
	}
}

// literal-integer.bpp records "5 15 31 1000"; literal-rune.bpp records "233 10".
func TestProjectionRetainsIntegerAndRuneSpelling(t *testing.T) {
	ints := []struct{ text, want string }{
		{"0b101", "5"}, {"0o17", "15"}, {"0x1f", "31"}, {"1_000", "1000"},
	}
	for _, c := range ints {
		pr, err := projectionFromLiteral(literalOf(t, "INT", c.text))
		if err != nil {
			t.Fatalf("%s: %v", c.text, err)
		}
		if pr.text != c.want {
			t.Fatalf("%s: got %q want %q", c.text, pr.text, c.want)
		}
	}
	runes := []struct{ text, want string }{{`'é'`, "233"}, {`'\n'`, "10"}}
	for _, c := range runes {
		pr, err := projectionFromLiteral(literalOf(t, "CHAR", c.text))
		if err != nil {
			t.Fatalf("%s: %v", c.text, err)
		}
		if pr.text != c.want {
			t.Fatalf("%s: got %q want %q", c.text, pr.text, c.want)
		}
	}
}

// A string constant projects as its contents, not as a quoted or JSON form.
func TestProjectionStringConstantIsPlain(t *testing.T) {
	pr, err := projectionFromLiteral(literalOf(t, "STRING", `"incremental"`))
	if err != nil {
		t.Fatal(err)
	}
	if pr.text != "incremental" {
		t.Fatalf("got %q", pr.text)
	}
	// Content that looks like JSON is still an ordinary string.
	pr, err = projectionFromLiteral(literalOf(t, "STRING", `"{\"a\":1}"`))
	if err != nil {
		t.Fatal(err)
	}
	if pr.text != `{"a":1}` || pr.kind != projectScalar {
		t.Fatalf("got %q kind %v", pr.text, pr.kind)
	}
}

// The float64 entry point is a refusal, not a convenience. 1.5 happens to be
// exact as a float64, so a lenient implementation would look correct there and
// diverge on 1.1; both must fail.
func TestProjectionFromFloat64AlwaysFailsClosed(t *testing.T) {
	for _, f := range []float64{1.5, 1.1, 0} {
		if _, err := projectionFromFloat64(f); err == nil {
			t.Fatalf("%v: expected refusal", f)
		} else if !strings.Contains(err.Error(), "source literal") {
			t.Fatalf("%v: refusal lacks the actionable hint: %v", f, err)
		}
	}
}

func TestProjectionRejectsBadLiterals(t *testing.T) {
	if _, err := projectionFromLiteral(nil); err == nil {
		t.Fatal("nil literal accepted")
	}
	if _, err := projectionFromLiteral(literalOf(t, "IMAG", "1i")); err == nil {
		t.Fatal("unsupported kind accepted")
	}
	if _, err := projectionFromLiteral(literalOf(t, "INT", "0x")); err == nil {
		t.Fatal("invalid literal accepted")
	}
}

func TestProjectValueChoosesKindFromProvenanceNotShape(t *testing.T) {
	var p projector
	p.projectionPush()
	p.projectionBind("h", objectProjection())
	p.projectionBind("s", scalarProjection())
	// Same emitted expression, different recorded provenance.
	object, err := p.projectValue("h", "h")
	if err != nil {
		t.Fatal(err)
	}
	scalar, err := p.projectValue("s", "s")
	if err != nil {
		t.Fatal(err)
	}
	if object != "shellrt.Project(h, shellrt.KindObject)" {
		t.Fatalf("object projection: %s", object)
	}
	if scalar != "shellrt.Project(s, shellrt.KindScalar)" {
		t.Fatalf("scalar projection: %s", scalar)
	}
	for _, src := range []string{object, scalar} {
		if _, err := parser.ParseExpr(src); err != nil {
			t.Fatalf("emitted %s is not a Go expression: %v", src, err)
		}
	}
}

// A retained constant emits a plain Go literal with no runtime call, which is
// how the exact spelling reaches the generated program.
func TestProjectValueEmitsConstantsDirectly(t *testing.T) {
	var p projector
	p.projectionBind("a", mustLiteral(t, "FLOAT", "1.5"))
	got, err := p.projectValue("a", "a")
	if err != nil {
		t.Fatal(err)
	}
	if got != `"3/2"` {
		t.Fatalf("got %s want %q", got, "3/2")
	}
	if strings.Contains(got, "shellrt") {
		t.Fatalf("constant projection emitted a runtime call: %s", got)
	}
	if _, err := parser.ParseExpr(got); err != nil {
		t.Fatalf("emitted %s is not a Go expression: %v", got, err)
	}
	p.projectionBind("b", projectionFromBool(true))
	if got, _ := p.projectValue("b", "b"); got != `"true"` {
		t.Fatalf("bool constant: %s", got)
	}
}

func mustLiteral(t *testing.T, kind, text string) projection {
	t.Helper()
	pr, err := projectionFromLiteral(literalOf(t, kind, text))
	if err != nil {
		t.Fatal(err)
	}
	return pr
}

func TestProjectValueRejectsUnknownName(t *testing.T) {
	var p projector
	p.projectionPush()
	_, err := p.projectValue("missing", "missing")
	if err == nil {
		t.Fatal("unknown name accepted")
	}
	d, ok := projectionDiagnostic(&syntax.Lit{}, err).(Diagnostic)
	if !ok || d.Code != CodeUndefined {
		t.Fatalf("got %#v want a %s diagnostic", err, CodeUndefined)
	}
	var p2 projector
	p2.projectionBind("h", objectProjection())
	if _, err := p2.projectValue("h", ""); err == nil {
		t.Fatal("empty expression accepted")
	}
}

// Emitter hook proof: the projector is driven in lockstep with the emitter's
// existing push/pop/bind, which is the seam the callable owner wires up. Inner
// bindings shadow and then disappear exactly as the emitter's own scopes do.
func TestProjectorTracksEmitterScopes(t *testing.T) {
	e := &emitter{scopes: []map[string]bool{{}}, funcs: map[string]bool{}, globals: map[string]bool{}}
	var p projector
	p.projectionPush()

	e.bind("h")
	p.projectionBind("h", objectProjection())

	e.push()
	p.projectionPush()
	e.bind("h") // shadows the outer rich root with a selected scalar
	p.projectionBind("h", scalarProjection())
	e.bind("n")
	p.projectionBind("n", mustLiteral(t, "INT", "0x1f"))

	if got, _ := p.projectValue("h", "h"); got != "shellrt.Project(h, shellrt.KindScalar)" {
		t.Fatalf("inner shadow: %s", got)
	}
	if got, _ := p.projectValue("n", "n"); got != `"31"` {
		t.Fatalf("inner constant: %s", got)
	}

	e.pop()
	p.projectionPop()

	if !e.known("h") {
		t.Fatal("emitter lost the outer binding")
	}
	if got, _ := p.projectValue("h", "h"); got != "shellrt.Project(h, shellrt.KindObject)" {
		t.Fatalf("outer rich root not restored: %s", got)
	}
	if _, err := p.projectValue("n", "n"); err == nil {
		t.Fatal("inner binding outlived its scope")
	}
	if e.known("n") {
		t.Fatal("emitter scope disagrees; the seam would drift")
	}
}

// Popping more than was pushed must not panic mid-compile.
func TestProjectorPopIsSafe(t *testing.T) {
	var p projector
	p.projectionPop()
	p.projectionPush()
	p.projectionPop()
	p.projectionPop()
	p.projectionBind("_", objectProjection())
	p.projectionBind("", objectProjection())
	if _, ok := p.projectionLookup("_"); ok {
		t.Fatal("blank identifier was bound")
	}
}
