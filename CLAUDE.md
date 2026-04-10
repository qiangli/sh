# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Pure-Go shell parser, formatter, and interpreter for POSIX Shell, Bash, mksh, and Zsh. Published as the Go module `mvdan.cc/sh/v3` and requires Go 1.25+. Two binaries are shipped from `cmd/`:

- `shfmt` — formatter (the user-facing flagship; flags/style documented in `cmd/shfmt/shfmt.1.scd`).
- `gosh` — proof-of-concept interactive shell built on `interp`.

Note: this checkout is a fork. `origin/master` is the fork integration branch — `upstream/master` with our unmerged patches rebased on top, force-pushed on each sync. Upstream PRs target `upstream/master` directly via single-commit topic branches (e.g. `interp-pipe-fd-eof`, `interp-bash-redirects`).

## Build / test / lint

```sh
# Build everything
go build ./...

# Run all tests (mirrors CI)
go test ./...
cd moreinterp && go test ./...   # separate Go module, must be tested independently

# Race detector and 32-bit (CI runs both on Linux only)
go test -race ./...
GOARCH=386 go test -count=1 ./...

# Static checks
gofmt -s -d .
go vet ./...

# Run a single test / package
go test ./syntax -run TestParseBash
go test ./interp -run TestRunnerRun/specific_subtest

# Fuzzing (Go native)
cd syntax && go test -run=- -fuzz=ParsePrint

# Confirm interpreter behavior against real Bash 5.2 (requires Docker + dockexec)
go install mvdan.cc/dockexec@latest
CGO_ENABLED=0 go test -run TestRunnerRunConfirm -exec 'dockexec bash:5.2' ./interp
```

`TestRunnerRunConfirm` is the canonical "behaves like real Bash" oracle: the same script table is run by `gosh` and by the dockerized Bash, and outputs/exit codes are diffed. When changing interpreter semantics, run it.

## Architecture

The codebase is a layered pipeline. Each layer is a standalone package usable on its own; the higher layers compose the lower ones.

1. **`syntax/`** — lexer (`lexer.go`), parser (`parser.go`, `parser_arithm.go`), AST (`nodes.go`), and printer (`printer.go`). `LangVariant` (Bash / POSIX / mksh / Bats) is a bitset that gates token recognition and parse rules. `simplify.go` implements `shfmt -s`. `walk.go` is the visitor API. `typedjson/` round-trips the AST as JSON (the first key of each tagged node MUST be `"Type"` — this is enforced for streaming decode).

2. **`expand/`** — shell expansions over `syntax` words: parameter (`param.go`), arithmetic (`arith.go`), brace (`braces.go`), tilde, glob (delegates to `pattern/`). `environ.go` defines the `Environ` / `WriteEnviron` interfaces that the interpreter plugs its variable scopes into. `expand_unix.go` / `expand_windows.go` split out platform-specific globbing.

3. **`pattern/`** — shell glob → Go regexp translation. Both `expand` and `interp` use it for `case`, `[[ ... == ... ]]`, parameter substitution patterns, etc.

4. **`interp/`** — the runner. The central type is `Runner` in `api.go`, configured exclusively via `RunnerOption` functions (`Env`, `Dir`, `StdIO`, `ExecHandlers`, `OpenHandler`, etc.) passed to `New`. After construction, exported fields are read-only. Execution flow:
   - `runner.go` walks the AST and dispatches statements/commands.
   - `builtin.go` implements shell builtins (`cd`, `read`, `printf`, `test`, …).
   - `test.go` + `test_classic.go` implement `[[ ... ]]` and POSIX `test`.
   - `handler.go` defines the **middleware model** — exec/open/read-dir/stat/etc. handlers are chained so embedders can intercept any I/O or process launch. Default handlers use real OS syscalls.
   - `vars.go` manages variable scopes (locals, globals, exported, arrays, assoc arrays).
   - Subshells are **goroutines, not real `fork()`** (pure-Go constraint) — there are no real subprocess PIDs and file descriptors aren't truly shared across "processes". Code that touches pipes/fds must keep this in mind; see commit `12f5191d` for an example (dup'ing pipe fds so EOF/SIGPIPE propagate inside the simulated pipeline).
   - `os_unix.go` / `os_notunix.go` split the few syscalls that genuinely differ.

