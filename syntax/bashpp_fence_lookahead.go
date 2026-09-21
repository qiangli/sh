package syntax

import "strings"

// bashppFenceLookahead maps a fence language spelling — canonical and alias
// alike — to the reader that extracts `name(` from one body line when the
// line declares a top-level function, and "" otherwise. It exists only so an
// unaliased fence's later bare `name()` parses as a call; the language's
// analyzer decides what is really exported. Text fences have no row: they
// are always called through their alias and promote nothing.
var bashppFenceLookahead = map[string]func(line string) string{}

func bashppRegisterFenceLookahead(read func(line string) string, spellings ...string) {
	for _, spelling := range spellings {
		bashppFenceLookahead[spelling] = read
	}
}

func init() {
	bashppRegisterFenceLookahead(func(line string) string {
		if strings.HasPrefix(line, "def ") {
			return strings.TrimPrefix(line, "def ")
		}
		return ""
	}, "python", "py")
	bashppRegisterFenceLookahead(func(line string) string {
		declaration := strings.TrimPrefix(line, "export ")
		if !strings.HasPrefix(declaration, "function ") {
			return ""
		}
		return strings.TrimPrefix(declaration, "function ")
	}, "typescript", "ts")
	bashppRegisterFenceLookahead(func(line string) string {
		declaration := strings.TrimSpace(line)
		if !strings.HasPrefix(declaration, "pub fn ") {
			return ""
		}
		return strings.TrimPrefix(declaration, "pub fn ")
	}, "rust", "rs")
	bashppRegisterFenceLookahead(func(line string) string {
		declaration := strings.TrimSpace(line)
		if strings.HasPrefix(declaration, "static ") || strings.HasPrefix(declaration, "#") {
			return ""
		}
		before, after, ok := strings.Cut(declaration, "(")
		if !ok || before == "" {
			return ""
		}
		head := strings.TrimSpace(before)
		if strings.HasSuffix(head, "if") || strings.HasSuffix(head, "for") || strings.HasSuffix(head, "while") {
			return ""
		}
		parts := strings.Fields(before)
		if len(parts) <= 1 {
			return ""
		}
		return parts[len(parts)-1] + "(" + after
	}, "c", "cpp", "cxx")
	bashppRegisterFenceLookahead(func(line string) string {
		declaration := strings.TrimSpace(line)
		if !strings.HasPrefix(declaration, "func ") {
			return ""
		}
		return strings.TrimPrefix(declaration, "func ")
	}, "go")
	bashppRegisterFenceLookahead(func(line string) string {
		before, _, ok := strings.Cut(strings.TrimSpace(line), "()")
		if !ok {
			return ""
		}
		return strings.TrimSpace(before) + "("
	}, "bash", "sh")
}
