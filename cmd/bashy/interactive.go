// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ergochat/readline"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// ignoreEOFLimit decodes the IGNOREEOF environment variable into the
// number of *additional* EOFs to tolerate before exiting. An unset
// or empty value disables the feature (ok=false). A non-numeric value
// behaves like bash's documented default of 10.
func ignoreEOFLimit(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(s); err == nil {
		if n < 0 {
			n = 0
		}
		return n, true
	}
	return 10, true
}

// setHistCmd publishes the interactive command counter as $HISTCMD.
// Bash only sets HISTCMD when history is enabled (interactive mode),
// so we update it here rather than in lookupVar.
func setHistCmd(r *interp.Runner, n int) {
	if r.Vars == nil {
		r.Vars = make(map[string]expand.Variable)
	}
	r.Vars["HISTCMD"] = expand.Variable{
		Set:  true,
		Kind: expand.String,
		Str:  strconv.Itoa(n),
	}
}

func runInteractive(r *interp.Runner, stdin *os.File, stdout, stderr io.Writer) error {
	lang := syntax.LangBash
	if *posix {
		lang = syntax.LangPOSIX
	}

	var cmdNum int

	getPrompt := func(ps string) string {
		defaultPS := `\u@\h:\w\$ `
		if ps == "PS2" {
			defaultPS = "> "
		}
		val := r.Env.Get(ps).String()
		if val == "" {
			val = defaultPS
		}
		envGet := func(name string) string {
			return r.Env.Get(name).String()
		}
		return expandPrompt(val, envGet, cmdNum, cmdNum)
	}

	// Determine history file path.
	histFile := r.Env.Get("HISTFILE").String()
	if histFile == "" {
		if home, _ := os.UserHomeDir(); home != "" {
			histFile = filepath.Join(home, ".bashy_history")
		}
	}

	rl, err := readline.NewFromConfig(&readline.Config{
		Prompt:            getPrompt("PS1"),
		HistoryFile:       histFile,
		HistoryLimit:      1000,
		HistorySearchFold: true,
		InterruptPrompt:   "^C",
		EOFPrompt:         "exit",
		Stdin:             stdin,
		Stdout:            stdout,
		Stderr:            stderr,
	})
	if err != nil {
		return runInteractiveBasic(r, stdin, stdout)
	}
	defer rl.Close()

	// IGNOREEOF counts consecutive Ctrl-D presses (received as EOF
	// errors) we tolerate before actually exiting. Bash treats unset
	// as "exit on first EOF", a positive integer N as "require N+1
	// presses", and a non-numeric value the same as N=10.
	var eofPresses int
	for {
		// Execute PROMPT_COMMAND before displaying PS1.
		if pc := r.Env.Get("PROMPT_COMMAND").String(); pc != "" {
			pcp := syntax.NewParser(syntax.Variant(lang))
			if prog, err := pcp.Parse(strings.NewReader(pc), "PROMPT_COMMAND"); err == nil {
				r.Run(context.Background(), prog)
			}
		}

		rl.SetPrompt(getPrompt("PS1"))
		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				eofPresses = 0
				continue
			}
			limit, ok := ignoreEOFLimit(r.Env.Get("IGNOREEOF").String())
			if ok && eofPresses < limit {
				eofPresses++
				io.WriteString(stderr, "Use \"exit\" to leave the shell.\n")
				continue
			}
			break // EOF
		}
		eofPresses = 0
		if strings.TrimSpace(line) == "" {
			continue
		}

		// Collect continuation lines for incomplete input.
		input := line
		for {
			parser := syntax.NewParser(syntax.Variant(lang))
			_, err := parser.Parse(strings.NewReader(input), "")
			if err == nil {
				break // Complete input
			}
			// Check if it's an incomplete parse (needs more input).
			if !parser.Incomplete() {
				break // Real parse error, let it through
			}
			rl.SetPrompt(getPrompt("PS2"))
			cont, err := rl.Readline()
			if err != nil {
				break
			}
			input += "\n" + cont
		}

		// Parse and execute.
		parser := syntax.NewParser(syntax.Variant(lang))
		prog, err := parser.Parse(strings.NewReader(input), "")
		if err != nil {
			io.WriteString(stderr, "bashy: "+err.Error()+"\n")
			continue
		}
		cmdNum++
		setHistCmd(r, cmdNum)
		ctx := context.Background()
		for _, stmt := range prog.Stmts {
			if err := r.Run(ctx, stmt); r.Exited() {
				return err
			}
		}
	}
	return nil
}

// runInteractiveBasic is the fallback when readline is not available.
func runInteractiveBasic(r *interp.Runner, stdin io.Reader, stdout io.Writer) error {
	lang := syntax.LangBash
	if *posix {
		lang = syntax.LangPOSIX
	}
	parser := syntax.NewParser(syntax.Variant(lang))
	var cmdNum int

	getPrompt := func(ps string) string {
		defaultPS := `\u@\h:\w\$ `
		if ps == "PS2" {
			defaultPS = "> "
		}
		val := r.Env.Get(ps).String()
		if val == "" {
			val = defaultPS
		}
		envGet := func(name string) string {
			return r.Env.Get(name).String()
		}
		return expandPrompt(val, envGet, cmdNum, cmdNum)
	}

	io.WriteString(stdout, getPrompt("PS1"))
	for stmts, err := range parser.InteractiveSeq(stdin) {
		if err != nil {
			return err
		}
		if parser.Incomplete() {
			io.WriteString(stdout, getPrompt("PS2"))
			continue
		}
		cmdNum++
		setHistCmd(r, cmdNum)
		ctx := context.Background()
		for _, stmt := range stmts {
			err := r.Run(ctx, stmt)
			if r.Exited() {
				return err
			}
		}
		io.WriteString(stdout, getPrompt("PS1"))
	}
	return nil
}
