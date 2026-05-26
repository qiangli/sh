// Copyright (c) 2017, Daniel Martí <mvdan@mvdan.cc>
// See LICENSE for licensing information

// Package interactive runs a readline-backed interactive shell loop on top
// of an [*interp.Runner].
//
// Two callers share this implementation:
//
//   - cmd/bashy, the bash-compatible CLI shell, where stdin is the terminal
//     the user is typing into.
//   - Embedders driving a PTY-wrapped runner from another process (e.g. the
//     outpost matrix-shell endpoint that bridges WebSocket bytes ↔ the
//     in-process bash). PTY slaves are TTYs, so the same arrow-keys-and-
//     history experience works there too — provided the slave fd is the one
//     used for termios changes, not the daemon's actual stdin. That wiring
//     is automatic when [Options.Stdin] is an *os.File on a TTY.
//
// The fallback path (when [Options.Stdin] is not a TTY *os.File) runs the
// plain newline-buffered loop from [syntax.Parser.InteractiveSeq], without
// line-editing. Useful for tests piping stdin and for callers who want one
// entry point for both interactive and scripted use.
package interactive

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/ergochat/readline"
	"golang.org/x/term"

	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// Options configures [Run].
type Options struct {
	// Runner is the in-process shell interpreter to drive. Required.
	Runner *interp.Runner

	// Lang selects the parser variant (Bash, POSIX, mksh, …). Zero
	// defaults to [syntax.LangBash].
	Lang syntax.LangVariant

	// Stdin is the input source. When it is an *os.File on a TTY, the
	// package auto-wires raw-mode handling against that fd — required for
	// arrow keys, history navigation, and Ctrl-C. Otherwise Run falls
	// back to plain newline-buffered input via [syntax.Parser.InteractiveSeq].
	Stdin io.Reader
	// Stdout receives line-edit echo and command stdout. If nil, [os.Stdout]
	// is used.
	Stdout io.Writer
	// Stderr receives runner diagnostics. If nil, [os.Stderr] is used.
	Stderr io.Writer

	// PS1 / PS2 return the prompt to display before each new line of input.
	// The returned bytes are written verbatim — any \u/\h/\w expansion is
	// the caller's responsibility (see cmd/bashy's expandPrompt). Nil
	// defaults: PS1 = "$ ", PS2 = "> ".
	PS1 func() string
	PS2 func() string

	// PreCommand, if non-nil, is invoked before each PS1 prompt is shown.
	// Bash's $PROMPT_COMMAND is the canonical use case.
	PreCommand func(context.Context, *interp.Runner)

	// OnRunError is called when a parsed statement returns a non-nil error
	// that is not just a non-zero command exit code. Default behaviour:
	// write "<err>\n" to Stderr. Set to a no-op func to suppress.
	OnRunError func(error)

	// Greeting is written verbatim to Stdout once before the first prompt.
	// Empty = no greeting.
	Greeting string

	// HistoryFile is the path to persist input lines to. Empty = in-memory
	// only. Parent directories are not created; the caller should ensure
	// they exist.
	HistoryFile string
	// HistoryLimit caps the number of remembered lines. Zero defaults to
	// 1000. Negative disables history entirely.
	HistoryLimit int
	// HistorySearchFold makes Ctrl-R reverse search case-insensitive.
	HistorySearchFold bool
	// InterruptPrompt is what readline prints on Ctrl-C while a line is
	// being edited. Defaults to "^C".
	InterruptPrompt string
	// EOFPrompt is what readline prints on Ctrl-D on an empty line.
	// Defaults to "exit".
	EOFPrompt string
}

