// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

// bashy is a Bash 5.3 compatible shell built on top of [interp].
package main

import (
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
)

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
	r, err = interp.New(
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
	)
	if err != nil {
		return nil, err
	}
	return r, nil
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
		loadStartupFiles(r, false)
		return run(r, strings.NewReader(*command), "")
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
	prog, err := syntax.NewParser(syntax.Variant(lang)).Parse(reader, name)
	if err != nil {
		return err
	}
	r.Reset()
	ctx := context.Background()
	return r.Run(ctx, prog)
}

func runPath(r *interp.Runner, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return run(r, f, path)
}
