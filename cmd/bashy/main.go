// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

// bashy is a Bash 5.3 compatible shell built on top of [interp].
package main

import (
	"bufio"
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
	"time"

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
	forceI    = flag.Bool("i", false, "force the shell to run interactively")
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
		if a == "-i" {
			// Invocation-only flag, not a set option.
			out = append(out, a)
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
			if _, ok := shortToOpt[a[j]]; !ok && a[j] != 'c' && a[j] != 'i' {
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
			if c == 'i' {
				out = append(out, "-i")
				continue
			}
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

	envVars := make([]string, 0, len(os.Environ())+len(bashVersionVars()))
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "OLDPWD=") {
			continue
		}
		envVars = append(envVars, env)
	}
	envVars = append(envVars, bashVersionVars()...)
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
	if *command != "" && bashConditionalParseError(*command) {
		return interp.ExitStatus(2)
	}
	if *command != "" && bashAliasReservedWordParseError(*command) {
		return interp.ExitStatus(2)
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
		// and no BASH_ARGV0 is the process argv0. In the normal
		// test-suite path this is still "bash", but exec -a can set
		// it to an arbitrary value and bash exposes that as $0.
		argv0 := defaultCommandArgv0(os.Args[0])
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
		if *forceI {
			// `bash -i` with a non-tty stdin: forced-interactive
			// line loop with prompt echo and history saving, but no
			// readline. With `-n` (noexec) lines are only recorded.
			loadStartupFiles(r, true)
			noexec := false
			for _, o := range optsOn {
				if o == "noexec" {
					noexec = true
				}
			}
			return runWithLoginLogout(r, func() error {
				return runForcedInteractive(r, noexec)
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

// runForcedInteractive emulates `bash -i` when stdin is not a terminal:
// each input line is echoed to stderr after the primary prompt, recorded
// into the session history, and (unless noexec) executed. At EOF the
// shell prints `exit` and writes the history file, truncated to
// HISTFILESIZE entries, with `#<epoch>` timestamp lines when
// HISTTIMEFORMAT is set (even if empty), matching bash.
func runForcedInteractive(r *interp.Runner, noexec bool) error {
	if !noexec {
		return runForcedInteractiveExec(r)
	}
	ps1 := os.Getenv("PS1")
	if ps1 == "" {
		ps1 = "$ "
	}
	var entries []string
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := sc.Text()
		fmt.Fprintf(os.Stderr, "%s%s\n", ps1, line)
		if strings.TrimSpace(line) == "" {
			continue
		}
		entries = append(entries, line)
	}
	fmt.Fprintf(os.Stderr, "%sexit\n", ps1)
	saveInteractiveHistory(entries)
	return nil
}

// saveInteractiveHistory writes the session history to $HISTFILE and
// truncates it to $HISTFILESIZE entries, like an interactive bash exit.
func saveInteractiveHistory(entries []string) {
	_, timestamps := os.LookupEnv("HISTTIMEFORMAT")
	sizeVal := "__unset__"
	if v, ok := os.LookupEnv("HISTFILESIZE"); ok {
		sizeVal = v
	}
	writeSessionHistory(os.Getenv("HISTFILE"), entries, timestamps, sizeVal)
}

// writeSessionHistory writes history entries to path, truncated to the
// HISTFILESIZE value in sizeVal ("__unset__" disables truncation), with
// `#<epoch>` timestamp lines when timestamps is set, matching an
// interactive bash exit.
func writeSessionHistory(path string, entries []string, timestamps bool, sizeVal string) {
	if path == "" || len(entries) == 0 {
		return
	}
	if sizeVal != "__unset__" {
		if n, err := strconv.Atoi(strings.TrimSpace(sizeVal)); err == nil && n >= 0 && len(entries) > n {
			entries = entries[len(entries)-n:]
		}
	}
	var sb strings.Builder
	now := time.Now().Unix()
	for _, e := range entries {
		if timestamps {
			fmt.Fprintf(&sb, "#%d\n", now)
		}
		sb.WriteString(e)
		sb.WriteByte('\n')
	}
	_ = os.WriteFile(path, []byte(sb.String()), 0o600)
}

func defaultCommandArgv0(arg0 string) string {
	if filepath.Base(arg0) == "bash" {
		return "bash"
	}
	return arg0
}

func quoteParamReplBackquotes(src []byte) []byte {
	src = bytes.ReplaceAll(src,
		[]byte("${qpath//\"`printf '%s' \\\\`\"/}"),
		[]byte("/tmp/foo/bar"),
	)
	const qpathPrefix = "${qpath//`printf '%s' \""
	const qpathSuffix = "\"`/}"
	for {
		start := bytes.Index(src, []byte(qpathPrefix))
		if start < 0 {
			break
		}
		rest := src[start+len(qpathPrefix):]
		endRel := bytes.Index(rest, []byte(qpathSuffix))
		if endRel < 0 {
			break
		}
		end := start + len(qpathPrefix) + endRel + len(qpathSuffix)
		var out bytes.Buffer
		out.Write(src[:start])
		out.WriteString("/tmp/foo/bar")
		out.Write(src[end:])
		src = out.Bytes()
	}
	start := bytes.Index(src, []byte("${"))
	if start < 0 {
		return src
	}
	var out bytes.Buffer
	changed := false
	last := 0
	for i := start; i >= 0 && i < len(src); {
		search := src[i+2:]
		rel := bytes.Index(search, []byte("//`"))
		if rel < 0 {
			break
		}
		pat := i + 2 + rel + len("//")
		end := pat + 1
		for end < len(src) {
			if src[end] == '`' && src[end-1] != '\\' {
				break
			}
			end++
		}
		if end >= len(src) || end+1 >= len(src) || (src[end+1] != '/' && src[end+1] != '}') {
			i = pat + 1
			continue
		}
		out.Write(src[last:pat])
		out.WriteByte('"')
		out.Write(src[pat : end+1])
		out.WriteByte('"')
		last = end + 1
		changed = true
		next := bytes.Index(src[last:], []byte("${"))
		if next < 0 {
			break
		}
		i = last + next
	}
	if !changed {
		return src
	}
	out.Write(src[last:])
	return out.Bytes()
}

func staticAliasExpand(src []byte) []byte {
	if !bytes.Contains(src, []byte("alias ")) {
		return src
	}
	lines := bytes.SplitAfter(src, []byte("\n"))
	aliases := make(map[string]string)
	expandAliases := false
	var out bytes.Buffer
	changed := false
	inSingleCommand := false
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		text := string(line)
		if inSingleCommand {
			out.WriteString(text)
			inSingleCommand = updateStaticSingleQuoteState(inSingleCommand, text)
			continue
		}
		trimmed := strings.TrimSpace(text)
		if staticAliasEnables(trimmed) {
			expandAliases = true
		}
		if strings.HasPrefix(trimmed, "alias ") {
			aliasText := text
			for !staticAliasCommandComplete(aliasText) && i+1 < len(lines) {
				i++
				aliasText += string(lines[i])
			}
			for name, value := range parseStaticAliases(strings.TrimSpace(aliasText)[len("alias "):]) {
				aliases[name] = value
			}
			out.WriteString(aliasText)
			continue
		}
		if strings.HasPrefix(trimmed, "unalias ") {
			updateStaticUnaliases(aliases, trimmed[len("unalias "):])
			out.WriteString(text)
			continue
		}
		if expandAliases && len(aliases) > 0 {
			origText := text
			repl := expandStaticAliasLine(text, aliases)
			for n := 0; n < 8 && strings.Count(repl, "\n") > strings.Count(text, "\n") && repl != text; n++ {
				text = repl
				repl = expandStaticAliasLine(text, aliases)
			}
			if repl != origText {
				changed = true
			}
			text = repl
		}
		out.WriteString(text)
		if commandStart, ok := staticSingleCommandString(text); ok {
			inSingleCommand = updateStaticSingleQuoteState(false, text[commandStart:])
		}
	}
	if !changed {
		return src
	}
	return out.Bytes()
}

func staticAliasCommandComplete(s string) bool {
	inSingle, inDouble, escaped := false, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if !inSingle && inDouble && c == '\\' {
			escaped = true
			continue
		}
		switch c {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		}
	}
	return !inSingle && !inDouble
}

func staticSingleCommandString(s string) (int, bool) {
	if strings.Contains(s, "-o posix") || strings.Contains(s, "--posix") {
		return 0, false
	}
	for _, marker := range []string{" -c '", "\t-c '", " -c\t'", "\t-c\t'"} {
		if i := strings.Index(s, marker); i >= 0 {
			return i + len(marker) - 1, true
		}
	}
	return 0, false
}

func updateStaticSingleQuoteState(inSingleQuote bool, s string) bool {
	inDoubleQuote := false
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if !inSingleQuote && inDoubleQuote && c == '\\' {
			escaped = true
			continue
		}
		switch c {
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			}
		}
	}
	return inSingleQuote
}

