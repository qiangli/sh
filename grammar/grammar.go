// Package grammar interprets the published Bash# delta GBNF. It recognizes
// extension headers and fence boundaries; ordinary Bash remains opaque.
package grammar

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Grammar struct {
	rules    map[string]string
	patterns map[string]*regexp.Regexp
}

func Load(path string) (*Grammar, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	g := &Grammar{rules: map[string]string{}, patterns: map[string]*regexp.Regexp{}}
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rhs, ok := strings.Cut(line, "::=")
		if !ok {
			return nil, fmt.Errorf("invalid GBNF rule: %s", line)
		}
		name = strings.TrimSpace(name)
		if name == "" || strings.TrimSpace(rhs) == "" {
			return nil, fmt.Errorf("empty GBNF rule: %s", line)
		}
		if _, exists := g.rules[name]; exists {
			return nil, fmt.Errorf("duplicate GBNF rule %s", name)
		}
		g.rules[name] = strings.TrimSpace(rhs)
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	for name := range g.rules {
		if _, err := g.pattern(name, map[string]bool{}); err != nil {
			return nil, err
		}
	}
	return g, nil
}

func (g *Grammar) Match(rule, source string) bool {
	re := g.patterns[rule]
	return re != nil && re.MatchString(source)
}

func (g *Grammar) pattern(name string, active map[string]bool) (string, error) {
	if re := g.patterns[name]; re != nil {
		return strings.TrimSuffix(strings.TrimPrefix(re.String(), "^(?:"), ")$"), nil
	}
	rhs, ok := g.rules[name]
	if !ok {
		return "", fmt.Errorf("undefined GBNF rule %s", name)
	}
	if active[name] {
		return "", fmt.Errorf("recursive GBNF rule %s", name)
	}
	active[name] = true
	p := &expression{src: rhs, g: g, active: active}
	body, err := p.choice()
	p.skip()
	if err == nil && p.i != len(rhs) {
		err = fmt.Errorf("unexpected GBNF in %s at %q", name, rhs[p.i:])
	}
	delete(active, name)
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	re, err := regexp.Compile("^(?:" + body + ")$")
	if err != nil {
		return "", fmt.Errorf("%s: %w", name, err)
	}
	g.patterns[name] = re
	return body, nil
}

type expression struct {
	src    string
	i      int
	g      *Grammar
	active map[string]bool
}

func (p *expression) skip() bool {
	for p.i < len(p.src) && (p.src[p.i] == ' ' || p.src[p.i] == '\t') {
		p.i++
	}
	return p.i < len(p.src)
}
func (p *expression) choice() (string, error) {
	var parts []string
	for {
		s, err := p.sequence()
		if err != nil {
			return "", err
		}
		parts = append(parts, s)
		if !p.skip() || p.src[p.i] != '|' {
			break
		}
		p.i++
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return "(?:" + strings.Join(parts, "|") + ")", nil
}
func (p *expression) sequence() (string, error) {
	var out strings.Builder
	for p.skip() && p.src[p.i] != ')' && p.src[p.i] != '|' {
		atom, err := p.atom()
		if err != nil {
			return "", err
		}
		if p.skip() && strings.ContainsRune("*+?", rune(p.src[p.i])) {
			atom = "(?:" + atom + ")" + string(p.src[p.i])
			p.i++
		}
		out.WriteString(atom)
	}
	return out.String(), nil
}
func (p *expression) atom() (string, error) {
	switch p.src[p.i] {
	case '(':
		p.i++
		s, err := p.choice()
		if err != nil {
			return "", err
		}
		if !p.skip() || p.src[p.i] != ')' {
			return "", fmt.Errorf("unclosed group")
		}
		p.i++
		return "(?:" + s + ")", nil
	case '"':
		start := p.i
		p.i++
		for p.i < len(p.src) {
			if p.src[p.i] == '\\' {
				p.i += 2
				continue
			}
			if p.src[p.i] == '"' {
				p.i++
				break
			}
			p.i++
		}
		if p.i > len(p.src) || p.src[p.i-1] != '"' {
			return "", fmt.Errorf("unclosed string")
		}
		lit, err := strconv.Unquote(p.src[start:p.i])
		if err != nil {
			return "", err
		}
		return regexp.QuoteMeta(lit), nil
	case '[':
		start := p.i
		p.i++
		for p.i < len(p.src) {
			if p.src[p.i] == '\\' {
				p.i += 2
				continue
			}
			if p.src[p.i] == ']' {
				p.i++
				break
			}
			p.i++
		}
		if p.i > len(p.src) || p.src[p.i-1] != ']' {
			return "", fmt.Errorf("unclosed character class")
		}
		return p.src[start:p.i], nil
	default:
		start := p.i
		for p.i < len(p.src) {
			c := p.src[p.i]
			if c == '-' || c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' {
				p.i++
			} else {
				break
			}
		}
		if start == p.i {
			return "", fmt.Errorf("unexpected %q", p.src[p.i:])
		}
		return p.g.pattern(p.src[start:p.i], p.active)
	}
}

// CheckDelta checks the productions whose complete shape is specified here.
// Bash lines and the contents of foreign fences are intentionally opaque.
func (g *Grammar) CheckDelta(src string) error {
	_, err := g.Inspect(src)
	return err
}

// Inspect interprets the published extension headers and reports which rules
// accepted them. Unclaimed lines are delegated to the opaque Bash base.
func (g *Grammar) Inspect(src string) (map[string]int, error) {
	counts := make(map[string]int)
	lines := strings.SplitAfter(src, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	fence := ""
	for i, raw := range lines {
		line := strings.TrimSuffix(strings.TrimSuffix(raw, "\n"), "\r")
		if fence != "" {
			if line == fence {
				fence = ""
			}
			continue
		}
		if !strings.HasPrefix(line, "~~~") {
			switch {
			case strings.HasPrefix(line, "@"):
				if g.Match("go-error", line) {
					counts["go-error"]++
				}
				if g.Match("decorator", line) {
					counts["decorator"]++
				}
			case strings.HasPrefix(line, "embed "):
				if g.Match("embed", line) {
					counts["embed"]++
				}
			case strings.HasPrefix(line, "agentic "):
				for _, rule := range []string{"agentic-block", "agentic-shell-func", "agentic-typed-func", "agentic-typed-method"} {
					if g.Match(rule, line) {
						counts[rule]++
						break
					}
				}
			case strings.HasPrefix(line, "func "):
				for _, rule := range []string{"typed-func", "typed-method"} {
					if g.Match(rule, line) {
						counts[rule]++
						break
					}
				}
			}
			continue
		}
		if !g.Match("fence-open", line) {
			continue
		} // Class E near miss: Bash command.
		counts["fence-open"]++
		n := 0
		for n < len(line) && line[n] == '~' {
			n++
		}
		fence = line[:n]
		if i == len(lines)-1 {
			return counts, fmt.Errorf("line %d: unclosed fence %s", i+1, fence)
		}
	}
	if fence != "" {
		return counts, fmt.Errorf("unclosed fence %s", fence)
	}
	return counts, nil
}
