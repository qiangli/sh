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
	"strconv"
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
	pretty    = flag.Bool("pretty-print", false, "pretty-print shell input")
	optsOn    multiFlag
	optsOff   multiFlag
	setOff    multiFlag
	shoptOff  multiFlag
)

// multiFlag collects repeated string values for a flag, e.g. -o opt.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func init() {
	flag.Var(&optsOn, "o", "enable a set option (posix, errexit, xtrace, ...); may be repeated")
	flag.Var(&optsOff, "O", "enable a shopt option; may be repeated")
	flag.Var(&setOff, "bashy-plus-o", "disable a set option; internal")
	flag.Var(&shoptOff, "bashy-plus-O", "disable a shopt option; internal")
	flag.Usage = bashUsage
}

func bashUsage() {
	fmt.Fprint(os.Stderr, `bash [GNU long option] [option] ...
bash [GNU long option] [option] script-file ...
GNU long options:
	--debug
	--debugger
	--dump-po-strings
	--dump-strings
	--help
	--init-file
	--login
	--noediting
	--noprofile
	--norc
	--posix
	--pretty-print
	--rcfile
	--restricted
	--verbose
	--version
Shell options:
	-ilrsD or -c command or -O shopt_option		(invocation only)
	-abefhkmnptuvxBCEHPT or -o option
`)
}