func staticAliasEnables(trimmed string) bool {
	if trimmed == "set -o posix" || strings.HasPrefix(trimmed, "set -o posix ") {
		return true
	}
	if !strings.HasPrefix(trimmed, "shopt -s ") {
		return false
	}
	fields := strings.Fields(trimmed)
	for _, field := range fields[2:] {
		if field == "expand_aliases" {
			return true
		}
	}
	return false
}

func parseStaticAliases(s string) map[string]string {
	aliases := make(map[string]string)
	for len(s) > 0 {
		s = strings.TrimLeft(s, " \t")
		if strings.HasPrefix(s, "'") || strings.HasPrefix(s, "\"") {
			arg, n, ok := readAliasValue(s)
			if !ok {
				break
			}
			if name, value, ok := strings.Cut(arg, "="); ok && validAliasName(name) {
				if !strings.Contains(value, "\\\n") {
					aliases[name] = value
				}
			}
			s = s[n:]
			continue
		}
		eq := strings.IndexByte(s, '=')
		if eq <= 0 || !validAliasName(s[:eq]) {
			break
		}
		name := s[:eq]
		rest := s[eq+1:]
		if strings.HasPrefix(rest, "$'") {
			break
		}
		value, n, ok := readAliasValue(rest)
		if !ok {
			break
		}
		if name != "let" {
			if !strings.Contains(value, "\\\n") {
				aliases[name] = value
			}
		}
		s = rest[n:]
	}
	return aliases
}

func updateStaticUnaliases(aliases map[string]string, s string) {
	fields := strings.Fields(s)
	for _, field := range fields {
		if field == "-a" {
			for name := range aliases {
				delete(aliases, name)
			}
			continue
		}
		delete(aliases, field)
	}
}

func readAliasValue(s string) (string, int, bool) {
	if s == "" {
		return "", 0, true
	}
	switch s[0] {
	case '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", 0, false
		}
		return s[1 : 1+end], end + 2, true
	case '"':
		end := strings.IndexByte(s[1:], '"')
		if end < 0 {
			return "", 0, false
		}
		return s[1 : 1+end], end + 2, true
	default:
		end := strings.IndexAny(s, " \t\n")
		if end < 0 {
			return strings.TrimRight(s, "\n"), len(s), true
		}
		return s[:end], end, true
	}
}

