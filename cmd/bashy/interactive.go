// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

package main

import (
	"context"
	"fmt"
	"io"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func runInteractive(r *interp.Runner, stdin io.Reader, stdout, stderr io.Writer) error {
	lang := syntax.LangBash
	if *posix {
		lang = syntax.LangPOSIX
	}
	parser := syntax.NewParser(syntax.Variant(lang))

	var cmdNum int

	prompt := func(ps string) {
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
		expanded := expandPrompt(val, envGet, cmdNum, cmdNum)
		fmt.Fprint(stdout, expanded)
	}

	prompt("PS1")
	for stmts, err := range parser.InteractiveSeq(stdin) {
		if err != nil {
			return err
		}
		if parser.Incomplete() {
			prompt("PS2")
			continue
		}
		cmdNum++
		ctx := context.Background()
		for _, stmt := range stmts {
			err := r.Run(ctx, stmt)
			if r.Exited() {
				return err
			}
		}
		prompt("PS1")
	}
	return nil
}
