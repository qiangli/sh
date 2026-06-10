# Repository Guidelines

## Project Structure & Module Organization
- This repository is a Go workspace for `mvdan.cc/sh/v3`, a shell parser, formatter, and interpreter.
- Core packages live in top-level directories: `syntax/` (lexer/parser/AST/printer), `interp/` (runner), `expand/` (shell expansions), `shell/` (convenience API), `pattern/` (glob matching), `fileutil/` (script detection).
- Command-line tools are under `cmd/`: `cmd/shfmt` (formatter), `cmd/gosh` (interactive shell), `cmd/bashy` (Bash 5.3 drop-in).
- `moreinterp/` is a **separate Go module** (`mvdan.cc/sh/moreinterp`). Must test it independently: `cd moreinterp && go test ./...`
- Tests sit beside code as `*_test.go`. Bash compatibility fixtures live under `external/bash-5.3/tests/`.

## Build, Test, and Development Commands
- `make build` builds the main binaries into `bin/`.
- `go test ./...` runs the Go test suite across all packages.
- `make test` runs the same full Go test sweep from the repository root.
- `make test-bash` runs the Bash 5.3 compatibility suite against `bashy`; it is slower and depends on `external/bash-5.3/tests/`.
- `make tidy` runs `go mod tidy`, `gofmt -s -w .`, and `go vet ./...`.
- `make clean` removes the `bin/` directory.

### Running Specific Tests
```bash
# Run a single test or package
go test ./syntax -run TestParseBash
go test ./interp -run TestRunnerRun/specific_subtest

# Confirm behavior against real Bash 5.3 (requires Docker)
go install mvdan.cc/dockexec@latest
CGO_ENABLED=0 go test -run TestRunnerRunConfirm -exec 'dockexec bash:5.3' ./interp
```

### PATH Gotcha (ycode Shim)
Some tests fork shells via `exec sh -c '...'` and verify signal traps. If `PATH` puts a ycode shim in front of `sh`, tests fail because the shim doesn't forward `SIGINT`/`SIGTERM`. Workaround:
```bash
PATH=/bin:/usr/bin:$(dirname $(which go)) go test ./...
```

## Coding Style & Naming Conventions
- Use `gofmt` formatting and keep Go code idiomatic.
- Follow existing package naming and keep exported identifiers descriptive.
- Prefer short, lower-case file names that match package purpose, such as `parser.go`, `printer.go`, or `handler_test.go`.
- When adding shell fixtures or examples, keep names aligned with the package or feature under test.
- **Generated code**: `syntax/token_string.go` comes from `go generate` via the `stringer` tool. Run `go generate ./...` after touching the corresponding enums.

## Testing Guidelines
- Add unit tests alongside code changes, using the standard Go `testing` package and the existing table-driven style where practical.
- Name tests `TestXxx`, benchmarks `BenchmarkXxx`, and fuzz targets `FuzzXxx`.
- For parser, formatter, and interpreter changes, update or add focused tests in the affected package before relying on the full suite.
- Run `go test ./...` for routine validation and `make test-bash` when changing Bash-compatibility behavior.

## Commit & Pull Request Guidelines
- Recent commits use concise, scoped prefixes such as `interp:`, `syntax:`, or `expand+interp:` followed by a brief imperative summary.
- Keep commits focused on one behavior or bug fix.
- Pull requests should describe the change, the affected package(s), and the validation performed.
- Include sample output or reproduction steps when behavior changes, especially for CLI or compatibility fixes.

## Agent-Specific Instructions
- Prefer small, targeted edits over broad refactors.
- Preserve existing test conventions and do not introduce new formatting tools without a clear need.