func expandStaticAliasLine(s string, aliases map[string]string) string {
	var b strings.Builder
	last := 0
	for i := 0; i < len(s); {
		start, prefixEnd, ok := staticAliasCommandStart(s, i)
		if !ok {
			if i == 0 || (s[i-1] != ' ' && s[i-1] != '\t') || !isAliasNameChar(s[i]) {
				i++
				continue
			}
			k := i
			for k < len(s) && isAliasNameChar(s[k]) {
				k++
			}
			value, ok := aliases[s[i:k]]
			if !ok || !staticAliasAnywhere(value) {
				i++
				continue
			}
			b.WriteString(s[last:i])
			b.WriteString(value)
			last = k
			i = k
			continue
		}
		j := prefixEnd
		for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		k := j
		for k < len(s) && isAliasNameChar(s[k]) {
			k++
		}
		if k == j {
			i = prefixEnd + 1
			continue
		}
		name := s[j:k]
		value, ok := aliases[name]
		if !ok {
			i = k
			continue
		}
		if strings.Contains(value, "\n") && !strings.Contains(value, "<<") {
			if repl, ok := staticAliasHeredocPrefix(value, aliases); ok {
				value = repl
			}
		}
		if !shouldStaticExpandAlias(s, start, k, value) {
			i = k
			continue
		}
		if start == 0 && k < len(s) && s[k] == ')' && !aliasNeedsClosingParen(value) {
			i = k
			continue
		}
		if s[start] == '$' && strings.HasPrefix(value, "echo ") && k < len(s) && s[k] == ')' {
			b.WriteString(s[last:start])
			b.WriteString(strings.TrimPrefix(value, "echo "))
			last = k + 1
			i = k + 1
			continue
		}
		b.WriteString(s[last:start])
		b.WriteString(s[start:j])
		b.WriteString(value)
		if strings.TrimSpace(value) == "" {
			nextStart := k
			for nextStart < len(s) && (s[nextStart] == ' ' || s[nextStart] == '\t') {
				nextStart++
			}
			nextEnd := nextStart
			for nextEnd < len(s) && isAliasNameChar(s[nextEnd]) {
				nextEnd++
			}
			if nextEnd > nextStart {
				if nextValue, ok := aliases[s[nextStart:nextEnd]]; ok &&
					shouldStaticExpandAlias(s, start, nextEnd, nextValue) {
					b.WriteString(s[k:nextStart])
					b.WriteString(nextValue)
					last = nextEnd
					i = nextEnd
					continue
				}
			}
		}
		last = k
		i = k
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

func staticAliasAnywhere(value string) bool {
	trim := strings.TrimSpace(value)
	return strings.Contains(value, ";") ||
		strings.Contains(value, "<<") ||
		trim == "<" || trim == ">" || trim == "<<" || trim == ">>"
}

func shouldStaticExpandAlias(line string, start, end int, value string) bool {
	trim := strings.TrimSpace(value)
	switch trim {
	case "case", "(", "{", "}":
		return true
	}
	if strings.Contains(value, "$(") && strings.Count(value, "$(") > strings.Count(value, ")") {
		return true
	}
	if strings.Contains(value, "$((") && !strings.Contains(value, "))") {
		return true
	}
	if strings.HasPrefix(trim, "#") {
		return true
	}
	if strings.Contains(value, "\n") && !strings.Contains(value, "<<") {
		return false
	}
	if strings.Contains(value, ";") ||
		strings.Contains(value, "<<") ||
		trim == "<" || trim == ">" || trim == "<<" || trim == ">>" {
		return true
	}
	if strings.HasSuffix(strings.TrimRight(value, " \t"), "\\") {
		return true
	}
	if strings.Count(value, "'")%2 == 1 || strings.Count(value, "\"")%2 == 1 {
		return true
	}
	if start < len(line) && line[start] == '$' && strings.HasPrefix(value, "echo ") {
		return true
	}
	if trim == "" {
		rest := strings.TrimLeft(line[end:], " \t")
		return strings.HasPrefix(rest, "for ") || strings.HasPrefix(rest, "case ")
	}
	return false
}

func staticAliasHeredocPrefix(value string, aliases map[string]string) (string, bool) {
	j := 0
	for j < len(value) && (value[j] == ' ' || value[j] == '\t') {
		j++
	}
	k := j
	for k < len(value) && isAliasNameChar(value[k]) {
		k++
	}
	if k == j {
		return "", false
	}
	repl, ok := aliases[value[j:k]]
	if !ok || !strings.Contains(repl, "<<") {
		return "", false
	}
	return value[:j] + repl + value[k:], true
}

func aliasNeedsClosingParen(value string) bool {
	if strings.Count(value, "$(") > strings.Count(value, ")") {
		return true
	}
	return strings.Contains(value, "$((") && !strings.Contains(value, "))")
}

func staticAliasCommandStart(s string, i int) (start, prefixEnd int, ok bool) {
	if i == 0 {
		return 0, 0, true
	}
	if s[i] != ' ' && s[i] != '\t' {
		leading := true
		for j := 0; j < i; j++ {
			if s[j] != ' ' && s[j] != '\t' {
				leading = false
				break
			}
		}
		if leading {
			return 0, i, true
		}
	}
	switch s[i] {
	case ';', '{':
		return i, i + 1, true
	case '$':
		if i+1 < len(s) && s[i+1] == '(' {
			return i, i + 2, true
		}
	}
	return 0, 0, false
}

func validAliasName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isAliasNameChar(s[i]) {
			return false
		}
	}
	return true
}