// Run starts the interactive read-edit-execute loop. It blocks until the
// user exits (the `exit` builtin or Ctrl-D on an empty line), the parser
// hits EOF, or ctx is canceled.
//
// The returned error is nil on a clean exit and the underlying runner
// error otherwise. A non-zero exit code from a final `exit N` propagates
// as an [interp.ExitStatus] error; callers wanting bash-style "the
// session's exit code is the last command's" should use [errors.As].
func Run(ctx context.Context, opts Options) error {
	if opts.Runner == nil {
		return errors.New("interactive: Runner is required")
	}
	r := opts.Runner

	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	stdin := opts.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	lang := opts.Lang
	if lang == 0 {
		lang = syntax.LangBash
	}
	ps1 := opts.PS1
	if ps1 == nil {
		ps1 = func() string { return "$ " }
	}
	ps2 := opts.PS2
	if ps2 == nil {
		ps2 = func() string { return "> " }
	}
	histLimit := opts.HistoryLimit
	if histLimit == 0 {
		histLimit = 1000
	}
	interruptPrompt := opts.InterruptPrompt
	if interruptPrompt == "" {
		interruptPrompt = "^C"
	}
	eofPrompt := opts.EOFPrompt
	if eofPrompt == "" {
		eofPrompt = "exit"
	}
	onRunError := opts.OnRunError
	if onRunError == nil {
		onRunError = func(err error) { _, _ = io.WriteString(stderr, err.Error()+"\n") }
	}

	if opts.Greeting != "" {
		_, _ = io.WriteString(stdout, opts.Greeting)
	}

	cfg := &readline.Config{
		Prompt:            ps1(),
		HistoryFile:       opts.HistoryFile,
		HistoryLimit:      histLimit,
		HistorySearchFold: opts.HistorySearchFold,
		InterruptPrompt:   interruptPrompt,
		EOFPrompt:         eofPrompt,
		Stdin:             stdin,
		Stdout:            stdout,
		Stderr:            stderr,
	}
	bindTTY(cfg, stdin)

	rl, err := readline.NewFromConfig(cfg)
	if err != nil {
		return runFallback(ctx, opts.Runner, stdin, stdout, stderr, lang, ps1, ps2, onRunError, opts.PreCommand)
	}
	defer rl.Close()

	// Close the readline instance when ctx is canceled so the in-flight
	// Readline() call returns instead of blocking forever (server shutdown,
	// PTY closed by remote peer, etc.).
	//
	// We intentionally do NOT also check ctx.Done() at the top of the
	// read-eval loop. Some HTTP servers cancel the request context as
	// soon as the response headers are written — for a hijacked WS this
	// happens immediately after the 101 upgrade response goes out, while
	// the underlying conn is still alive. The original parser.Interactive
	// path was unaffected because it only watched ctx after running a
	// statement, and a freshly-spawned shell has no statements to run.
	// Mirror that semantic here: cancellation tears us down via the
	// in-flight Readline closing, not via a pre-emptive return on a ctx
	// the caller may have prematurely tied to the wrong lifetime.
	if ctx.Done() != nil && ctx.Err() == nil {
		stop := context.AfterFunc(ctx, func() { _ = rl.Close() })
		defer stop()
	}

	for {
		if opts.PreCommand != nil {
			opts.PreCommand(ctx, r)
		}

		rl.SetPrompt(ps1())
		line, err := rl.Readline()
		if err != nil {
			if errors.Is(err, readline.ErrInterrupt) {
				continue
			}
			return nil // EOF (Ctrl-D) — clean exit
		}
		if strings.TrimSpace(line) == "" {
			continue
		}

		// Multi-line continuation: keep reading until the parser is
		// satisfied or hits a real syntax error. parser.Parse leaves the
		// instance in an indeterminate state after returning, so each
		// probe uses a fresh parser — mirrors the cmd/bashy pattern.
		input := line
		for {
			pp := syntax.NewParser(syntax.Variant(lang))
			_, perr := pp.Parse(strings.NewReader(input), "")
			if perr == nil {
				break
			}
			if !pp.Incomplete() {
				break
			}
			rl.SetPrompt(ps2())
			cont, cerr := rl.Readline()
			if cerr != nil {
				// EOF or Ctrl-C mid-continuation — abandon and reprompt.
				input = ""
				break
			}
			input += "\n" + cont
		}
		if input == "" {
			continue
		}

		parser := syntax.NewParser(syntax.Variant(lang))
		prog, perr := parser.Parse(strings.NewReader(input), "")
		if perr != nil {
			_, _ = io.WriteString(stderr, perr.Error()+"\n")
			continue
		}
		for _, stmt := range prog.Stmts {
			cmdCtx, cancel := context.WithCancel(ctx)
			runErr := r.Run(cmdCtx, stmt)
			cancel()
			if runErr != nil && !isExitStatus(runErr) {
				onRunError(runErr)
			}
			if r.Exited() {
				return runErr
			}
		}
	}
}

// bindTTY wires custom raw-mode / size / is-terminal callbacks on cfg when
// stdin is an *os.File backed by a TTY whose fd is NOT the daemon's actual
// fd 0. Without this readline's defaults call term.MakeRaw on syscall.Stdin
// — wrong fd, raw mode never reaches the PTY slave the caller asked us to
// drive. The hardcoded fd 0 default works for cmd/bashy (a CLI tool) but
// breaks for embedders driving a separate PTY pair.
func bindTTY(cfg *readline.Config, stdin io.Reader) {
	f, ok := stdin.(*os.File)
	if !ok {
		return
	}
	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return
	}
	var saved *term.State
	cfg.FuncIsTerminal = func() bool { return true }
	cfg.FuncMakeRaw = func() error {
		state, err := term.MakeRaw(fd)
		if err == nil {
			saved = state
		}
		return err
	}
	cfg.FuncExitRaw = func() error {
		if saved == nil {
			return nil
		}
		err := term.Restore(fd, saved)
		if err == nil {
			saved = nil
		}
		return err
	}
	cfg.FuncGetSize = func() (int, int) {
		w, h, err := term.GetSize(fd)
		if err != nil {
			return 80, 24
		}
		return w, h
	}
}

// runFallback runs the plain newline-buffered loop when readline cannot
// initialise (typically because Stdin is not a TTY — a pipe, test fixture,
// or genuinely non-interactive context). It is structurally the same loop
// that pre-existed in cmd/bashy/runInteractiveBasic and outpost's
// shell.Session.Run prior to this package — kept here so Run() has a
// single entry point regardless of TTY status.
func runFallback(ctx context.Context, r *interp.Runner, stdin io.Reader, stdout, stderr io.Writer, lang syntax.LangVariant, ps1, ps2 func() string, onRunError func(error), preCommand func(context.Context, *interp.Runner)) error {
	parser := syntax.NewParser(syntax.Variant(lang))
	if preCommand != nil {
		preCommand(ctx, r)
	}
	_, _ = io.WriteString(stdout, ps1())
	for stmts, err := range parser.InteractiveSeq(stdin) {
		if err != nil {
			_, _ = io.WriteString(stderr, err.Error()+"\n")
			return err
		}
		if parser.Incomplete() {
			_, _ = io.WriteString(stdout, ps2())
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		for _, stmt := range stmts {
			cmdCtx, cancel := context.WithCancel(ctx)
			runErr := r.Run(cmdCtx, stmt)
			cancel()
			if runErr != nil && !isExitStatus(runErr) {
				onRunError(runErr)
			}
			if r.Exited() {
				return runErr
			}
		}
		if preCommand != nil {
			preCommand(ctx, r)
		}
		_, _ = io.WriteString(stdout, ps1())
	}
	return nil
}

// isExitStatus reports whether err is just a non-zero command exit code
// (interp.ExitStatus) rather than a fatal handler error.
func isExitStatus(err error) bool {
	var ec interp.ExitStatus
	return errors.As(err, &ec)
}
