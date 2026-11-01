package interp

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// sourceGrammarAlias resolves only aliases that introduce shell grammar. Plain
// command aliases retain the existing execution/timing path. Source is a fresh
// read, so every alias already defined by its caller is visible here.
func (r *Runner) sourceGrammarAlias(name string, seen map[string]bool) (string, bool) {
	if seen[name] {
		return "", false
	}
	a, ok := r.alias[name]
	if !ok {
		return "", false
	}
	seen[name] = true
	value := aliasValue(a)
	trimmed := strings.TrimLeft(value, " \t\r\n")
	for _, kw := range []string{"!", "while", "until", "for", "select", "if", "case", "{"} {
		if trimmed == kw || strings.HasPrefix(trimmed, kw+" ") || strings.HasPrefix(trimmed, kw+"\t") || strings.HasPrefix(trimmed, kw+"\n") || strings.HasPrefix(trimmed, kw+";") {
			return value, true
		}
	}
	// Let the shell word lexer identify a chained alias's literal head. Never
	// interpret quoted, escaped or expanded text as an alias name.
	for w, err := range syntax.NewParser().WordsSeq(strings.NewReader(value)) {
		if err != nil || w == nil || w.Lit() == "" {
			return "", false
		}
		head := w.Lit()
		start, end := int(w.Pos().Offset()), int(w.End().Offset())
		if start < 0 || end > len(value) || value[start:end] != head {
			return "", false
		}
		replacement, ok := r.sourceGrammarAlias(head, seen)
		if !ok {
			return "", false
		}
		return value[:start] + replacement + value[end:], true
	}
	return "", false
}

type sourceAliasEdit struct {
	start, end  int
	replacement string
	names       map[string]bool
}

func (*sourceAliasEdit) Error() string { return "source alias requires reparse" }

type sourceAliasRange struct {
	start, end int
	names      map[string]bool
}

// parseSourceAliases retries only at lexer-identified command words. The
// bounded reparse never executes partial trees or scans/replaces arbitrary text.
// Alias definition/unalias commands inside this source are not executed early.
func (r *Runner) parseSourceAliases(p *syntax.Parser, source []byte, path string) (*syntax.File, error) {
	if !r.opts[optExpandAliases] || len(r.alias) == 0 {
		return p.Parse(strings.NewReader(string(source)), path)
	}
	text := string(source)
	var ranges []sourceAliasRange
	syntax.CommandWordObserver(func(name string, pos syntax.Pos) error {
		start := int(pos.Offset())
		end := start + len(name)
		if start < 0 || end > len(text) || text[start:end] != name {
			return nil
		}
		for _, span := range ranges {
			if start >= span.start && start < span.end && span.names[name] {
				// Bash suppresses this alias inside its own replacement. Quote
				// the literal so the execution-time alias path also respects it.
				return &sourceAliasEdit{start, start + len(name), "'" + strings.ReplaceAll(name, "'", "'\\''") + "'", nil}
			}
		}
		names := make(map[string]bool)
		replacement, ok := r.sourceGrammarAlias(name, names)
		if !ok {
			return nil
		}
		return &sourceAliasEdit{start, end, replacement, names}
	})(p)
	defer syntax.CommandWordObserver(nil)(p)
	for n := 0; n < 1024; n++ {
		file, err := p.Parse(strings.NewReader(text), path)
		edit, ok := err.(*sourceAliasEdit)
		if !ok {
			return file, err
		}
		delta := len(edit.replacement) - (edit.end - edit.start)
		for i := range ranges {
			span := &ranges[i]
			if span.start >= edit.end {
				span.start += delta
				span.end += delta
			} else if span.end > edit.start {
				span.end += delta
			}
		}
		text = text[:edit.start] + edit.replacement + text[edit.end:]
		ranges = append(ranges, sourceAliasRange{edit.start, edit.start + len(edit.replacement), edit.names})
	}
	return nil, fmt.Errorf("%s: source alias expansion limit exceeded", path)
}
