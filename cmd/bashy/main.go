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
	"strings"

	"golang.org/x/term"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

var (
	command = flag.String("c", "", "command to be executed")
	version = flag.Bool("version", false, "print version and exit")
	posix   = flag.Bool("posix", false, "POSIX mode")

	// Flags accepted for compatibility; not yet implemented.
	_ = flag.Bool("norc", false, "do not read ~/.bashyrc")
	_ = flag.Bool("noprofile", false, "do not read /etc/profile or ~/.bashy_profile")
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
	// Build the initial environment with bashy identity variables.
	env := expand.ListEnviron(append(os.Environ(), bashVersionVars()...)...)
	r, err := interp.New(
		interp.Interactive(true),
		interp.StdIO(os.Stdin, os.Stdout, os.Stderr),
		interp.Env(env),
	)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func runAll() error {
	r, err := newRunner()
	if err != nil {
		return err
	}

	if *command != "" {
		return run(r, strings.NewReader(*command), "")
	}
	if flag.NArg() == 0 {
		if term.IsTerminal(int(os.Stdin.Fd())) {
			return runInteractive(r, os.Stdin, os.Stdout, os.Stderr)
		}
		return run(r, os.Stdin, "")
	}
	for _, path := range flag.Args() {
		if err := runPath(r, path); err != nil {
			return err
		}
	}
	return nil
}

func run(r *interp.Runner, reader io.Reader, name string) error {
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
