//go:build ignore

// Build this helper against the candidate sh checkout before a corpus run.
// It prepares only declared dependencies under a separate 120-second deadline.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"mvdan.cc/sh/v3/polyglot"
)

func main() {
	contextFile := flag.String("context", "", "explicit dependency context JSON")
	sourceFile := flag.String("source", "", "exact raw aggregated Rust fence source")
	rustc := flag.String("rustc", "rustc", "selected compiler")
	flag.Parse()
	if *contextFile == "" || *sourceFile == "" {
		fmt.Fprintln(os.Stderr, "-context and -source are required")
		os.Exit(2)
	}
	source, err := os.ReadFile(*sourceFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	r := polyglot.Rust{Command: *rustc, ContextFile: *contextFile}
	if err = r.PrewarmDependencies(ctx, string(source)); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("PASS Rust locked dependency prewarm; source compilation not attempted")
}
