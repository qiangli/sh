package interp

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/polyglot"
	"mvdan.cc/sh/v3/syntax"
)

// A runner fence (`~~~<type> as <alias> !<runner>`) hands its materialized
// body to a runner the script names. The runner resolves, in this order, to
// a Bash++ function of the unit, a shell function, a builtin, or a command
// the embedder registered — and never to a program on PATH: a fence means
// the same thing on every host, so a PATH tool is wrapped in a function.
// Every shape is invoked as `runner <verb> <file> [args…]`; its stdout (a
// Bash++ function: its string result) is the call's value and a failing
// status is the call's error.

// FenceRunnerRegistered, when set, reports whether the embedder dispatches
// a command of that name itself (a registered command of the host shell).
// nil — a standalone engine — registers nothing, so only functions and
// builtins may serve as runners.
var FenceRunnerRegistered func(name string) bool

// bashPPRunnerFence builds the runtime of a runner fence, binding the
// runner to this Runner's dispatch. Fences are prepared before the unit's
// first statement runs, so the runner's own top-level declaration, when the
// unit holds one, is evaluated now: a fence may name a function declared
// anywhere in its unit, and the declaration runs again, harmlessly, in
// order.
func (r *Runner) bashPPRunnerFence(ctx context.Context, file *syntax.File, block *syntax.SourceBlock, language string) polyglot.RunnerFence {
	runner := block.Runner.Value
	for _, stmt := range file.Stmts {
		switch decl := stmt.Cmd.(type) {
		case *syntax.FuncDecl:
			if decl.Name.Value == runner {
				r.cmd(ctx, decl)
			}
		case *syntax.BashPPFuncDecl:
			if decl.Receiver == nil && decl.Name.Value == runner {
				r.cmd(ctx, decl)
				if r.bashPPHoistedDecls == nil {
					r.bashPPHoistedDecls = map[*syntax.BashPPFuncDecl]bool{}
				}
				r.bashPPHoistedDecls[decl] = true
			}
		}
	}
	return polyglot.RunnerFence{
		Type:   language,
		Runner: runner,
		Invoke: func(ctx context.Context, argv []string) (string, error) {
			return r.bashPPInvokeRunner(ctx, block, runner, argv)
		},
	}
}

func (r *Runner) bashPPInvokeRunner(ctx context.Context, block *syntax.SourceBlock, runner string, argv []string) (string, error) {
	if fn, ok := r.bashPPFuncs[runner]; ok {
		args := make([]any, len(argv))
		for i, arg := range argv {
			args[i] = arg
		}
		result, err := r.bashPPForeignCallback(ctx, runner, fn, args)
		if err != nil {
			return "", err
		}
		return foreignResult(result), nil
	}
	switch {
	case r.Funcs[runner] != nil, IsBuiltin(runner) && !r.disabledBuiltins[runner],
		FenceRunnerRegistered != nil && FenceRunnerRegistered(runner):
	default:
		return "", fmt.Errorf("runner %s is not a function, a builtin or a registered command (PATH is never consulted)", runner)
	}
	// A command runner runs in this shell with its stdout captured, the way
	// a funsub `${ cmd; }` does: same scope, no subshell, exit state
	// restored afterwards.
	var out bytes.Buffer
	savedStdout, savedExit := r.stdout, r.exit
	r.stdout = &out
	defer func() {
		r.stdout = savedStdout
		r.exit = savedExit
	}()
	r.exit = exitStatus{}
	if len(argv) >= 2 {
		r.setVarString("BASHPP_FENCE_TYPE", block.Language.Value)
		r.setVarString("BASHPP_FENCE_FILE", argv[1])
	}
	r.call(ctx, block.Pos(), append([]string{runner}, argv...))
	// The command's stdout is the value with its trailing newlines removed,
	// as a command substitution reads a command.
	value := strings.TrimRight(out.String(), "\n")
	if err := ctx.Err(); err != nil {
		return value, err
	}
	if r.exit.err != nil {
		return value, r.exit.err
	}
	if r.exit.code != 0 || r.exit.exiting || r.exit.fatalExit {
		return value, fmt.Errorf("%s exited %d", runner, r.exit.code)
	}
	return value, nil
}
