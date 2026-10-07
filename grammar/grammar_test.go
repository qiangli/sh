package grammar

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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
	roots := []string{"../../bashsharp-tour", "../../bashsharp-tests/tools"}
	count, embedded := 0, 0
	embeddedByScript := map[string]int{}
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
			if root == roots[0] && ext != ".bsh" {
				return nil
			}
			if root == roots[1] && ext != ".sh" {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fixtures := map[string]string{}
			if ext == ".bsh" {
				fixtures[path] = string(data)
			} else {
				fixtures = embeddedBPP(path, string(data))
				embedded += len(fixtures)
				if len(fixtures) > 0 {
					embeddedByScript[filepath.Base(path)] += len(fixtures)
				}
			}
			for fixture, src := range fixtures {
				count++
				grammarErr := g.CheckDelta(src)
				recognized, inspectErr := g.Inspect(src)
				if inspectErr != nil && grammarErr == nil {
					t.Errorf("%s: inspect error: %v", fixture, inspectErr)
				}
				for rule, n := range recognized {
					covered[rule] += n
				}
				file, parserErr := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), fixture)
				if parserErr == nil {
					checkSites(t, g, fixture, src, file)
				}
				if (grammarErr == nil) != (parserErr == nil) {
					t.Errorf("%s: grammar: %v; parser: %v", fixture, grammarErr, parserErr)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if count < 55 || embedded < 15 {
		t.Fatalf("corpus missing: checked %d fixtures, including %d embedded", count, embedded)
	}
	if embeddedByScript["polyglot-gate.sh"] != 18 || len(embeddedByScript) != 1 {
		t.Fatalf("unexpected embedded fixture inventory: %v", embeddedByScript)
	}
	for _, rule := range []string{"fence-open", "decorator", "agentic-block", "agentic-shell-func", "agentic-typed-func", "agentic-typed-method", "typed-func"} {
		if covered[rule] == 0 {
			t.Errorf("published %s production was never exercised", rule)
		}
	}
	t.Logf("checked %d fixtures (%d Tour files, %d embedded .bpp heredocs); recognized %v", count, count-embedded, embedded, covered)
}

var bppHeredoc = regexp.MustCompile(`(?m)^cat\s+>[^\n]*\.bpp["']?\s+<<'([A-Za-z_][A-Za-z_0-9]*)'\s*$`)

func embeddedBPP(path, script string) map[string]string {
	out := map[string]string{}
	for _, m := range bppHeredoc.FindAllStringSubmatchIndex(script, -1) {
		marker := script[m[2]:m[3]]
		body := script[m[1]:]
		end := strings.Index(body, "\n"+marker+"\n")
		if end < 0 {
			continue
		}
		line := strings.Count(script[:m[0]], "\n") + 1
		out[filepath.Base(path)+":"+strconv.Itoa(line)] = body[:end] + "\n"
	}
	return out
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

func TestCommittedMalformedHeadersAgreeOnRejection(t *testing.T) {
	g, err := Load("delta.gbnf")
	if err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"agentic {\n", "func f( {\n", "agentic function f( {\n", "func (r Report) F( {\n"} {
		if err := g.CheckDelta(src); err == nil {
			t.Errorf("delta accepted %q", src)
		}
		if _, err := syntax.NewParser(syntax.Variant(syntax.LangBashPP)).Parse(strings.NewReader(src), "bad.bsh"); err == nil {
			t.Errorf("engine accepted %q", src)
		}
	}
}

func TestDeltaRejects(t *testing.T) {
	g, err := Load("delta.gbnf")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"fence":                   "~~~python as\npass\n~~~\n",
		"embed":                   "embed python \"/absolute.py\" as py\n",
		"decorator":               "@guard(read\nfunc f() {}\n",
		"go.error":                "@go.error(1)\nfunc f() {}\n",
		"agentic block":           "agentic {\necho open\n",
		"agentic shell function":  "agentic function f( { echo bad; }\n",
		"agentic typed function":  "agentic func f( { return 1 }\n",
		"agentic typed method":    "agentic func (r Report) F( { return 1 }\n",
		"typed function":          "func f( { return 1 }\n",
		"typed method":            "func (r Report) F( { return 1 }\n",
		"typed suffix":            "func f() ??? { return 1 }\n",
		"typed unclosed body":     "func f() int {\nreturn 1\n",
		"dangling decorator":      "@guard(\"read\")\necho done\n",
		"go.error shell function": "@go.error()\nfunction f() { :; }\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if err := g.CheckDelta(src); err == nil {
				t.Fatalf("accepted malformed delta %q", src)
			}
		})
	}
	for name, src := range map[string]string{
		"embed":                  "embed python \"./calc.py\" as py\n",
		"decorator":              "@guard(effects: \"read\")\nfunc f() {}\n",
		"go.error":               "@go.error()\nfunc f() {}\n",
		"agentic block":          "agentic { echo yes; }\n",
		"agentic shell function": "agentic function f() { echo yes; }\n",
		"agentic typed function": "agentic func f(x int) int { return x }\n",
		"agentic typed method":   "agentic func (r Report) F() int { return 1 }\n",
		"typed function":         "func f(x int) int { return x }\n",
		"typed method":           "func (r Report) F() int { return 1 }\n",
	} {
		t.Run(name+" valid", func(t *testing.T) {
			if err := g.CheckDelta(src); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// EBNF remains a compositional specification with opaque Bash/Go productions.
// Exercise its production graph: every claimed extension must be reachable from
// extension_statement, and every executable header must have an EBNF peer.
func TestEBNFProductionInventory(t *testing.T) {
	data, err := os.ReadFile("delta.ebnf")
	if err != nil {
		t.Fatal(err)
	}
	definitions := regexp.MustCompile(`(?m)^([a-z_]+)\s*=`).FindAllStringSubmatchIndex(string(data), -1)
	if len(definitions) == 0 {
		t.Fatal("no EBNF productions")
	}
	rules := map[string]string{}
	for i, m := range definitions {
		name := string(data[m[2]:m[3]])
		end := len(data)
		if i+1 < len(definitions) {
			end = definitions[i+1][0]
		}
		if _, duplicate := rules[name]; duplicate {
			t.Fatalf("duplicate EBNF %s", name)
		}
		rules[name] = string(data[m[1]:end])
	}
	for _, name := range []string{"extension_statement", "decorator", "go_error", "agentic_block", "agentic_function", "typed_function", "typed_method", "fence", "embed", "fence_open", "fence_close"} {
		if _, ok := rules[name]; !ok {
			t.Errorf("missing EBNF production %s", name)
		}
	}
	for _, name := range []string{"decorator_stack", "fence", "embed", "agentic_block", "agentic_function", "typed_function", "typed_method"} {
		if !strings.Contains(rules["extension_statement"], name) {
			t.Errorf("%s is unreachable from extension_statement", name)
		}
	}
	if !strings.Contains(rules["decorator"], "selector") || !strings.Contains(rules["decorator"], "go_error") || !strings.Contains(rules["fence"], "fence_close") || !strings.Contains(rules["typed_function"], "parameters") {
		t.Error("EBNF extension lost a required component")
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
