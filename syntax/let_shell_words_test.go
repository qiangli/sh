package syntax

import (
	"bytes"
	"strings"
	"testing"
)

func TestLetShellWords(t *testing.T) {
	cases := []struct {
		src                 string
		operands, redirects int
	}{
		{`let n 1>&2`, 1, 1},
		{`let n >&2`, 1, 1},
		{`let n 2>/dev/null`, 1, 1},
		{`let 2>/dev/null n`, 1, 1},
		{`let n=1 >out n+=2`, 2, 1},
		{`let n>out`, 1, 1},
		{`let 'n>0'`, 1, 0},
		{`let .sh.subshell`, 1, 0},
		{`let a+ b`, 2, 0},
		{`let + a`, 2, 0},
		{`let --$a`, 1, 0},
		{`let a+`, 1, 0},
		{`let !`, 1, 0},
		{`let a:b`, 1, 0},
		{`let "n=(1 + 2)"`, 1, 0},
		{`let n=(6 + 4) m=(8 + 5)`, 2, 0},
		{`let n=(6*4)/2`, 1, 0},
		{"let n=1 \\\n n+=2", 2, 0},
	}
	for _, lang := range []LangVariant{LangBash, LangBats, LangBashPP} {
		for _, tc := range cases {
			t.Run(lang.String()+"/"+tc.src, func(t *testing.T) {
				f, err := NewParser(Variant(lang)).Parse(strings.NewReader(tc.src), "let")
				if err != nil {
					t.Fatal(err)
				}
				check := func(f *File) {
					t.Helper()
					if len(f.Stmts) != 1 {
						t.Fatalf("statements: %d", len(f.Stmts))
					}
					lc, ok := f.Stmts[0].Cmd.(*LetClause)
					if !ok || len(lc.Exprs) != tc.operands || len(f.Stmts[0].Redirs) != tc.redirects {
						t.Fatalf("unexpected let tree: %#v", f.Stmts[0])
					}
					for _, operand := range lc.Exprs {
						if _, ok := operand.(*Word); !ok {
							t.Fatalf("operand must retain shell word: %T", operand)
						}
					}
				}
				check(f)
				var printed bytes.Buffer
				if err := NewPrinter().Print(&printed, f); err != nil {
					t.Fatal(err)
				}
				again, err := NewParser(Variant(lang)).Parse(strings.NewReader(printed.String()), "printed")
				if err != nil {
					t.Fatalf("printed %q: %v", printed.String(), err)
				}
				check(again)
				var second bytes.Buffer
				if err := NewPrinter().Print(&second, again); err != nil {
					t.Fatal(err)
				}
				if printed.String() != second.String() {
					t.Fatalf("unstable printing: %q then %q", printed.String(), second.String())
				}
			})
		}
	}
}

func TestLetDialectBoundaries(t *testing.T) {
	for _, lang := range []LangVariant{LangBash, LangBats, LangBashPP, LangMirBSDKorn, LangZsh, LangPOSIX} {
		t.Run(lang.String(), func(t *testing.T) {
			f, err := NewParser(Variant(lang)).Parse(strings.NewReader(`let i++`), "")
			if err != nil {
				t.Fatal(err)
			}
			if lang == LangPOSIX {
				if _, ok := f.Stmts[0].Cmd.(*CallExpr); !ok {
					t.Fatal("POSIX let must remain ordinary command")
				}
				return
			}
			lc := f.Stmts[0].Cmd.(*LetClause)
			if lang == LangMirBSDKorn || lang == LangZsh {
				if _, ok := lc.Exprs[0].(*UnaryArithm); !ok {
					t.Fatal("legacy arithmetic tree changed")
				}
			}
			_, err = NewParser(Variant(lang)).Parse(strings.NewReader(`let i=(1 + 2)`), "")
			if (err == nil) != (lang != LangPOSIX) {
				t.Fatalf("grouped unquoted operand: %v", err)
			}
		})
	}
}

func TestLetParenthesisGrammar(t *testing.T) {
	for _, src := range []string{`let (2+3)`, `let n=2/(3+4)`, `let n=(2 + (3*4))`, `let n=(1 > 0)`, `if false; then let n=(.; fi`} {
		t.Run(src, func(t *testing.T) {
			if _, err := NewParser(Variant(LangBash)).Parse(strings.NewReader(src), ""); err == nil {
				t.Fatal("invalid shell parenthesis grammar accepted")
			}
		})
	}
}