5. **`shell/`** — thin convenience API (`Fields`, `Expand`, `SourceFile`, …) for callers who want one-shot expansion of a string without wiring up a `Runner` themselves. Uses POSIX syntax regardless of host OS — Windows paths must be escaped or single-quoted.

6. **`fileutil/`** — heuristics to recognize shell scripts by extension and shebang (used by `shfmt` when walking directories).

7. **`moreinterp/`** — **separate Go module** (`mvdan.cc/sh/moreinterp`). Contains `coreutils/`, an `ExecHandler` middleware that satisfies commands like `cat`, `cp`, `find`, `ls`, `mkdir`, `rm`, etc. via the [u-root](https://github.com/u-root/u-root) implementations. Primarily for Windows / minimal environments where these binaries aren't installed. Because it's a separate module, dependency updates and tests are run independently of the root module.

8. **`cmd/shfmt/`** — CLI wrapping `syntax` + `typedjson` + `fileutil` + `editorconfig`. Reads `.editorconfig` for per-file style. `cmd/gosh/` is a small CLI wrapping `interp.Runner` in interactive/script modes.

### Conventions / sharp edges

- The parser is **streaming over `io.Reader`** and intentionally avoids backtracking. This is why `$( (` ambiguity, dynamic assoc array indices, and a few other forms are unsupported by design — don't "fix" the parser to handle them without first reading `README.md` § Caveats.
- `export`, `let`, `declare`, `typeset`, etc. are parsed as **keywords** (not generic builtin calls) so their syntax tree is statically known — required for forms like `declare foo=(bar)`. Adding new keyword-like builtins means changes in both `lexer.go` and `parser.go`.
- AST nodes use position info (`Pos`, `End`) heavily for the printer; new node types must populate these or formatting will misalign.
- Generated code: `expand/valuekind_string.go` and `syntax/token_string.go` come from `go generate` via the `stringer` tool declared as a Go `tool` directive in `go.mod`. Run `go generate ./...` after touching the corresponding enums.
- Tests use `github.com/go-quicktest/qt` for assertions and `github.com/rogpeppe/go-internal` for `testscript`-style cases in `cmd/shfmt/testdata/`.
- Cross-compile sanity: CI builds `GOOS=plan9` and `GOOS=js GOARCH=wasm`. Don't introduce hard syscall dependencies outside `*_unix.go` / platform-tagged files.
- Builtins listed in `IsBuiltin` but not actually implemented (job control, completion programming, `ulimit`, etc.) print `<name>: not supported in this shell — <hint>` via the `unsupportedHints` map in `interp/builtin.go`. `coproc` does the same from the runner's command dispatcher. When adding a new entry to `IsBuiltin` without a dispatcher case, add a hint too — `TestUnsupportedHints` catches drift.

## Workflow

At the start of every session, read `docs/TODO.md` and pick the first unchecked item to work on. After completing it, check it off in the TODO, run `go test ./...` and `make test-bash`, then commit. Repeat until the user says otherwise.

## Plans

Always save a copy of all implementation plans in `docs/`. Use a descriptive filename (e.g., `docs/plan-feature-name.md`).

## Third-Party Libraries

- **Permissive licenses only**: All third-party dependencies must use MIT, BSD, Apache 2.0, or equivalent permissive licenses. No GPL/LGPL.
- **Pure Go only**: No CGo, no C dependencies.
- **Local vendoring for fixes**: If any third-party library is missing features or has bugs that block our work, make a local clone of its source code in `./libs/<pkg-name>/`. Add a `CREDITS.md` file in that directory with proper attribution (original author, license, upstream URL). Then make the required changes locally. Update `go.mod` to use a `replace` directive pointing to the local copy. This ensures we can fix upstream issues without waiting for PRs to be merged, while maintaining clear provenance.
>>>>>>> 5a8f7980 (fix parser and interpreter issues for bash 5.3 test compatibility)
