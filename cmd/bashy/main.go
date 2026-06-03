// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

// bashy is a Bash 5.3 compatible shell built on top of [interp].
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

var (
	command   = flag.String("c", "", "command to be executed")
	version   = flag.Bool("version", false, "print version and exit")
	posix     = flag.Bool("posix", false, "POSIX mode")
	norc      = flag.Bool("norc", false, "do not read ~/.bashyrc")
	noprofile = flag.Bool("noprofile", false, "do not read /etc/profile or ~/.bashy_profile")
	login     = flag.Bool("login", false, "act as a login shell")
	optsOn    multiFlag
	optsOff   multiFlag
)

// multiFlag collects repeated string values for a flag, e.g. -o opt.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func init() {
	flag.Var(&optsOn, "o", "enable a set option (posix, errexit, xtrace, ...); may be repeated")
	flag.Var(&optsOff, "O", "enable a shopt option; may be repeated")
}

// splitCombinedShortFlags rewrites bash-style short / combined
// short flags into the long-form names our `flag` parser knows.
// `-ce 'cmd'` becomes `-o errexit -c 'cmd'`, `-eu` becomes
// `-o errexit -o nounset`, and so on. `-c` is value-taking so the
// emitted `-c` goes last in the cluster, just before its argument.
// Unknown clusters pass through untouched.
func splitCombinedShortFlags(args []string) []string {
	// Map of bash short-flag letters to their long set-option name.
	shortToOpt := map[byte]string{
		'a': "allexport",
		'e': "errexit",
		'f': "noglob",
		'n': "noexec",
		'u': "nounset",
		'v': "verbose",
		'x': "xtrace",
		'p': "privileged",
	}
	out := make([]string, 0, len(args))
	out = append(out, args[0])
	for i := 1; i < len(args); i++ {
		a := args[i]
		if len(a) <= 2 || a[0] != '-' || a[1] == '-' {
			out = append(out, a)
			continue
		}
		allKnown := true
		for j := 1; j < len(a); j++ {
			if _, ok := shortToOpt[a[j]]; !ok && a[j] != 'c' {
				allKnown = false
				break
			}
		}
		if !allKnown {
			out = append(out, a)
			continue
		}
		var bools, vals []byte
		for j := 1; j < len(a); j++ {
			if a[j] == 'c' {
				vals = append(vals, a[j])
			} else {
				bools = append(bools, a[j])
			}
		}
		for _, c := range bools {
			out = append(out, "-o", shortToOpt[c])
		}
		for _, c := range vals {
			out = append(out, "-"+string(c))
		}
	}
	return out
}

