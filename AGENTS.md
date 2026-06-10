# Repository Guidelines

## Project Structure & Module Organization
- Go workspace for `mvdan.cc/sh/v3`, a shell parser, formatter, and interpreter.
- Core packages: `syntax/` (lexer/parser/AST/printer), `interp/` (runner), `expand/` (expansions), `shell/` (convenience API), `pattern/` (glob), `fileutil/` (script detection).
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

## Agent-Specific Instructions
- Prefer small, targeted edits over broad refactors.
- Preserve existing test conventions; don't introduce new formatting tools without clear need.
