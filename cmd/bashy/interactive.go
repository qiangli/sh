// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/interactive"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

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
			cmdNum++
		},
		OnRunError: func(err error) {
			_, _ = io.WriteString(stderr, "bashy: "+err.Error()+"\n")
		},
	})
}