func main() {
	// bash accepts POSIX-style combined short flags (`-ce 'cmd'`,
	// `-eu`, etc.). Go's flag package doesn't, so pre-split any
	// bare `-XYZ` argument (where every char is a single-letter
	// flag we know about) into individual `-X -Y -Z` args.
	os.Args = splitCombinedShortFlags(os.Args)
	flag.Parse()
	if *version {
		fmt.Printf("GNU bash, version %s\n", bashVersion)
		return
	}
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

func newRunner() (*interp.Runner, error) {
	// Increment SHLVL from parent environment.
	shlvl := 0
	if s := os.Getenv("SHLVL"); s != "" {
		fmt.Sscanf(s, "%d", &shlvl)
	}
	shlvl++

	envVars := append(os.Environ(), bashVersionVars()...)
	envVars = append(envVars, fmt.Sprintf("SHLVL=%d", shlvl))

	env := expand.ListEnviron(envVars...)
	var r *interp.Runner
	var err error
	// bash defaults to expanding aliases ONLY in interactive shells.
	// For `-c CMD` and script invocations the user must `shopt -s
	// expand_aliases` explicitly. Treat a tty-attached stdin as
	// interactive; otherwise leave alias expansion off, matching
	// bash's startup behaviour.
	interactive := *command == "" && flag.NArg() == 0 && term.IsTerminal(int(os.Stdin.Fd()))
	opts := []interp.RunnerOption{
		interp.Interactive(interactive),
		interp.StdIO(os.Stdin, os.Stdout, os.Stderr),
		interp.Env(env),
		interp.WithBashCompatErrors(true),
		interp.PromptExpand(func(s string) string {
			envGet := func(name string) string {
				return r.Env.Get(name).String()
			}
			return expandPrompt(s, envGet, 0, 0)
		}),
	}
	// Reuse interp.Params to apply set-options requested on the
	// command line. `bashy -o posix -o errexit` arrives here as
	// optsOn=["posix","errexit"]; `+O foo` would land in optsOff
	// once we accept the `+` prefix at flag-parse time.
	if setArgs := collectSetArgs(); len(setArgs) > 0 {
		opts = append(opts, interp.Params(setArgs...))
	}
	if *posix {
		opts = append(opts, interp.Params("-o", "posix"))
	}
	r, err = interp.New(opts...)
	if err != nil {
		return nil, err
	}
	// Bash imports exported functions from environment variables of
	// the form `BASH_FUNC_<name>%%=() { body; }`. Parse each one and
	// register it as a shell function so child invocations see the
	// caller's exported functions.
	importBashFuncs(r)
	return r, nil
}

// importBashFuncs scans os.Environ() for entries matching
// `BASH_FUNC_<name>%%=() { … }` and registers each one as a shell
// function in r.Funcs. Silently ignores any that don't parse.
func importBashFuncs(r *interp.Runner) {
	for _, e := range os.Environ() {
		name, value, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		rest, ok := strings.CutPrefix(name, "BASH_FUNC_")
		if !ok {
			continue
		}
		funcName, ok := strings.CutSuffix(rest, "%%")
		if !ok || funcName == "" {
			continue
		}
		// The value is `() { body; }`. Synthesize a function
		// definition by prepending the name.
		src := funcName + " " + value
		file, err := syntax.NewParser().Parse(strings.NewReader(src), "")
		if err != nil || len(file.Stmts) != 1 {
			continue
		}
		fn, ok := file.Stmts[0].Cmd.(*syntax.FuncDecl)
		if !ok {
			continue
		}
		if r.Funcs == nil {
			r.Funcs = make(map[string]*syntax.Stmt)
		}
		r.Funcs[funcName] = fn.Body
	}
}

// collectSetArgs converts the -o / -O flags collected on the command
// line into the argv form that interp.Params understands.
func collectSetArgs() []string {
	var out []string
	for _, name := range optsOn {
		out = append(out, "-o", name)
	}
	for _, name := range optsOff {
		out = append(out, "+o", name)
	}
	return out
}

// isLoginShell returns true if bashy was invoked as a login shell.
func isLoginShell() bool {
	if *login {
		return true
	}
	// Login shell if argv[0] starts with '-'
	return len(os.Args) > 0 && strings.HasPrefix(os.Args[0], "-")
}

// sourceIfExists sources a file if it exists, ignoring errors.
func sourceIfExists(r *interp.Runner, path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	f.Close()
	run(r, nil, path) // use runPath logic
	runPath(r, path)
}

// loadStartupFiles sources the appropriate startup files.
func loadStartupFiles(r *interp.Runner, interactive bool) {
	home, _ := os.UserHomeDir()

	if isLoginShell() {
		if !*noprofile {
			sourceIfExists(r, "/etc/profile")
			// Source first of: ~/.bash_profile, ~/.bash_login, ~/.profile
			for _, name := range []string{".bash_profile", ".bash_login", ".profile"} {
				path := filepath.Join(home, name)
				if _, err := os.Stat(path); err == nil {
					sourceIfExists(r, path)
					break
				}
			}
		}
	} else if interactive {
		if !*norc && home != "" {
			// Try ~/.bashyrc first, fall back to ~/.bashrc
			rc := filepath.Join(home, ".bashyrc")
			if _, err := os.Stat(rc); err != nil {
				rc = filepath.Join(home, ".bashrc")
			}
			sourceIfExists(r, rc)
		}
	} else {
		// Non-interactive: source $BASH_ENV
		if bashEnv := os.Getenv("BASH_ENV"); bashEnv != "" {
			sourceIfExists(r, bashEnv)
		}
	}
}

func runAll() error {
	if *command != "" {
		// BASH_EXECUTION_STRING holds the literal -c argument, per
		// bash. Set on the process env BEFORE constructing the
		// runner so its captured env includes the value.
		os.Setenv("BASH_EXECUTION_STRING", *command)
	}
	r, err := newRunner()
	if err != nil {
		return err
	}

	if *command != "" {
		// Bash 5.3 syntax: `bash -c COMMAND [argv0 [arg1 arg2 …]]`.
		// The first positional after the -c command sets $0 (used
		// for parse-error prefixes and as the script name within the
		// runner). The rest become $1, $2, … . The command body
		// itself stays in *command.
		argv0 := ""
		var posArgs []string
		if rest := flag.Args(); len(rest) > 0 {
			argv0 = rest[0]
			posArgs = rest[1:]
		}
		if len(posArgs) > 0 {
			// Reach the Params option side-effect for free.
			interp.Params(append([]string{"--"}, posArgs...)...)(r)
		}
		loadStartupFiles(r, false)
		return run(r, strings.NewReader(*command), argv0)
	}
	if flag.NArg() == 0 {
		if term.IsTerminal(int(os.Stdin.Fd())) {
			loadStartupFiles(r, true)
			return runInteractive(r, os.Stdin, os.Stdout, os.Stderr)
		}
		loadStartupFiles(r, false)
		return run(r, os.Stdin, "")
	}
	loadStartupFiles(r, false)
	for _, path := range flag.Args() {
		if err := runPath(r, path); err != nil {
			return err
		}
	}
	return nil
}

func run(r *interp.Runner, reader io.Reader, name string) error {
	if reader == nil {
		return nil
	}
	lang := syntax.LangBash
	if *posix {
		lang = syntax.LangPOSIX
	}
	// Buffer the source so we can echo the offending line back to stderr
	// in bash's `<file>: line N: \`<line>'` format when parsing fails.
	src, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	// Bash 5.3's `<file>: line N: …` prefix shape, with `: -c`
	// inserted when running via `-c`. argv0 (the first positional
	// after the -c command) is the file-name in -c mode; otherwise
	// it's the actual script path.
	errPrefix := name
	if errPrefix == "" {
		errPrefix = "bashy"
	}
	if *command != "" {
		errPrefix += ": -c"
	}
	// Bash 5.3 treats `<<EOF\n...` running off the end of the file as a
	// warning (not an error) and uses whatever was read up to EOF as
	// the body. Wire that behaviour through the parser so the
	// affected tests (comsub-eof, exportfunc, …) behave like bash.
	hdocWarn := func(startLine, eofLine int, stop string) {
		fmt.Fprintf(os.Stderr,
			"%s: line %d: warning: here-document at line %d delimited by end-of-file (wanted `%s')\n",
			errPrefix, eofLine, startLine, stop)
	}
	prog, err := syntax.NewParser(syntax.Variant(lang), syntax.HeredocEOFWarning(hdocWarn)).Parse(bytes.NewReader(src), name)
	if err != nil {
		var pe syntax.ParseError
		if errors.As(err, &pe) {
			printBashParseError(os.Stderr, src, errPrefix, pe)
			return interp.ExitStatus(2)
		}
		return err
	}
	r.Reset()
	ctx := context.Background()
	return r.Run(ctx, prog)
}

// printBashParseError emits a syntax.ParseError in the same shape bash
// 5.3 uses: a `<prefix>: line N: <text>` line, followed by a second
// `<prefix>: line N: \`<offending source line>'` echo. The prefix is
// `<file>` for a parsed script and `bashy: -c` for the -c form.
func printBashParseError(w io.Writer, src []byte, prefix string, pe syntax.ParseError) {
	line := int(pe.Pos.Line())
	text := rewriteParserErrorText(string(src), pe)
	fmt.Fprintf(w, "%s: line %d: %s\n", prefix, line, text)
	// Bash omits the trailing source-line echo for "unexpected EOF"
	// diagnostics (the matching-`X' messages already point at the
	// unclosed construct).
	if strings.HasPrefix(text, "unexpected EOF") {
		return
	}
	if srcLine := nthLine(src, line); srcLine != "" {
		fmt.Fprintf(w, "%s: line %d: `%s'\n", prefix, line, srcLine)
	}
}

// rewriteParserErrorText rewrites mvdan/sh's parser error messages
// into bash 5.3's canonical wording when the pattern is recognisable.
// Falls back to the original text otherwise.
func rewriteParserErrorText(src string, pe syntax.ParseError) string {
	// Bash escalates a partial-arithmetic parse to "missing `))`"
	// instead of naming the inner token — match that for `((` blocks
	// before any of the per-message rewrites below.
	if insideUnclosedArith(src, pe.Pos) {
		return "unexpected EOF while looking for matching `)'"
	}
	switch {
	case pe.Text == "statements must be separated by &, ; or a newline",
		strings.Contains(pe.Text, "must be followed by"),
		strings.Contains(pe.Text, "must follow a name"):
		// For `case`/`for`/`select` follow-errors the parser anchors the
		// position at the keyword itself ("`case x` must be followed by
		// `in`") but bash reports the actually-offending token (the one
		// it found instead). Skip over the construct's preamble to find
		// that token in the source.
		skipWords := tokensToSkip(pe.Text)
		if tok := offendingTokenAfter(src, pe.Pos, skipWords); tok != "" {
			return fmt.Sprintf("syntax error near unexpected token `%s'", tok)
		}
		if tok := offendingTokenAt(src, pe.Pos); tok != "" {
			return fmt.Sprintf("syntax error near unexpected token `%s'", tok)
		}
	case strings.HasPrefix(pe.Text, "reached EOF without matching"):
		// Map our `${`/`$(`/`{` matching-error wording to bash's.
		if strings.Contains(pe.Text, "`$(`") || strings.Contains(pe.Text, "`(`") {
			return "unexpected EOF while looking for matching `)'"
		}
		if strings.Contains(pe.Text, "`${`") || strings.Contains(pe.Text, "`{`") {
			return "unexpected EOF while looking for matching `}'"
		}
	case pe.Text == "unclosed quote":
		return "unexpected EOF while looking for matching `\"'"
	}
	return pe.Text
}

// insideUnclosedArith reports whether pos sits inside an `(( ... ))`
// arithmetic command whose matching `))` is missing in the source up
// to that point.
func insideUnclosedArith(src string, pos syntax.Pos) bool {
	col := int(pos.Col())
	line := int(pos.Line())
	if line <= 0 || col <= 0 {
		return false
	}
	curLine := 1
	end := 0
	for ; end < len(src) && curLine < line; end++ {
		if src[end] == '\n' {
			curLine++
		}
	}
	end += col - 1
	if end > len(src) {
		end = len(src)
	}
	prefix := src[:end]
	// Count `((` and `))` occurrences before pos. If `((` > `))` we
	// are inside an unclosed arith block. This is conservative — it
	// ignores `((` inside strings/comments — but good enough for the
	// bashy CLI's error-message remap.
	open := strings.Count(prefix, "((")
	close := strings.Count(prefix, "))")
	return open > close
}

// tokensToSkip returns how many words must be skipped past pe.Pos in
// the source to land on the token bash would name as the offender.
// For `case x must be followed by in` we must skip 2 words (`case`
// and the subject); for `for must be followed by a literal` only 1
// (`for`); etc. Returns 0 when no special skipping is needed.
func tokensToSkip(text string) int {
	switch {
	case strings.HasPrefix(text, "`case ") && strings.Contains(text, "must be followed by `in`"):
		return 2
	case strings.HasPrefix(text, "`case` must be followed by"):
		return 1
	case strings.HasPrefix(text, "`for` must be followed by"),
		strings.HasPrefix(text, "`select` must be followed by"):
		return 1
	case strings.Contains(text, "` must be followed by `in`, `do`, `;`, or a newline"):
		// `for foo` / `select foo` -- skip kw + name.
		return 2
	}
	return 0
}

// offendingTokenAfter advances `skip` whitespace-delimited words past
// pos in src and returns the next token starting after the last skip,
// in the same shape as offendingTokenAt. Used to find bash's notion of
// the offender when the parser anchored its position at the start of
// the construct (`case x`, `for`, `for foo`, …).
func offendingTokenAfter(src string, pos syntax.Pos, skip int) string {
	if skip <= 0 {
		return ""
	}
	col := int(pos.Col())
	line := int(pos.Line())
	if line <= 0 || col <= 0 {
		return ""
	}
	curLine := 1
	i := 0
	for ; i < len(src) && curLine < line; i++ {
		if src[i] == '\n' {
			curLine++
		}
	}
	i += col - 1
	if i >= len(src) {
		return ""
	}
	skipWord := func() {
		// skip leading whitespace
		for ; i < len(src); i++ {
			c := src[i]
			if c != ' ' && c != '\t' {
				break
			}
		}
		// consume one bash-style word/operator
		if i >= len(src) {
			return
		}
		switch src[i] {
		case ')', '(', '|', '&', ';', '<', '>', '`':
			i++
			return
		}
		for ; i < len(src); i++ {
			c := src[i]
			if c == ' ' || c == '\t' || c == '\n' || c == ';' || c == '&' || c == '|' || c == '<' || c == '>' || c == '(' || c == ')' || c == '`' {
				break
			}
		}
	}
	for n := 0; n < skip; n++ {
		skipWord()
	}
	// skip whitespace before the offender
	for ; i < len(src); i++ {
		c := src[i]
		if c != ' ' && c != '\t' {
			break
		}
	}
	if i >= len(src) {
		return ""
	}
	switch src[i] {
	case ')', '(', '|', '&', ';', '<', '>', '`':
		return string(src[i])
	}
	start := i
	for ; i < len(src); i++ {
		c := src[i]
		if c == ' ' || c == '\t' || c == '\n' || c == ';' || c == '&' || c == '|' || c == '<' || c == '>' || c == '(' || c == ')' || c == '`' {
			break
		}
	}
	return src[start:i]
}

// offendingTokenAt extracts a single bash-style token (operator or
// word) starting at the given position in src. Used by the parser-
// error rewriter to fill in `… unexpected token \`X' …`.
func offendingTokenAt(src string, pos syntax.Pos) string {
	col := int(pos.Col())
	line := int(pos.Line())
	if line <= 0 || col <= 0 {
		return ""
	}
	curLine := 1
	i := 0
	for ; i < len(src) && curLine < line; i++ {
		if src[i] == '\n' {
			curLine++
		}
	}
	i += col - 1
	if i >= len(src) {
		return ""
	}
	switch src[i] {
	case ')', '(', '|', '&', ';', '<', '>', '`':
		return string(src[i])
	}
	start := i
	for ; i < len(src); i++ {
		c := src[i]
		if c == ' ' || c == '\t' || c == '\n' || c == ';' || c == '&' || c == '|' || c == '<' || c == '>' || c == '(' || c == ')' || c == '`' {
			break
		}
	}
	return src[start:i]
}

// nthLine returns the 1-indexed line `n` of src with the trailing
// newline stripped, or "" when n is out of range.
func nthLine(src []byte, n int) string {
	if n <= 0 {
		return ""
	}
	cur := 1
	start := 0
	for i := range len(src) {
		if src[i] == '\n' {
			if cur == n {
				return string(src[start:i])
			}
			cur++
			start = i + 1
		}
	}
	if cur == n {
		return string(src[start:])
	}
	return ""
}

func runPath(r *interp.Runner, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return run(r, f, path)
}
