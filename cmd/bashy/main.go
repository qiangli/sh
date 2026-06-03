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

func main() {
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
	opts := []interp.RunnerOption{
		interp.Interactive(true),
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
	return r, nil
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
	// Bash 5.3 treats `<<EOF\n...` running off the end of the file as a
	// warning (not an error) and uses whatever was read up to EOF as
	// the body. Wire that behaviour through the parser so the
	// affected tests (comsub-eof, exportfunc, …) behave like bash.
	hdocWarn := func(startLine, eofLine int, stop string) {
		prefix := name
		if prefix == "" {
			prefix = "bashy: -c"
		}
		fmt.Fprintf(os.Stderr,
			"%s: line %d: warning: here-document at line %d delimited by end-of-file (wanted `%s')\n",
			prefix, eofLine, startLine, stop)
	}
	prog, err := syntax.NewParser(syntax.Variant(lang), syntax.HeredocEOFWarning(hdocWarn)).Parse(bytes.NewReader(src), name)
	if err != nil {
		var pe syntax.ParseError
		if errors.As(err, &pe) {
			printBashParseError(os.Stderr, src, name, pe)
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
func printBashParseError(w io.Writer, src []byte, name string, pe syntax.ParseError) {
	prefix := name
	if prefix == "" {
		prefix = "bashy: -c"
	}
	line := int(pe.Pos.Line())
	fmt.Fprintf(w, "%s: line %d: %s\n", prefix, line, pe.Text)
	if srcLine := nthLine(src, line); srcLine != "" {
		fmt.Fprintf(w, "%s: line %d: `%s'\n", prefix, line, srcLine)
	}
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
