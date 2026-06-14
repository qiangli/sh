# mvdan.cc/sh - Pure-Go Shell Parser, Formatter, and Interpreter

This repository is a pure-Go shell toolset for POSIX Shell, Bash, Zsh, and mksh. It is a **fork** of `mvdan.cc/sh` that carries unmerged `interp`/`expand`/`syntax` patches extending **Bash 5.3 compatibility**. The user-facing Bash 5.3 drop-in CLI built on these patches — `bashy` — lives in its own repo, [`github.com/qiangli/bashy`](https://github.com/qiangli/bashy); this repo is the engine it depends on.

## Project Overview

- **Core Module**: `mvdan.cc/sh/v3` (Requires Go 1.25+)
- **Secondary Module**: `mvdan.cc/sh/moreinterp` (coreutils implementation for limited environments)
- **Primary Technology**: Pure Go (no CGo, no `fork()`). Subshells are implemented via goroutines.
- **Key Commands**:
    - `shfmt`: The flagship shell formatter (used by many editor integrations).
    - `gosh`: A proof-of-concept interactive shell.
- **Downstream**: `github.com/qiangli/bashy` (Bash 5.3 drop-in CLI), `outpost`, and `ycode` consume this module via `replace mvdan.cc/sh/v3 => ../sh`.

## Architecture & Layers

The codebase is a layered pipeline:
1.  **`syntax/`**: Lexer, parser, AST, and printer/formatter. Streaming parser (no backtracking).
2.  **`expand/`**: Shell expansions (parameter, arithmetic, brace, tilde, glob).
3.  **`pattern/`**: Shell glob to Go regexp translation.
4.  **`interp/`**: The execution engine (Runner). Uses a middleware model for I/O and process execution.
5.  **`shell/`**: High-level one-shot expansion API.
6.  **`interactive/`**: Readline wrapper for `interp.Runner` (consumed by `bashy`, outpost, ycode — keep its API stable).
7.  **`moreinterp/`**: `u-root` based coreutils (cat, ls, rm, etc.) as an `ExecHandler`.

## Building and Running

Common flows are managed via the `Makefile`:
- `make build`: Builds `gosh` and `shfmt` into `bin/`.
- `make test`: Runs all Go tests.
- `make tidy`: Standard Go maintenance (`mod tidy`, `gofmt`, `vet`).

`moreinterp/` is a separate module — test it independently: `cd moreinterp && go test ./...`.

## Development Conventions

- **Pure Go**: No hard syscall dependencies outside `*_unix.go` or platform-tagged files.
- **No Backtracking**: The parser is streaming; do not attempt to "fix" it with backtracking.
- **Keywords**: `export`, `let`, `declare` are keywords, not generic builtins.
- **Generated Code**: Run `go generate ./...` after touching enums in `syntax` or `expand`.
- **Testing**:
    - Use table-driven tests.
    - `TestRunnerRunConfirm` (in `interp`) is the "oracle" test against real Bash 5.3 (requires Docker). It is the canonical bash-fidelity check in this repo — run it when changing interpreter semantics.
    - Fuzzing: `go test -run=- -fuzz=ParsePrint` in `syntax/`.
- **Formatting**: Strictly follow `shfmt`'s style (run `make tidy`).

## Bash 5.3 drop-in work

The full Bash 5.3 drop-in (the `bashy` CLI + bash's own test suite + the compliance roadmap/scoreboard) lives in [`github.com/qiangli/bashy`](https://github.com/qiangli/bashy). A bash-fixture flip usually edits `interp`/`expand`/`syntax` **here**, then is measured by `make test-bash` **there**. Keep changes here focused and upstream-cherry-pickable.

## Strategic Orchestration (For Gemini CLI)

1.  **Verify with `TestRunnerRunConfirm`**: Any change to interpreter semantics must be verified against real Bash (Docker required). Full drop-in verification happens in the `bashy` repo.
2.  **Local PATH Gotcha**: If tests fail with signal/trap issues, ensure `ycode` shims are bypassed:
    `PATH=/bin:/usr/bin:$(dirname $(which go)) go test ./...`
3.  **Middleware Power**: Use `interp` handlers to intercept or mock system behavior for testing or specialized execution.
4.  **Commit Style**: Use scoped prefixes (e.g., `interp: fix redirect handling`).

## Key Documentation

- `README.md`: High-level overview and caveats.
- `CLAUDE.md` / `AGENTS.md`: Technical handoff and tool-specific guidance.
- The `bashy` repo's `docs/`: Bash 5.3 compliance scoreboard, roadmap, and per-fixture analyses.