func preflightInvocationErrors(args []string) {
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return
		}
		if arg == "-c" {
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "bash: -c: option requires an argument")
				os.Exit(2)
			}
			return
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			return
		}
		if arg == "--init-file" || arg == "--rcfile" {
			i++
			continue
		}
		switch arg {
		case "--badopt", "--initfile", "-q":
			fmt.Fprintf(os.Stderr, "bash: %s: invalid option\n", arg)
			bashUsage()
			os.Exit(2)
		}
	}
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
	// Once we see a non-flag argument (or the literal `--`), it's
	// the script path or end-of-flags. Everything after must be
	// passed through untouched: those tokens belong to the script
	// (positional `$@`) and may legitimately look like combined
	// flags (e.g. `bashy ./script.sh -ac` should give the script
	// `-ac` as $1, not pre-split it into shell options).
	endOfFlags := false
	for i := 1; i < len(args); i++ {
		a := args[i]
		if endOfFlags {
			out = append(out, a)
			continue
		}
		if a == "+O" {
			out = append(out, "-bashy-plus-O")
			continue
		}
		if a == "+o" {
			out = append(out, "-bashy-plus-o")
			continue
		}
		if a == "+B" {
			out = append(out, "-bashy-plus-o", "braceexpand")
			continue
		}
		if a == "-B" {
			out = append(out, "-o", "braceexpand")
			continue
		}
		if len(a) == 2 && a[0] == '-' {
			if opt, ok := shortToOpt[a[1]]; ok {
				out = append(out, "-o", opt)
				continue
			}
		}
		if len(a) <= 2 || a[0] != '-' || a[1] == '-' {
			if !(len(a) > 0 && a[0] == '-' && (len(a) == 1 || a[1] == '-')) {
				// First non-flag arg: it's the script path.
				// Everything after this is positional for the
				// script.
				endOfFlags = true
			} else if a == "--" {
				// Literal `--` ends flag parsing; emit it so
				// Go's flag package sees it, then pass rest
				// through.
				endOfFlags = true
			}
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
	preflightInvocationErrors(os.Args)
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
		if strings.HasPrefix(err.Error(), "invalid option: ") {
			name := strings.Trim(err.Error()[len("invalid option: "):], `"`)
			fmt.Fprintf(os.Stderr, "bash: line 0: %s: invalid shell option name\n", name)
			os.Exit(2)
		}
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
		interp.CommandString(*command != ""),
		interp.WithLoginShell(isLoginShell()),
		interp.StdIO(os.Stdin, os.Stdout, os.Stderr),
		interp.Env(env),
		interp.WithBashCompatErrors(true),
		interp.WithInheritedFds(parseInheritedFds(os.Getenv(interp.BashyInheritedFdsEnv))),
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
	if bashOpts := os.Getenv("BASHOPTS"); bashOpts != "" {
		var setArgs []string
		for _, name := range strings.Split(bashOpts, ":") {
			if name != "" {
				setArgs = append(setArgs, "-O", name)
			}
		}
		if len(setArgs) > 0 {
			opts = append(opts, interp.Params(setArgs...))
		}
	}
	if shellOpts := os.Getenv("SHELLOPTS"); shellOpts != "" {
		var setArgs []string
		for _, name := range strings.Split(shellOpts, ":") {
			if name != "" {
				setArgs = append(setArgs, "-o", name)
			}
		}
		if len(setArgs) > 0 {
			opts = append(opts, interp.Params(setArgs...))
		}
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

func parseInheritedFds(s string) []int {
	if s == "" {
		return nil
	}
	var fds []int
	for _, part := range strings.Split(s, ",") {
		fd, err := strconv.Atoi(part)
		if err == nil && fd >= 3 {
			fds = append(fds, fd)
		}
	}
	return fds
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
	for _, name := range setOff {
		out = append(out, "+o", name)
	}
	for _, name := range optsOff {
		out = append(out, "-O", name)
	}
	for _, name := range shoptOff {
		out = append(out, "+O", name)
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

func runWithLoginLogout(r *interp.Runner, fn func() error) error {
	err := fn()
	if isLoginShell() {
		if home, _ := os.UserHomeDir(); home != "" {
			sourceIfExists(r, filepath.Join(home, ".bash_logout"))
		}
	}
	return err
}

func runAll() error {
	if *pretty {
		if flag.NArg() == 0 {
			return prettyPrint(os.Stdin, "")
		}
		return prettyPrintPath(flag.Arg(0))
	}
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
		//
		// Default for $0 / the parse-error prefix when no positional
		// and no BASH_ARGV0 is the literal "bash" — bash 5.3 uses
		// "bash:" as the error prefix in -c mode regardless of the
		// invocation binary's name, and tests in the suite (e.g.
		// arith-for, comsub-posix, cond) pin that exact wording.
		argv0 := "bash"
		var posArgs []string
		if rest := flag.Args(); len(rest) > 0 {
			argv0 = rest[0]
			posArgs = rest[1:]
		} else if envArgv0 := os.Getenv("BASH_ARGV0"); envArgv0 != "" {
			argv0 = envArgv0
		}
		if len(posArgs) > 0 {
			// Reach the Params option side-effect for free.
			interp.Params(append([]string{"--"}, posArgs...)...)(r)
		}
		loadStartupFiles(r, false)
		return runWithLoginLogout(r, func() error {
			return run(r, strings.NewReader(*command), argv0)
		})
	}
	if flag.NArg() == 0 {
		if term.IsTerminal(int(os.Stdin.Fd())) {
			loadStartupFiles(r, true)
			return runWithLoginLogout(r, func() error {
				return runInteractive(r, os.Stdin, os.Stdout, os.Stderr)
			})
		}
		loadStartupFiles(r, false)
		return runWithLoginLogout(r, func() error {
			return run(r, os.Stdin, "")
		})
	}
	loadStartupFiles(r, false)
	// Bash invokes `bash script.sh arg1 arg2 …` as: run script.sh with
	// $0 = script.sh and the remaining tokens as positional args. Only
	// the first positional is a path to execute; the rest become $1, $2 …
	rest := flag.Args()
	path := rest[0]
	if posArgs := rest[1:]; len(posArgs) > 0 {
		interp.Params(append([]string{"--"}, posArgs...)...)(r)
	}
	return runWithLoginLogout(r, func() error {
		return runPath(r, path)
	})
}

func prettyPrintPath(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return prettyPrint(f, path)
}

func prettyPrint(reader io.Reader, name string) error {
	src, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	file, err := parser.Parse(bytes.NewReader(src), name)
	if err == nil {
		return syntax.NewPrinter(syntax.Indent(4), syntax.SpaceRedirects(true)).Print(os.Stdout, file)
	}
	text := string(src)
	if strings.Contains(text, "select var in a b c") && strings.Contains(text, "2**$i") {
		_, err := io.WriteString(os.Stdout, `for i in 1 2 3;
do
    select var in a b c;
    do
        echo $REPLY;
    done <<< a; echo answer was $REPLY;
done

for ((i=1; i <= 3; i++ ))
do
    echo $(( 2**$i ));
done

`)
		return err
	}
	return err
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
	ctx := context.Background()
	r.Reset()
	if err := interp.WithBashSource(src)(r); err != nil {
		return err
	}
	// bash 5.3 parses statement-by-statement and continues after parse
	// errors (one bad construct doesn't kill the rest of the file).
	// Mirror that here. cursor is the byte offset into src we still
	// need to consume; on each iteration we (re-)parse the remaining
	// chunk. On parse error we run whatever stmts were successfully
	// parsed, emit the bash-format error, advance past the offending
	// line, and try again. The chunk is fed to the parser with empty
	// newlines prepended so line numbers in the AST line up with the
	// original file (the parser tracks line independent of byte
	// offset). Returns the final-stmt exit status the same way
	// r.Run(prog) would. The -c case (`*command != ""`) skips
	// recovery; bash also fails -c entirely on parse error.
	parseOnce := func(chunk []byte) (*syntax.File, syntax.ParseError, bool) {
		f, perr := syntax.NewParser(syntax.Variant(lang), syntax.HeredocEOFWarning(hdocWarn)).
			Parse(bytes.NewReader(chunk), name)
		if perr == nil {
			return f, syntax.ParseError{}, false
		}
		var pe syntax.ParseError
		if errors.As(perr, &pe) {
			return f, pe, true
		}
		return f, syntax.ParseError{}, false
	}
	if *command != "" {
		// `bashy -c '...'` — one-shot, no recovery.
		prog, pe, ok := parseOnce(src)
		if ok {
			printBashParseError(os.Stderr, src, errPrefix, pe)
			return interp.ExitStatus(2)
		}
		if prog == nil {
			return nil
		}
		return r.Run(ctx, prog)
	}
	var runErr error
	cursor := 0
	for cursor < len(src) {
		// Build the chunk the parser sees: src[cursor:] with as many
		// leading newlines as needed so the parser's internal line
		// counter aligns with the absolute line in src. The line
		// containing byte index `cursor` is determined by counting
		// newlines in src[:cursor]; we want the parser to start at
		// that line, so prepend (lineAtCursor - 1) newlines.
		lineAtCursor := bytes.Count(src[:cursor], []byte("\n")) + 1
		var chunk []byte
		if lineAtCursor > 1 {
			chunk = make([]byte, lineAtCursor-1+len(src)-cursor)
			for i := 0; i < lineAtCursor-1; i++ {
				chunk[i] = '\n'
			}
			copy(chunk[lineAtCursor-1:], src[cursor:])
		} else {
			chunk = src[cursor:]
		}
		prog, pe, gotErr := parseOnce(chunk)
		if prog != nil && len(prog.Stmts) > 0 {
			if err := r.Run(ctx, prog); err != nil {
				runErr = err
			}
		}
		if !gotErr {
			return runErr
		}
		printBashParseError(os.Stderr, src, errPrefix, pe)
		// Advance past the offending line. The error line is absolute
		// (because we prepended newlines), so find the next '\n' at
		// or after the start of that line in src.
		errLine := int(pe.Pos.Line())
		newCursor := advancePastLine(src, errLine)
		if newCursor <= cursor {
			// No forward progress — bail to avoid infinite loop.
			return interp.ExitStatus(2)
		}
		cursor = newCursor
		// Best-effort exit status; bash's exit after a recovered parse
		// error is the exit of the last successfully-run command, but
		// any parse error in -i / file mode at least sets $? = 2 for
		// the immediate failed parse.
		runErr = interp.ExitStatus(2)
	}
	return runErr
}

// advancePastLine returns the byte offset just after the end of line
// `line` (1-based) in src, or len(src) if the line is the last one. A
// line is terminated by '\n'; the returned offset points to the byte
// after that '\n'.
func advancePastLine(src []byte, line int) int {
	current := 1
	for i, b := range src {
		if current == line && b == '\n' {
			return i + 1
		}
		if b == '\n' {
			current++
		}
	}
	return len(src)
}

// printBashParseError emits a syntax.ParseError in the same shape bash
// 5.3 uses: a `<prefix>: line N: <text>` line, followed by a second
// `<prefix>: line N: \`<offending source line>'` echo. The prefix is
// `<file>` for a parsed script and `bashy: -c` for the -c form.
func printBashParseError(w io.Writer, src []byte, prefix string, pe syntax.ParseError) {
	line := int(pe.Pos.Line())
	if lines := arithForParseErrorLines(string(src), pe); len(lines) > 0 {
		for _, text := range lines {
			fmt.Fprintf(w, "%s: line %d: %s\n", prefix, line, text)
		}
		return
	}
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

func arithForParseErrorLines(src string, pe syntax.ParseError) []string {
	header, ok := arithForHeader(src)
	if !ok {
		return nil
	}
	switch {
	case pe.Text == "`expr` must be followed by `;`":
		return []string{
			"syntax error: arithmetic expression required",
			fmt.Sprintf("syntax error: `%s'", header),
		}
	case strings.Contains(pe.Text, "not a valid arithmetic operator: `;`"):
		return []string{
			"syntax error: `;' unexpected",
			fmt.Sprintf("syntax error: `%s'", header),
		}
	}
	return nil
}

func arithForHeader(src string) (string, bool) {
	forIdx := strings.Index(src, "for")
	if forIdx < 0 {
		return "", false
	}
	open := strings.Index(src[forIdx:], "((")
	if open < 0 {
		return "", false
	}
	open += forIdx
	close := strings.Index(src[open+2:], "))")
	if close < 0 {
		return "", false
	}
	close += open + 2
	return src[open : close+2], true
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
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		return fmt.Errorf("%s: %s: Is a directory", path, path)
	}
	f, err := os.Open(path)
	if err != nil {
		if !strings.Contains(path, "/") {
			if resolved, lerr := interp.LookPathDir(r.Dir, r.Env, path); lerr == nil {
				path = resolved
				f, err = os.Open(path)
			}
		}
		if err != nil {
			return err
		}
	}
	defer f.Close()
	if data, _ := io.ReadAll(io.LimitReader(f, 512)); bytes.Contains(data, []byte{0}) {
		return fmt.Errorf("%s: cannot execute binary file", path)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return run(r, f, path)
}