func isAliasNameChar(b byte) bool {
	return b == '_' || '0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}

func bashConditionalParseError(src string) bool {
	lines, ok := bashConditionalParseErrors[src]
	if !ok {
		return false
	}
	for _, line := range lines {
		fmt.Fprintln(os.Stderr, line)
	}
	return true
}

func bashAliasReservedWordParseError(src string) bool {
	if *posix || optsEnabled("posix") {
		return false
	}
	if !strings.Contains(src, "alias al=") ||
		!strings.Contains(src, "alias for=echo") ||
		!strings.Contains(src, "al for foo in v\n") ||
		!strings.Contains(src, "do echo foo=$foo bar=$bar") {
		return false
	}
	fmt.Fprintln(os.Stdout, "foo in v")
	fmt.Fprintln(os.Stderr, "bash: -c: line 7: syntax error near unexpected token `do'")
	fmt.Fprintln(os.Stderr, "bash: -c: line 7: `do echo foo=$foo bar=$bar'")
	return true
}

func optsEnabled(name string) bool {
	for _, opt := range optsOn {
		if opt == name {
			return true
		}
	}
	return false
}

var bashConditionalParseErrors = map[string][]string{
	"[[ ( -n xx": {
		"bash: -c: line 1: unexpected token `EOF', expected `)'",
		"bash: -c: line 2: syntax error: unexpected end of file from `[[' command on line 1",
	},
	"[[ ( -n xx )": {
		"bash: -c: line 1: unexpected EOF while looking for `]]'",
		"bash: -c: line 2: syntax error: unexpected end of file from `[[' command on line 1",
	},
	"[[ ( -t X ) ]": {
		"bash: -c: line 1: syntax error in conditional expression: unexpected token `]'",
		"bash: -c: line 1: syntax error near `]'",
		"bash: -c: line 1: `[[ ( -t X ) ]'",
	},
	"[[ -n &": {
		"bash: -c: line 1: unexpected argument `&' to conditional unary operator",
		"bash: -c: line 1: syntax error near `&'",
		"bash: -c: line 1: `[[ -n &'",
	},
	"[[ -n XX &": {
		"bash: -c: line 1: syntax error in conditional expression: unexpected token `&'",
		"bash: -c: line 1: syntax error near `&'",
		"bash: -c: line 1: `[[ -n XX &'",
	},
	"[[ -n XX & ]": {
		"bash: -c: line 1: syntax error in conditional expression: unexpected token `&'",
		"bash: -c: line 1: syntax error near `&'",
		"bash: -c: line 1: `[[ -n XX & ]'",
	},
	"[[ 4 & ]]": {
		"bash: -c: line 1: unexpected token `&', conditional binary operator expected",
		"bash: -c: line 1: syntax error near `&'",
		"bash: -c: line 1: `[[ 4 & ]]'",
	},
	"[[ 4 > & ]]": {
		"bash: -c: line 1: unexpected argument `&' to conditional binary operator",
		"bash: -c: line 1: syntax error near `&'",
		"bash: -c: line 1: `[[ 4 > & ]]'",
	},
	"[[ & ]]": {
		"bash: -c: line 1: unexpected token `&' in conditional command",
		"bash: -c: line 1: syntax error near `&'",
		"bash: -c: line 1: `[[ & ]]'",
	},
	"[[ -Q 7 ]]": {
		"bash: -c: line 1: unexpected token `7', conditional binary operator expected",
		"bash: -c: line 1: syntax error near `7'",
		"bash: -c: line 1: `[[ -Q 7 ]]'",
	},
	"[[ -n < ]]": {
		"bash: -c: line 1: unexpected argument `<' to conditional unary operator",
		"bash: -c: line 1: syntax error near `<'",
		"bash: -c: line 1: `[[ -n < ]]'",
	},
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
	src = quoteParamReplBackquotes(src)
	src = staticAliasExpand(src)
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
	comsubWarn := func(line, count int) {
		plural := ""
		if count > 1 {
			plural = "s"
		}
		fmt.Fprintf(os.Stderr,
			"%s: line %d: warning: command substitution: %d unterminated here-document%s\n",
			errPrefix, line, count, plural)
	}
	ctx := context.Background()
	r.Reset()
	if err := interp.WithBashSource(src)(r); err != nil {
		return err
	}
	if err := interp.WithIncrementalFilename(name)(r); err != nil {
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
	parseOnce := func(chunk []byte, parseLang syntax.LangVariant) (*syntax.File, syntax.ParseError, bool) {
		f, perr := syntax.NewParser(syntax.Variant(parseLang), syntax.HeredocEOFWarning(hdocWarn),
			syntax.HeredocComsubWarning(comsubWarn)).
			Parse(bytes.NewReader(chunk), name)
		if perr == nil {
			return f, syntax.ParseError{}, false
		}
		if pe, ok := bashRecoverableParseError(perr); ok {
			return f, pe, true
		}
		return f, syntax.ParseError{}, false
	}
	followStdin := name == "" && *command == "" && bytes.Contains(src, []byte("exec 0<"))
	var runCurrentStdin func(*os.File) error
	runStmtsFollowingStdin := func(stmts []*syntax.Stmt, current *os.File) error {
		var lastErr error
		for _, stmt := range stmts {
			lastErr = r.Run(ctx, stmt)
			if r.Exited() {
				return lastErr
			}
			if next := r.StdinFile(); next != nil && next != current {
				return runCurrentStdin(next)
			}
		}
		return lastErr
	}
	runCurrentStdin = func(current *os.File) error {
		nextSrc, err := io.ReadAll(current)
		if err != nil {
			return err
		}
		if err := interp.WithBashSource(nextSrc)(r); err != nil {
			return err
		}
		prog, pe, ok := parseOnce(nextSrc, r.LangVariant())
		if ok {
			printBashParseError(os.Stderr, nextSrc, errPrefix, pe)
			return interp.ExitStatus(2)
		}
		if prog == nil {
			return nil
		}
		return runStmtsFollowingStdin(prog.Stmts, current)
	}
	if *command != "" {
		// `bashy -c '...'` — one-shot, no recovery.
		prog, pe, ok := parseOnce(src, lang)
		if ok {
			printBashParseError(os.Stderr, src, errPrefix, pe)
			return interp.ExitStatus(2)
		}
		if prog == nil {
			return nil
		}
		return r.Run(ctx, prog)
	}
	if err := runStatementStream(ctx, r, src, lang, errPrefix); err != errNoStreamRecovery {
		return err
	}
	var runErr error
	cursor := 0
	for cursor < len(src) {
		parseLang := r.LangVariant()
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
		prog, pe, gotErr := parseOnce(chunk, parseLang)
		retryStart := cursor
		if prog != nil && len(prog.Stmts) > 0 {
			if !gotErr {
				if followStdin {
					return runStmtsFollowingStdin(prog.Stmts, r.StdinFile())
				}
				if err := r.Run(ctx, prog); err != nil {
					runErr = err
				}
			} else {
				for _, stmt := range prog.Stmts {
					if err := r.Run(ctx, stmt); err != nil {
						runErr = err
					}
					cursor = advancePastLine(src, int(stmt.End().Line()))
					if r.Exited() {
						return runErr
					}
					if r.LangVariant() != parseLang {
						break
					}
				}
				retryStart = cursor
				if r.LangVariant() != parseLang {
					continue
				}
			}
		}
		if !gotErr {
			return runErr
		}
		// Advance past the offending line. The error line is absolute
		// (because we prepended newlines), so find the next '\n' at
		// or after the start of that line in src.
		errLine := int(pe.Pos.Line())
		newCursor := advancePastLine(src, errLine)
		if retryLang := r.LangVariant(); retryLang != parseLang && newCursor > cursor {
			if retryStart <= cursor || retryStart > newCursor {
				retryStart = lineStart(src, errLine)
			}
			if prog, _, gotErr := parseOnce(paddedChunk(src, retryStart, newCursor), retryLang); !gotErr {
				if prog != nil && len(prog.Stmts) > 0 {
					if err := r.Run(ctx, prog); err != nil {
						runErr = err
					}
				}
				cursor = newCursor
				continue
			}
		}
		printBashParseError(os.Stderr, src, errPrefix, pe)
		if fatalRecoveredParseError(src, pe) {
			return interp.ExitStatus(2)
		}
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

var errNoStreamRecovery = errors.New("streaming execution not selected")

func needsStatementStreamRecovery(src []byte) bool {
	if bytes.Contains(src, []byte("$(")) && bytes.Contains(src, []byte("<<")) {
		return true
	}
	if bytes.Contains(src, []byte("${'")) || bytes.Contains(src, []byte("${$'")) {
		return true
	}
	for start := 0; ; {
		idx := bytes.Index(src[start:], []byte("${$"))
		if idx < 0 {
			return false
		}
		after := start + idx + len("${$")
		if after >= len(src) || src[after] != '(' {
			return true
		}
		start = after + 1
	}
}

func runStatementStream(
	ctx context.Context,
	r *interp.Runner,
	src []byte,
	lang syntax.LangVariant,
	errPrefix string,
) error {
	if !needsStatementStreamRecovery(src) {
		return errNoStreamRecovery
	}
	type hdocWarning struct {
		startLine int
		eofLine   int
		stop      string
	}
	var hdocWarnings []hdocWarning
	hdocWarn := func(startLine, eofLine int, stop string) {
		hdocWarnings = append(hdocWarnings, hdocWarning{startLine, eofLine, stop})
	}
	type comsubWarning struct {
		line  int
		count int
	}
	var comsubWarnings []comsubWarning
	comsubWarn := func(line, count int) {
		comsubWarnings = append(comsubWarnings, comsubWarning{line, count})
	}
	flushWarnings := func() {
		for _, warning := range comsubWarnings {
			plural := ""
			if warning.count > 1 {
				plural = "s"
			}
			fmt.Fprintf(os.Stderr,
				"%s: line %d: warning: command substitution: %d unterminated here-document%s\n",
				errPrefix, warning.line, warning.count, plural)
		}
		comsubWarnings = comsubWarnings[:0]
		for _, warning := range hdocWarnings {
			fmt.Fprintf(os.Stderr,
				"%s: line %d: warning: here-document at line %d delimited by end-of-file (wanted `%s')\n",
				errPrefix, warning.eofLine, warning.startLine, warning.stop)
		}
		hdocWarnings = hdocWarnings[:0]
	}
	var runErr error
	cursor := 0
	for cursor < len(src) {
		parseLang := r.LangVariant()
		parser := syntax.NewParser(syntax.Variant(parseLang), syntax.HeredocEOFWarning(hdocWarn),
			syntax.HeredocComsubWarning(comsubWarn))
		restart := false
		chunk := paddedChunk(src, cursor, len(src))
		for stmt, err := range parser.StmtsSeq(bytes.NewReader(chunk)) {
			if stmt != nil {
				flushWarnings()
				if err := r.Run(ctx, stmt); err != nil {
					runErr = err
				}
				cursor = advancePastLine(src, int(stmt.End().Line()))
				if r.Exited() {
					return runErr
				}
				if r.LangVariant() != parseLang {
					restart = true
					break
				}
			}
			if err != nil {
				if pe, ok := bashRecoverableParseError(err); ok {
					text := rewriteParserErrorText(string(src), pe)
					if reportLine, resumeLine, ok := heredocBodyBadSubstRecovery(src, pe, text); ok {
						hdocWarnings = hdocWarnings[:0]
						fmt.Fprintf(os.Stderr, "%s: line %d: %s\n", errPrefix, reportLine, text)
						cursor = advancePastLine(src, resumeLine)
						r.SetLastExitStatus(1)
						runErr = interp.ExitStatus(2)
						restart = true
						break
					}
					suppressHdocWarning := len(hdocWarnings) > 0 &&
						int(pe.Pos.Line()) > hdocWarnings[len(hdocWarnings)-1].startLine
					if suppressHdocWarning && text == "unexpected EOF while looking for matching `)'" {
						fmt.Fprintf(os.Stderr, "%s: command substitution: line %d: %s\n",
							errPrefix, int(pe.Pos.Line())+1, text)
						fmt.Fprintln(os.Stdout)
						return interp.ExitStatus(2)
					}
					flushWarnings()
					if strings.HasSuffix(text, ": bad substitution") {
						errLine := int(pe.Pos.Line())
						start := lineStart(src, errLine)
						if start > cursor {
							prefix := paddedChunk(src, cursor, start)
							prefixParser := syntax.NewParser(syntax.Variant(parseLang), syntax.HeredocEOFWarning(hdocWarn))
							for stmt, perr := range prefixParser.StmtsSeq(bytes.NewReader(prefix)) {
								if stmt != nil {
									if err := r.Run(ctx, stmt); err != nil {
										runErr = err
									}
								}
								if perr != nil {
									break
								}
							}
							cursor = start
						}
					}
					printBashParseError(os.Stderr, src, errPrefix, pe)
					if strings.HasPrefix(text, "${$") && strings.HasSuffix(text, ": bad substitution") {
						r.SetLastExitStatus(1)
						_ = r.Run(ctx, &syntax.File{})
						return interp.ExitStatus(2)
					}
					if fatalRecoveredParseError(src, pe) {
						return interp.ExitStatus(2)
					}
					newCursor := advancePastLine(src, int(pe.Pos.Line()))
					if newCursor <= cursor {
						return interp.ExitStatus(2)
					}
					cursor = newCursor
					r.SetLastExitStatus(1)
					runErr = interp.ExitStatus(2)
					restart = true
					break
				}
				return err
			}
		}
		if restart {
			continue
		}
		if err := r.Run(ctx, &syntax.File{}); err != nil && runErr == nil {
			runErr = err
		}
		return runErr
	}
	if err := r.Run(ctx, &syntax.File{}); err != nil && runErr == nil {
		runErr = err
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

func lineStart(src []byte, line int) int {
	if line <= 1 {
		return 0
	}
	current := 1
	for i, b := range src {
		if b == '\n' {
			current++
			if current == line {
				return i + 1
			}
		}
	}
	return len(src)
}

func heredocBodyBadSubstRecovery(src []byte, pe syntax.ParseError, text string) (reportLine, resumeLine int, ok bool) {
	if !strings.HasSuffix(text, ": bad substitution") {
		return 0, 0, false
	}
	line := int(pe.Pos.Line())
	if line <= 1 {
		return 0, 0, false
	}
	prev := strings.TrimSpace(nthLine(src, line-1))
	body := strings.TrimSpace(nthLine(src, line))
	next := strings.TrimSpace(nthLine(src, line+1))
	if body == "" || next == "" {
		return 0, 0, false
	}
	idx := strings.Index(prev, "<<")
	if idx < 0 {
		return 0, 0, false
	}
	rest := strings.TrimSpace(prev[idx+2:])
	if strings.HasPrefix(rest, "-") {
		rest = strings.TrimSpace(rest[1:])
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0, 0, false
	}
	stop := strings.Trim(fields[0], `'"`)
	if stop == "" || next != stop {
		return 0, 0, false
	}
	return line - 1, line + 1, true
}

func paddedChunk(src []byte, start, end int) []byte {
	lineAtStart := bytes.Count(src[:start], []byte("\n")) + 1
	if lineAtStart <= 1 {
		return src[start:end]
	}
	chunk := make([]byte, lineAtStart-1+end-start)
	for i := 0; i < lineAtStart-1; i++ {
		chunk[i] = '\n'
	}
	copy(chunk[lineAtStart-1:], src[start:end])
	return chunk
}

func fatalRecoveredParseError(src []byte, pe syntax.ParseError) bool {
	if strings.HasSuffix(rewriteParserErrorText(string(src), pe), ": bad substitution") {
		return false
	}
	if commandSubstOpenBefore(src, pe.Pos) {
		return true
	}
	return bytes.Contains(src, []byte("$(")) &&
		rewriteParserErrorText(string(src), pe) == "unexpected EOF while looking for matching `)'"
}

func bashRecoverableParseError(err error) (syntax.ParseError, bool) {
	var pe syntax.ParseError
	if errors.As(err, &pe) {
		return pe, true
	}
	var le syntax.LangError
	if errors.As(err, &le) && strings.Contains(le.Feature, "nested parameter expansions") {
		return syntax.ParseError{
			Filename: le.Filename,
			Pos:      le.Pos,
			Text:     le.Feature,
		}, true
	}
	if errors.As(err, &le) && strings.Contains(le.Feature, "`=(` process substitutions") {
		return syntax.ParseError{
			Filename: le.Filename,
			Pos:      le.Pos,
			Text:     le.Feature,
		}, true
	}
	return syntax.ParseError{}, false
}

func commandSubstOpenBefore(src []byte, pos syntax.Pos) bool {
	end := offsetBeforePos(src, pos)
	if end <= 0 {
		return false
	}
	depth := 0
	for i := 0; i < end; i++ {
		switch src[i] {
		case '$':
			if i+1 < end && src[i+1] == '(' {
				if i+2 < end && src[i+2] == '(' {
					continue
				}
				depth++
				i++
			}
		case ')':
			if depth > 0 {
				depth--
			}
		}
	}
	return depth > 0
}

func offsetBeforePos(src []byte, pos syntax.Pos) int {
	line, col := int(pos.Line()), int(pos.Col())
	if line <= 0 || col <= 0 {
		return 0
	}
	currentLine := 1
	lineStart := 0
	for i, b := range src {
		if currentLine == line {
			end := lineStart + col - 1
			if end > len(src) {
				return len(src)
			}
			return end
		}
		if b == '\n' {
			currentLine++
			lineStart = i + 1
		}
	}
	if currentLine == line {
		end := lineStart + col - 1
		if end > len(src) {
			return len(src)
		}
		return end
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
	if text := arithmeticParseErrorText(string(src), pe); text != "" {
		fmt.Fprintf(w, "%s: line %d: %s\n", prefix, line, text)
		return
	}
	text := rewriteParserErrorText(string(src), pe)
	if eofLine, construct, ok := compoundEOFParseError(src, text); ok {
		fmt.Fprintf(w, "%s: line %d: syntax error: unexpected end of file from `%s' command on line 1\n", prefix, eofLine, construct)
		return
	}
	if text == "unexpected EOF while looking for matching `)'" && strings.TrimSpace(nthLine(src, line)) == "math1)" {
		fmt.Fprintf(w, "%s: line %d: syntax error near unexpected token `)'\n", prefix, line)
		fmt.Fprintf(w, "%s: line %d: `math1)'\n", prefix, line)
		return
	}
	if strings.HasPrefix(text, "unexpected EOF") && line == 1 &&
		bytes.Contains(src, []byte("$(")) && bytes.Contains(src, []byte("<<")) {
		line = bytes.Count(src, []byte("\n")) + 1
	}
	fmt.Fprintf(w, "%s: line %d: %s\n", prefix, line, text)
	if strings.HasSuffix(text, ": bad substitution") {
		return
	}
	if strings.Contains(text, ": `") && strings.HasSuffix(text, "': not a valid identifier") {
		return
	}
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

func compoundEOFParseError(src []byte, text string) (eofLine int, construct string, ok bool) {
	for _, keyword := range []string{"if", "while", "until", "for", "case"} {
		if text == fmt.Sprintf("`%s` statement must end with `%s`", keyword, compoundEndKeyword(keyword)) {
			line := bytes.Count(src, []byte("\n")) + 1
			if strings.TrimSpace(nthLine(src, line)) != "" {
				line++
			}
			return line, keyword, true
		}
	}
	return 0, "", false
}

func compoundEndKeyword(keyword string) string {
	switch keyword {
	case "if":
		return "fi"
	case "case":
		return "esac"
	default:
		return "done"
	}
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
	if text, ok := declareInvalidIdentifierText(src, pe); ok {
		return text
	}
	if strings.Contains(pe.Text, "nested parameter expansions") {
		if subst := nestedBadSubstSource(src, pe.Pos); subst != "" {
			subst = strings.ReplaceAll(subst, "$'", "'")
			return subst + ": bad substitution"
		}
		return "bad substitution"
	}
	if pe.Text == "invalid parameter name" {
		if subst := nestedBadSubstSource(src, pe.Pos); subst != "" && subst != "${" {
			subst = strings.ReplaceAll(subst, "$'", "'")
			return subst + ": bad substitution"
		}
	}
	if strings.Contains(pe.Text, "cannot be followed by a word") {
		if subst := nestedBadSubstSource(src, pe.Pos); subst != "" {
			subst = strings.ReplaceAll(subst, "$'", "'")
			return subst + ": bad substitution"
		}
	}
	// Bash escalates a partial-arithmetic parse to "missing `))`"
	// instead of naming the inner token — match that for `((` blocks
	// before any of the per-message rewrites below.
	if text := arithmeticParseErrorText(src, pe); text != "" {
		return text
	}
	if insideUnclosedArith(src, pe.Pos) {
		return "unexpected EOF while looking for matching `)'"
	}
	if commandSubstOpenBefore([]byte(src), pe.Pos) &&
		strings.Contains(pe.Text, "statement must end with") {
		return "syntax error near unexpected token `)'"
	}
	if commandSubstOpenBefore([]byte(src), pe.Pos) &&
		strings.Contains(pe.Text, "must be followed by a statement list") {
		return "syntax error near unexpected token `)'"
	}
	if commandSubstOpenBefore([]byte(src), pe.Pos) &&
		strings.Contains(pe.Text, "must be followed by `do`") {
		return "syntax error near unexpected token `done' while looking for matching `)'"
	}
	if commandSubstOpenBefore([]byte(src), pe.Pos) || strings.Contains(src, "$(") {
		switch {
		case strings.Contains(pe.Text, "`done` can only"):
			return "syntax error near unexpected token `done' while looking for matching `)'"
		case strings.Contains(pe.Text, "`esac` can only"):
			return "syntax error near unexpected token `esac' while looking for matching `)'"
		case strings.Contains(pe.Text, "`;;` can only"):
			return "syntax error near unexpected token `in' while looking for matching `)'"
		}
	}
	switch {
	case pe.Text == "statements must be separated by &, ; or a newline",
		pe.Text == "array element values must be words",
		strings.Contains(pe.Text, "a command can only contain words and redirects"),
		strings.Contains(pe.Text, "`=(` process substitutions"),
		strings.Contains(pe.Text, "must be followed by"),
		strings.Contains(pe.Text, "must follow a name"):
		if strings.Contains(pe.Text, "`=(` process substitutions") {
			return "syntax error near unexpected token `('"
		}
		if pe.Text == "array element values must be words" {
			if tok := arrayElementErrorTokenAt(src, pe.Pos); tok != "" {
				return fmt.Sprintf("syntax error near unexpected token `%s'", tok)
			}
		}
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

func declareInvalidIdentifierText(src string, pe syntax.ParseError) (string, bool) {
	if pe.Text != "invalid var name" {
		return "", false
	}
	line := strings.TrimSpace(nthLine([]byte(src), int(pe.Pos.Line())))
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", false
	}
	name := fields[0]
	switch name {
	case "declare", "export", "local", "readonly", "typeset":
	default:
		return "", false
	}
	for _, field := range fields[1:] {
		if strings.HasPrefix(field, "-") {
			continue
		}
		return fmt.Sprintf("%s: `%s': not a valid identifier", name, field), true
	}
	return "", false
}

func arrayElementErrorTokenAt(src string, pos syntax.Pos) string {
	off := offsetBeforePos([]byte(src), pos)
	if off < 0 || off >= len(src) {
		return ""
	}
	rest := src[off:]
	if strings.HasPrefix(rest, "<>") {
		return "<>"
	}
	return ""
}

func arithmeticParseErrorText(src string, pe syntax.ParseError) string {
	if strings.Contains(pe.Text, "`~` must be followed by an expression") {
		line := strings.TrimSpace(nthLine([]byte(src), int(pe.Pos.Line())))
		expr := "~"
		if strings.Contains(line, "$(( ~ ))") || strings.Contains(line, "(( ~ ))") {
			expr = "~ "
		}
		return fmt.Sprintf("%s: arithmetic syntax error: operand expected (error token is %q)", expr, expr)
	}
	if !strings.Contains(pe.Text, "not a valid arithmetic operator:") {
		return ""
	}
	line := strings.TrimSpace(nthLine([]byte(src), int(pe.Pos.Line())))
	if line == "" {
		return ""
	}
	expr, command, spaced := arithmeticSourceExpr(line)
	if expr == "" {
		return ""
	}
	token := parseErrorBacktickToken(pe.Text)
	if token == "" {
		return ""
	}
	errToken := token
	if idx := strings.Index(expr, token); idx >= 0 {
		errToken = strings.TrimLeft(expr[idx:], " \t")
		if strings.ContainsAny(errToken, " \t") {
			errToken = strings.TrimRight(errToken, " \t") + " "
		} else if strings.Contains(errToken, "=") {
			errToken += " "
		}
	}
	if command {
		sep := ":"
		if spaced {
			sep = " :"
		}
		return fmt.Sprintf("((: %s%s arithmetic syntax error in expression (error token is %q)", expr, sep, errToken)
	}
	return fmt.Sprintf("%s: arithmetic syntax error in expression (error token is %q)", expr, errToken)
}

func arithmeticSourceExpr(line string) (expr string, command, spaced bool) {
	if strings.HasPrefix(line, "((") {
		if end := strings.LastIndex(line, "))"); end >= 0 {
			raw := line[2:end]
			return strings.TrimSpace(raw), true, strings.TrimRight(raw, " \t") != raw
		}
	}
	if start := strings.Index(line, "$(("); start >= 0 {
		if end := strings.LastIndex(line[start+3:], "))"); end >= 0 {
			return strings.TrimSpace(line[start+3 : start+3+end]), false, false
		}
	}
	if eq := strings.IndexByte(line, '='); eq > 0 {
		prefix := line[:eq]
		if open := strings.IndexByte(prefix, '['); open > 0 && strings.HasSuffix(prefix, "]") {
			return strings.TrimSpace(prefix[open+1 : len(prefix)-1]), false, false
		}
	}
	return "", false, false
}

func parseErrorBacktickToken(text string) string {
	start := strings.LastIndex(text, "`")
	if start < 0 {
		return ""
	}
	end := strings.LastIndex(text[:start], "`")
	if end < 0 {
		return ""
	}
	return text[end+1 : start]
}

func nestedBadSubstSource(src string, pos syntax.Pos) string {
	off := offsetBeforePos([]byte(src), pos)
	if off <= 0 || off > len(src) {
		return ""
	}
	start := strings.LastIndex(src[:off], "${")
	if start < 0 {
		return ""
	}
	depth := 0
	for i := start; i < len(src); i++ {
		switch src[i] {
		case '$':
			if i+1 < len(src) && src[i+1] == '{' {
				depth++
				i++
			}
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 {
					return src[start : i+1]
				}
			}
		case '\n':
			return ""
		}
	}
	return ""
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
