package shellrt

import (
	"go/constant"
	"go/token"
	"testing"
)

func TestLexicalExpressionKeepsWideOperandsAndNativeIdentity(t *testing.T) {
	p := lexicalProgram(t)
	cell := Cell[int8](p.Bindings, "tiny", "tiny", KindScalar)
	cell.Value = 1
	cell.Present = true
	writeRaw(t, p, "tiny", "128")
	operand := LexicalOperand[int8](p.Bindings, "tiny", ValueSite{Name: "tiny"})
	if got := LexicalBinary[int8](operand, constant.MakeInt64(2), token.QUO, ValueSite{}); got != 64 {
		t.Fatalf("division narrowed too early: %v", got)
	}
	if got := LexicalBinary[bool](operand, constant.MakeInt64(0), token.GTR, ValueSite{}); !got {
		t.Fatal("comparison narrowed too early")
	}
	if got := LexicalBinary[int8](operand, constant.MakeInt64(1), token.ADD, ValueSite{}); got != -127 {
		t.Fatalf("result did not narrow: %v", got)
	}
	if got := LexicalPrintAddress(p.Bindings, &cell.Value, "tiny", ValueSite{}); got != int64(128) {
		t.Fatalf("alias print narrowed: %#v", got)
	}
	writeRaw(t, p, "tiny", "bad")
	site := ValueSite{File: "alias.bpp", Name: "tiny", Line: 8, Column: 4}
	_, err := TryValue(func() int8 {
		return LexicalBinary[int8](LexicalOperandAddress(p.Bindings, &cell.Value, site), constant.MakeInt64(1), token.ADD, site)
	})
	if err == nil || err.Error() != "BASHPP-EEXPR-CONVERT: cannot convert String to int8" {
		t.Fatalf("alias error=%v", err)
	}
}
func TestLexicalExactPreservesCompilerRational(t *testing.T) {
	if got := LexicalExact("1/10").ExactString(); got != "1/10" {
		t.Fatal(got)
	}
	p := lexicalProgram(t)
	cell := Cell[float64](p.Bindings, "ratio", "ratio", KindScalar)
	cell.Value = .1
	cell.Present = true
	if err := p.Bindings.NativeScalarWritten("ratio", LexicalExact("1/10")); err != nil {
		t.Fatal(err)
	}
	value, present, err := p.Bindings.ShellValue(p.Session, "ratio")
	if err != nil || !present || value != "1/10" {
		t.Fatalf("%q/%v/%v", value, present, err)
	}
}
