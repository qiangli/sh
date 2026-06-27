// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

// gosh is a proof of concept shell built on top of [interp].
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

var command = flag.String("c", "", "command to be executed")
var posix = flag.Bool("posix", false, "run in POSIX mode")

func main() {
	flag.Parse()
	err := runAll()
	var es interp.ExitStatus
	if errors.As(err, &es) {
		os.Exit(int(es))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runAll() error {
	opts := []interp.RunnerOption{
		interp.Env(nil),
		interp.Interactive(true),
		interp.StdIO(os.Stdin, os.Stdout, os.Stderr),
		interp.WithBashCompatErrors(true),
		interp.WithInheritedFds(inheritedFdsFromEnv()),
	}
	if *posix {
		opts = append(opts, interp.WithPosixMode(true))
	}
	r, err := interp.New(opts...)
	if err != nil {
		return err
	}

	if *command != "" {
		return run(r, strings.NewReader(*command), "bash")
	}
	if flag.NArg() == 0 {
		if term.IsTerminal(int(os.Stdin.Fd())) {
			return runInteractive(r, os.Stdin, os.Stdout, os.Stderr)
		}
		return run(r, os.Stdin, "bash")
	}
	for _, path := range flag.Args() {
		if err := runPath(r, path); err != nil {
			return err
		}
	}
	return nil
}

func inheritedFdsFromEnv() []int {
	var fds []int
	for _, part := range strings.Split(os.Getenv(interp.BashyInheritedFdsEnv), ",") {
		if part == "" {
			continue
		}
		fd, err := strconv.Atoi(part)
		if err == nil && fd >= 3 {
			fds = append(fds, fd)
		}
	}
	return fds
}

func run(r *interp.Runner, reader io.Reader, name string) error {
	src, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	var hdocWarnings []string
	parserOpts := []syntax.ParserOption{
		syntax.HeredocEOFWarning(func(startLine, eofLine int, stop string) {
			hdocWarnings = append(hdocWarnings, fmt.Sprintf(
				"%s: line %d: warning: here-document at line %d delimited by end-of-file (wanted `%s')",
				name, eofLine, startLine, stop))
		}),
	}
	if *posix {
		parserOpts = append(parserOpts, syntax.Variant(syntax.LangPOSIX))
	}
	parser := syntax.NewParser(parserOpts...)
	prog, err := parser.Parse(strings.NewReader(string(src)), name)
	if err != nil {
		if msg, ok := bashScriptParseError(err, src, name); ok {
			if len(hdocWarnings) > 0 {
				msg = strings.Join(append(hdocWarnings, msg), "\n")
			}
			return errors.New(msg)
		}
		if msg, ok := bashDbracketParseError(err, src, name); ok {
			if len(hdocWarnings) > 0 {
				msg = strings.Join(append(hdocWarnings, msg), "\n")
			}
			return errors.New(msg)
		}
		if len(hdocWarnings) > 0 {
			return errors.New(strings.Join(append(hdocWarnings, err.Error()), "\n"))
		}
		return err
	}
	if len(hdocWarnings) > 0 {
		fmt.Fprintln(os.Stderr, strings.Join(hdocWarnings, "\n"))
	}
	r.Reset()
	if err := interp.WithBashSource(src)(r); err != nil {
		return err
	}
	ctx := context.Background()
	return r.Run(ctx, prog)
}

func bashScriptParseError(err error, src []byte, name string) (string, bool) {
	var pe syntax.ParseError
	if !errors.As(err, &pe) {
		return "", false
	}
	if name == "" {
		name = "bash"
	}
	line := int(pe.Pos.Line())
	srcLine := sourceLine(src, line)
	switch {
	case pe.Text == "`case` must be followed by a word":
		return bashParseTwoLine(name, line, "syntax error near unexpected token `newline'", srcLine), true
	case pe.Text == "`do` can only be used in a loop":
		return bashParseTwoLine(name, line, "syntax error near unexpected token `do'", srcLine), true
	case pe.Text == "unexpected EOF while looking for matching `}'":
		eofLine := strings.Count(string(src), "\n") + 1
		return fmt.Sprintf("%s: line %d: syntax error: unexpected end of file from `{' command on line %d",
			name, eofLine, line), true
	case strings.HasPrefix(pe.Text, "unexpected EOF while looking for matching `"):
		if strings.HasSuffix(pe.Text, "'") {
			quote := strings.TrimSuffix(strings.TrimPrefix(pe.Text, "unexpected EOF while looking for matching `"), "'")
			switch quote {
			case "'", `"`, "`":
				return fmt.Sprintf("%s: line %d: %s", name, line, pe.Text), true
			}
		}
	case strings.HasPrefix(pe.Text, "reached EOF without closing quote `"):
		quote := "'"
		if strings.Contains(pe.Text, "`\"`") {
			quote = "\""
		} else if strings.Contains(pe.Text, "\"`\"") {
			quote = "`"
		}
		return fmt.Sprintf("%s: line %d: unexpected EOF while looking for matching `%s'", name, line, quote), true
	}
	return "", false
}

func bashParseTwoLine(name string, line int, msg, srcLine string) string {
	return fmt.Sprintf("%s: line %d: %s\n%s: line %d: `%s'", name, line, msg, name, line, srcLine)
}

func bashDbracketParseError(err error, src []byte, name string) (string, bool) {
	var pe syntax.ParseError
	if !errors.As(err, &pe) {
		return "", false
	}
	line := int(pe.Pos.Line())
	stmtLine, stmt := dbracketSourceLine(src, line)
	if stmtLine == 0 {
		return "", false
	}
	if name == "" {
		name = "bashy"
	}
	prefix := fmt.Sprintf("%s: line %d: ", name, stmtLine)
	near := dbracketNearToken(pe, stmt)
	var b strings.Builder
	if first := dbracketFirstDiagnostic(pe, stmt); first != "" {
		b.WriteString(prefix)
		b.WriteString(first)
		b.WriteByte('\n')
	}
	b.WriteString(prefix)
	b.WriteString("syntax error near `")
	b.WriteString(near)
	b.WriteString("'\n")
	b.WriteString(prefix)
	b.WriteByte('`')
	b.WriteString(stmt)
	b.WriteByte('\'')
	return b.String(), true
}

func sourceLine(src []byte, line int) string {
	if line < 1 {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	if line > len(lines) {
		return ""
	}
	return lines[line-1]
}

func dbracketSourceLine(src []byte, line int) (int, string) {
	for n := line; n >= 1; n-- {
		stmt := sourceLine(src, n)
		if strings.HasPrefix(strings.TrimLeft(stmt, " \t"), "[[") {
			return n, stmt
		}
	}
	return 0, ""
}

func dbracketFirstDiagnostic(pe syntax.ParseError, stmt string) string {
	text := pe.Text
	trim := strings.TrimSpace(stmt)
	fields := strings.Fields(trim)
	switch {
	case text == "`[[` must be followed by an expression" && strings.HasPrefix(trim, "[[ &&"):
		return "unexpected token `&&' in conditional command"
	case text == "`[[` must be followed by an expression":
		return ""
	case strings.Contains(text, "must be followed by a word"):
		op := strings.TrimPrefix(text, "`")
		if i := strings.IndexByte(op, '`'); i >= 0 {
			op = op[:i]
		}
		if len(fields) >= 3 && fields[0] == "[[" && fields[1] == op && fields[2] == "]]" {
			return "unexpected argument `]]' to conditional unary operator"
		}
		if len(fields) >= 3 && fields[0] == "[[" && fields[1] == op {
			return fmt.Sprintf("unexpected argument `%s' to conditional unary operator", fields[2])
		}
	case strings.Contains(text, "not a valid test operator"):
		if len(fields) == 3 && fields[0] == "[[" && (fields[1] == "-z" || fields[1] == "-n") && fields[2] == "]]" {
			return "unexpected argument `]]' to conditional unary operator"
		}
		if len(fields) >= 4 && fields[0] == "[[" {
			if fields[1] == "-z" || fields[1] == "-n" {
				return fmt.Sprintf("syntax error in conditional expression: unexpected token `%s'", fields[3])
			}
			return fmt.Sprintf("unexpected token `%s', conditional binary operator expected", condBinOpToken(fields[2]))
		}
		return "conditional binary operator expected"
	}
	return text
}

// condBinOpToken returns the fragment bash names as the "unexpected
// token" when it finds tok where a conditional binary operator was
// expected. A redirection-shaped token such as `3<` / `3>` is reported
// by its leading file-descriptor digits alone (`3`), even though the
// "syntax error near" line still echoes the whole `3<`.
func condBinOpToken(tok string) string {
	for i := 0; i < len(tok); i++ {
		if tok[i] >= '0' && tok[i] <= '9' {
			continue
		}
		if i > 0 && (tok[i] == '<' || tok[i] == '>') {
			return tok[:i]
		}
		break
	}
	return tok
}

func dbracketNearToken(pe syntax.ParseError, stmt string) string {
	fields := strings.Fields(stmt)
	text := pe.Text
	if text == "`[[` must be followed by an expression" {
		if len(fields) >= 2 && fields[1] == "&&" {
			return "&"
		}
		return "]]"
	}
	if strings.Contains(text, "must be followed by a word") {
		if len(fields) >= 3 {
			return strings.Trim(fields[2], "'\"")
		}
	}
	if strings.Contains(text, "not a valid test operator") && len(fields) >= 3 {
		if len(fields) == 3 && fields[2] == "]]" {
			return "]]"
		}
		if len(fields) >= 4 && (fields[1] == "-z" || fields[1] == "-n") {
			return strings.Trim(fields[3], "'\"")
		}
		op := fields[2]
		if strings.HasPrefix(op, "3<") || strings.HasPrefix(op, "3>") {
			return op
		}
		return strings.Trim(op, "'\"")
	}
	if col := int(pe.Pos.Col()); col > 0 && col <= len(stmt) {
		rest := stmt[col-1:]
		if tok := strings.Fields(rest); len(tok) > 0 {
			return strings.Trim(tok[0], "'\"")
		}
	}
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}

func runPath(r *interp.Runner, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return run(r, f, path)
}

func runInteractive(r *interp.Runner, stdin io.Reader, stdout, stderr io.Writer) error {
	var parserOpts []syntax.ParserOption
	if *posix {
		parserOpts = append(parserOpts, syntax.Variant(syntax.LangPOSIX))
	}
	parser := syntax.NewParser(parserOpts...)
	fmt.Fprintf(stdout, "$ ")
	for stmts, err := range parser.InteractiveSeq(stdin) {
		if err != nil {
			return err // stop at the first error
		}
		if parser.Incomplete() {
			fmt.Fprintf(stdout, "> ")
			continue
		}
		ctx := context.Background()
		for _, stmt := range stmts {
			err := r.Run(ctx, stmt)
			if r.Exited() {
				return err
			}
		}
		fmt.Fprintf(stdout, "$ ")
	}
	return nil
}
