# mvdan.cc/sh - Pure-Go Shell Parser, Formatter, and Interpreter

This repository is a pure-Go shell toolset for POSIX Shell, Bash, Zsh, and mksh. It is currently being developed as a fork with a primary focus on **Bash 5.3 compatibility** (via the `bashy` command) and "Agentic" extensions.

## Project Overview

- **Core Module**: `mvdan.cc/sh/v3` (Requires Go 1.25+)
- **Secondary Module**: `mvdan.cc/sh/moreinterp` (coreutils implementation for limited environments)
- **Primary Technology**: Pure Go (no CGo, no `fork()`). Subshells are implemented via goroutines.
- **Key Commands**:
    - `shfmt`: The flagship shell formatter (used by many editor integrations).
    - `gosh`: A proof-of-concept interactive shell.
    - `bashy`: A Bash 5.3 drop-in replacement (current active development focus).

## Architecture & Layers

The codebase is a layered pipeline:
1.  **`syntax/`**: Lexer, parser, AST, and printer/formatter. Streaming parser (no backtracking).
2.  **`expand/`**: Shell expansions (parameter, arithmetic, brace, tilde, glob).
3.  **`pattern/`**: Shell glob to Go regexp translation.
4.  **`interp/`**: The execution engine (Runner). Uses a middleware model for I/O and process execution.
5.  **`shell/`**: High-level one-shot expansion API.
6.  **`interactive/`**: Readline wrapper for `interp.Runner`.
7.  **`moreinterp/`**: `u-root` based coreutils (cat, ls, rm, etc.) as an `ExecHandler`.

## Building and Running

Common flows are managed via the `Makefile`:
- `make build`: Builds `bashy`, `gosh`, and `shfmt` into `bin/`.
- `make test`: Runs all Go tests (including `moreinterp`).
- `make test-bash`: Runs the native Bash 5.3 test suite against `bin/bashy` (the primary quality metric).
- `make test-bash-list`: Lists available bash tests.
- `make tidy`: Standard Go maintenance (`mod tidy`, `gofmt`, `vet`).

**Running a single bash test**:
```bash
cd external/bash-5.3/tests
THIS_SH=../../../bin/bashy PATH=$PWD:$PATH ../../../bin/bashy ./<name>.tests
```

## Development Conventions

- **Pure Go**: No hard syscall dependencies outside `*_unix.go` or platform-tagged files.
- **No Backtracking**: The parser is streaming; do not attempt to "fix" it with backtracking.
- **Keywords**: `export`, `let`, `declare` are keywords, not generic builtins.
- **Generated Code**: Run `go generate ./...` after touching enums in `syntax` or `expand`.
- **Testing**:
    - Use table-driven tests.
    - `TestRunnerRunConfirm` (in `interp`) is the "oracle" test against real Bash 5.3 (requires Docker).
    - Fuzzing: `go test -run=- -fuzz=ParsePrint` in `syntax/`.
- **Formatting**: Strictly follow `shfmt`'s style (run `make tidy`).

## Active Focus: Bashy & Agentic Extensions

- **Roadmap**: Tracked in `docs/TODO.md`. Always check this first for priorities.
- **Bash 5.3 Conformance**: The goal is to maximize the PASS count in `make test-bash`.
- **Agentic Extensions**: Features like `--json` output, deterministic mode, and structured errors are being added to support LLM/Agentic use cases. See `docs/agentic-extensions.md`.

## Strategic Orchestration (For Gemini CLI)

1.  **Always read `docs/TODO.md`** at the start of a session to understand current priorities and status.
2.  **Verify with `make test-bash`**: Any change to `bashy` or `interp` must be verified against the bash test suite.
3.  **Local PATH Gotcha**: If tests fail with signal/trap issues, ensure `ycode` shims are bypassed:
    `PATH=/bin:/usr/bin:$(dirname $(which go)) go test ./...`
4.  **Middleware Power**: Use `interp` handlers to intercept or mock system behavior for testing or specialized execution.
5.  **Commit Style**: Use scoped prefixes (e.g., `interp: fix redirect handling`).

## Key Documentation

- `README.md`: High-level overview and caveats.
- `docs/TODO.md`: Current development scoreboard and roadmap.
- `docs/bash-gap-analysis.md`: Detailed breakdown of missing bash features.
- `docs/agentic-extensions.md`: Design for new agent-focused features.
- `CLAUDE.md` / `AGENTS.md`: Technical handoff and tool-specific guidance.
