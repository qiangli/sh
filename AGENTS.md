# AGENTS.md

This file provides guidance to AI coding assistants working in this repository.

## Project Structure & Module Organization
- Go workspace for `mvdan.cc/sh/v3`, a shell parser, formatter, and interpreter.
- Core packages: `syntax/` (lexer/parser/AST/printer), `interp/` (runner), `expand/` (expansions), `shell/` (convenience API), `pattern/` (globbing), `fileutil/` (script detection).
- CLI tools under `cmd/`: `shfmt` (formatter), `gosh` (interactive shell), `bashy` (Bash 5.3 drop-in).
- **`moreinterp/` is a separate Go module** (`mvdan.cc/sh/moreinterp`). Must test independently: `cd moreinterp && go test ./...`

## Build, Test, and Development Commands
```bash
make build          # Build binaries to bin/
make test           # Run all Go tests
make test-bash      # Run Bash 5.3 compatibility suite against bashy (slow)
make test-bash-list # List available bash tests
make tidy           # Run go mod tidy, gofmt -s -w ., go vet ./...
make clean          # Remove bin/
```

### Running Specific Tests
```bash
go test ./syntax -run TestParseBash
go test ./interp -run TestRunnerRun/specific_subtest

# Confirm behavior against real Bash 5.3 (requires Docker)
go install mvdan.cc/dockexec@latest
CGO_ENABLED=0 go test -run TestRunnerRunConfirm -exec 'dockexec bash:5.3' ./interp

# Fuzzing
cd syntax && go test -run=- -fuzz=ParsePrint
```

### PATH Gotcha (ycode Shim)
Some tests fork shells via `exec sh -c '...'` and verify signal traps. If `PATH` puts a ycode shim in front of `sh`, tests fail because the shim doesn't forward `SIGINT`/`SIGTERM`. Workaround:
```bash
PATH=/bin:/usr/bin:$(dirname $(which go)) go test ./...
```

## Coding Style & Conventions
- Use `gofmt` formatting and idiomatic Go.
- Prefer short, lower-case file names matching package purpose (e.g., `parser.go`, `printer.go`).
- **Generated code**: `syntax/token_string.go` and `expand/valuekind_string.go` come from `go generate` via `stringer`. Run `go generate ./...` after touching the corresponding enums.

## Testing Guidelines
- Add unit tests alongside code changes using standard Go `testing` and table-driven style.
- Name tests `TestXxx`, benchmarks `BenchmarkXxx`, fuzz targets `FuzzXxx`.
- For parser/formatter/interpreter changes, update focused tests in the affected package first.

## Commit & Pull Request Guidelines
- Use scoped prefixes: `interp:`, `syntax:`, `expand+interp:` followed by imperative summary.
- Keep commits focused on one behavior or bug fix.
- Include sample output or reproduction steps when behavior changes.

## Architecture

1. **`syntax/`** — lexer, parser, AST, and printer for shell scripts supporting Bash, POSIX, mksh, and Bats.
2. **`expand/`** — handles parameter, arithmetic, brace, tilde, and glob expansions leveraging `pattern/`.
3. **`interp/`** — core runner of shell scripts with middleware model for custom command execution (via goroutines simulating subprocesses).
4. **`shell/`** — provides convenient API functions for one-off expansion and script evaluation.
5. **`fileutil/`** — recognizes shell scripts by file extension and shebang lines.
6. **`moreinterp/`** — separate Go module with `coreutils/` middleware for basic command implementations on Windows or minimal environments.
7. **`interactive/`** — reusable readline wrapper integrated into `cmd/bashy`.
8. **`internal/`** — private utilities shared between packages.

## Important Scripts

- **`cmd/shfmt/main.go`**: Implements the shell formatter, integrates with editorconfig for per-file style rules.
- **`cmd/gosh/main.go`**: Minimal interactive shell using `interp.Runner`, primarily a proof-of-concept.
- **`cmd/bashy/main.go`**: Bash 5.3 compatible shell built on `interp`, provides enhancements like prompt expansion and version variables.

**Read [CLAUDE.md](./CLAUDE.md)** for Claude Code-specific conventions.
