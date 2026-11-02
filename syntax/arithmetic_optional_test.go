package syntax

import (
	"bytes"
	"strings"
	"testing"
)

func TestArithmeticExpansionAfterGroup(t *testing.T) {
	for _, lang := range []LangVariant{LangBash, LangBats, LangBashPP, LangPOSIX} {
		for _, src := range []string{
			`echo "$(((2)${tail+3}))"`,
			`echo "$(((2) ${tail+3}))"`,
			`echo "$(( (2+3)!=($value)${extra+${oops:?unexpected}} ))"`,
			`echo "$(((+ +n)${tail+3}))"`,
		} {
			t.Run(lang.String()+"/"+src, func(t *testing.T) {
				parse := func(src string) *File {
					t.Helper()
					f, err := NewParser(Variant(lang)).Parse(strings.NewReader(src), "")
					if err != nil {
						t.Fatal(err)
					}
					return f
				}
				f := parse(src)
				params := 0
				Walk(f, func(node Node) bool {
					if pe, ok := node.(*ParamExp); ok && pe.Dollar.IsValid() {
						params++
					}
					return true
				})
				if params == 0 {
					t.Fatal("parameter expansion nodes were lost")
				}
				var first, second bytes.Buffer
				if err := NewPrinter().Print(&first, f); err != nil {
					t.Fatal(err)
				}
				if err := NewPrinter().Print(&second, parse(first.String())); err != nil {
					t.Fatal(err)
				}
				if first.String() != second.String() {
					t.Fatalf("unstable printing: %q then %q", first.String(), second.String())
				}
			})
		}
	}
	for _, src := range []string{`echo "$(((2)3))"`, `echo "$(((2) + ))"`} {
		if _, err := NewParser().Parse(strings.NewReader(src), ""); err == nil {
			t.Fatalf("invalid static arithmetic accepted: %s", src)
		}
	}
}
