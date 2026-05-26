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

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interactive"
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
		envGet := func(name string) string { return r.Env.Get(name).String() }
		return expandPrompt(val, envGet, cmdNum, cmdNum)
	}

	histFile := r.Env.Get("HISTFILE").String()
	if histFile == "" {
		if home, _ := os.UserHomeDir(); home != "" {
			histFile = filepath.Join(home, ".bashy_history")
		}
	}

	var eofPresses int
	return interactive.Run(context.Background(), interactive.Options{
		Runner:            r,
		Lang:              lang,
		Stdin:             stdin,
		Stdout:            stdout,
		Stderr:            stderr,
		PS1:               func() string { return getPrompt("PS1") },
		PS2:               func() string { return getPrompt("PS2") },
		HistoryFile:       histFile,
		HistoryLimit:      1000,
		HistorySearchFold: true,
		InterruptPrompt:   "^C",
		EOFPrompt:         "exit",
		PreCommand: func(ctx context.Context, r *interp.Runner) {
			if pc := r.Env.Get("PROMPT_COMMAND").String(); pc != "" {
				pcp := syntax.NewParser(syntax.Variant(lang))
				if prog, err := pcp.Parse(strings.NewReader(pc), "PROMPT_COMMAND"); err == nil {
					_ = r.Run(ctx, prog)
				}
			}
			eofPresses = 0
			cmdNum++
			setHistCmd(r, cmdNum)
		},
		// IGNOREEOF asks the shell to tolerate N additional Ctrl-D
		// presses before actually exiting on EOF. Returning true here
		// keeps the interactive loop running; false (default) lets it
		// exit on the first EOF as bash does without IGNOREEOF.
		OnEOF: func() bool {
			limit, ok := ignoreEOFLimit(r.Env.Get("IGNOREEOF").String())
			if !ok || eofPresses >= limit {
				return false
			}
			eofPresses++
			_, _ = io.WriteString(stderr, "Use \"exit\" to leave the shell.\n")
			return true
		},
		OnRunError: func(err error) {
			_, _ = io.WriteString(stderr, "bashy: "+err.Error()+"\n")
		},
	})
}
