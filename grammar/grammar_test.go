package grammar

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func TestPublishedProductions(t *testing.T) {
	g, err := Load("delta.gbnf")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		rule, src string
		want      bool
	}{
		{"fence-open", "~~~python as py", true},
		{"fence-open", "~~~~c++ as lib !builder", true},
		{"fence-open", "~~~skill as !runner", true},
		{"root", "~~~python as py", true},
		{"fence-open", "~~~tf !bad-", false},
		{"fence-open", " ~~~python", false},
		{"fence-open", "~~~python nope", false},
		{"embed", "embed python \"./calc.py\" as py", true},
		{"embed", "embed python \"/tmp/calc.py\" as py", false},
		{"decorator", "@go.error()", true},
		{"decorator", "@guard(effects: \"read\")", true},
		{"go-error", "@go.error()", true},
		{"go-error", "@go.error(1)", false},
		{"agentic-block", "agentic {", true},
		{"agentic-shell-func", "agentic function helper() {", true},
		{"agentic-typed-func", "agentic func helper(x int) int {", true},
		{"typed-func", "func helper(x int) int {", true},
		{"typed-method", "func (r Report) Name() string {", true},
	}
	for _, c := range cases {
		if got := g.Match(c.rule, c.src); got != c.want {
			t.Errorf("%s %q: got %t, want %t", c.rule, c.src, got, c.want)
		}
	}
}

func TestAgreementWithPublishedCorpora(t *testing.T) {
	g, err := Load("delta.gbnf")
	if err != nil {
		t.Fatal(err)
	}
	roots := []string{"../../bashsharp-tour", "../../bashsharp-tests/tools/bashsharp"}
	count := 0
	covered := map[string]int{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			ext := filepath.Ext(path)
			if root == roots[0] && ext != ".bsh" && ext != ".sh" {
				return nil
			}
			if root == roots[1] && ext != ".sh" {
				return nil
			}
			count++
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			grammarErr := g.CheckDelta(string(data))
			recognized, inspectErr := g.Inspect(string(data))
			if inspectErr != nil && grammarErr == nil {
				t.Errorf("%s: inspect error: %v", path, inspectErr)
			}
			for rule, n := range recognized {
				covered[rule] += n
			}
			file, parserErr := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(string(data)), path)
			if parserErr == nil {
				checkSites(t, g, path, string(data), file)
			}
			if (grammarErr == nil) != (parserErr == nil) {
				t.Errorf("%s: grammar: %v; parser: %v", path, grammarErr, parserErr)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if count < 44 {
		t.Fatalf("corpus missing: checked %d files", count)
	}
	for _, rule := range []string{"fence-open", "decorator", "agentic-block", "agentic-shell-func", "agentic-typed-func", "agentic-typed-method", "typed-func"} {
		if covered[rule] == 0 {
			t.Errorf("published %s production was never exercised", rule)
		}
	}
	t.Logf("checked %d Tour and bashsharp tool fixtures; recognized %v", count, covered)
}

func TestUnclosedFenceVerdict(t *testing.T) {
	g, err := Load("delta.gbnf")
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"~~~python\nprint(1)\n", "~~~~skill as s\nTask: x\n~~~\n"} {
		grammarErr := g.CheckDelta(src)
		_, parserErr := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "bad.bsh")
		if grammarErr == nil || parserErr == nil {
			t.Errorf("expected both to reject %q: grammar=%v parser=%v", src, grammarErr, parserErr)
		}
	}
}

// The GBNF interpreter identifies each extension site independently from the
// parser's AST. The AST supplies only locations, so a parser-recognized site
// absent from the published grammar fails this coverage check.
func checkSites(t *testing.T, g *Grammar, path, src string, file *syntax.File) {
	t.Helper()
	lines := strings.Split(src, "\n")
	matchAt := func(pos syntax.Pos, rules ...string) {
		t.Helper()
		n := int(pos.Line())
		if n < 1 || n > len(lines) {
			t.Errorf("%s: invalid extension line %d", path, n)
			return
		}
		line := strings.TrimSuffix(lines[n-1], "\r")
		for _, rule := range rules {
			if g.Match(rule, line) {
				return
			}
		}
		t.Errorf("%s:%d: no published production %v accepts %q", path, n, rules, line)
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.SourceBlock:
			if n.Src != nil {
				matchAt(n.Pos(), "embed")
			} else {
				matchAt(n.Pos(), "fence-open")
			}
		case *syntax.BashPPDecorator:
			matchAt(n.Pos(), "decorator")
		case *syntax.BashPPAgenticBlock:
			matchAt(n.Pos(), "agentic-block")
		case *syntax.BashPPFuncDecl:
			if n.Agentic != nil {
				matchAt(n.Agentic.Pos(), "agentic-typed-func", "agentic-typed-method")
			} else if n.Receiver != nil {
				matchAt(n.Kw.Pos(), "typed-method")
			} else {
				matchAt(n.Kw.Pos(), "typed-func")
			}
		case *syntax.FuncDecl:
			if n.Agentic != nil {
				matchAt(n.Agentic.Pos(), "agentic-shell-func")
			}
		}
		return true
	})
}
