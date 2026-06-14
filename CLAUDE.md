# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Pure-Go shell parser, formatter, and interpreter for POSIX Shell, Bash, mksh, and Zsh. Published as the Go module `mvdan.cc/sh/v3` and requires Go 1.25+. Two binaries are shipped from `cmd/` (see `Makefile`'s `CMDS`):

- `shfmt` — formatter (the user-facing flagship; flags/style documented in `cmd/shfmt/shfmt.1.scd`).
- `gosh` — proof-of-concept interactive shell built on `interp`.

This fork carries unmerged `interp`/`expand`/`syntax` patches that extend Bash 5.3 semantics. The **`bashy` Bash 5.3 drop-in CLI** that those patches exist for now lives in its own repo, [`github.com/qiangli/bashy`](https://github.com/qiangli/bashy) — a downstream consumer of this module via `replace mvdan.cc/sh/v3 => ../sh`. The bash 5.3 compliance test suite (`make test-bash`) and the bashy planning/status docs moved there too; this repo keeps `TestRunnerRunConfirm` as its library-level bash-fidelity oracle (see Build/test below). A bash-5.3 feature flip typically edits `interp`/`expand`/`syntax` here, then is measured by `make test-bash` over in `bashy`.

Note: this checkout is a fork. `origin/master` is the fork integration branch — `upstream/master` with our unmerged patches rebased on top, force-pushed on each sync. Upstream PRs target `upstream/master` directly via single-commit topic branches (e.g. `interp-pipe-fd-eof`, `interp-bash-redirects`).

## Build / test / lint

The `Makefile` wraps the common flows: `make build`, `make test`, `make tidy`, `make clean`. For finer-grained control use the underlying `go` commands:

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

# Confirm interpreter behavior against real Bash 5.3 (requires Docker + dockexec)
go install mvdan.cc/dockexec@latest
CGO_ENABLED=0 go test -run TestRunnerRunConfirm -exec 'dockexec bash:5.3' ./interp
```

`TestRunnerRunConfirm` is the canonical "behaves like real Bash" oracle: the same script table is run by `gosh` and by the dockerized Bash, and outputs/exit codes are diffed. When changing interpreter semantics, run it.

### Local-env PATH gotcha (ycode shim)

A few tests fork a real shell via `exec sh -c '...'` (e.g. `interp/TestKillTimeout`, `cmd/shfmt/TestScript/atomic`) and verify behaviour like signal-delivered traps or atomic in-place writes. If your `PATH` puts a `ycode` shim in front of `sh` — common on this machine, where `which sh` returns something like `/var/folders/.../ycode-wrap/.../bin/sh` — those tests fail because the shim doesn't forward `SIGINT`/`SIGTERM` to the real shell underneath, so traps never fire. The failures look like:

```
TestKillTimeout/#01: want: trapped\n  got: ""
TestScript/atomic: unknown command "input.sh" for "ycode"
```

Workaround when running the suite locally:

```sh
PATH=/bin:/usr/bin:$(dirname $(which go)) go test ./...
```

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

8. **`interactive/`** — single-file reusable readline wrapper around `interp.Runner` (consumed by sibling projects: the `bashy` CLI, outpost's matrix shell + `/ssh`, ycode's shell runner — keep its API stable when refactoring). `Options.AssumeTTY` (+ `GetSize`) makes the loop treat a non-TTY stdin as an already-raw terminal — raw-mode enter/exit become no-ops, echo/editing are readline's own; the consumer is outpost's Windows virtual PTY, where no kernel PTY exists for an in-process runner and the far-end terminal (SSH client / xterm.js) is the one in raw mode. This checkout is a submodule under the `dhnt/` umbrella; `outpost` and `ycode` import it via `replace mvdan.cc/sh/v3 => ../sh`. See `dhnt/CLAUDE.md` for the sibling-replace convention — drifting the `sh` SHA between consumers was the catalyst for the umbrella migration, so coordinated pin-bumps matter.

9. **`internal/`** — module-private helpers (`pattern.go`, `testing.go`) shared between `syntax`/`interp` tests; not part of the public API surface.

10. **`cmd/shfmt/`** — CLI wrapping `syntax` + `typedjson` + `fileutil` + `editorconfig`. Reads `.editorconfig` for per-file style. `cmd/gosh/` is a small CLI wrapping `interp.Runner` in interactive/script modes. (The Bash 5.3 drop-in CLI built on these packages is the separate [`github.com/qiangli/bashy`](https://github.com/qiangli/bashy) repo.)

### Conventions / sharp edges

- The parser is **streaming over `io.Reader`** and intentionally avoids backtracking. This is why `$( (` ambiguity, dynamic assoc array indices, and a few other forms are unsupported by design — don't "fix" the parser to handle them without first reading `README.md` § Caveats.
- `export`, `let`, `declare`, `typeset`, etc. are parsed as **keywords** (not generic builtin calls) so their syntax tree is statically known — required for forms like `declare foo=(bar)`. Adding new keyword-like builtins means changes in both `lexer.go` and `parser.go`.
- AST nodes use position info (`Pos`, `End`) heavily for the printer; new node types must populate these or formatting will misalign.
- Generated code: `expand/valuekind_string.go` and `syntax/token_string.go` come from `go generate` via the `stringer` tool declared as a Go `tool` directive in `go.mod`. Run `go generate ./...` after touching the corresponding enums.
- Tests use `github.com/go-quicktest/qt` for assertions and `github.com/rogpeppe/go-internal` for `testscript`-style cases in `cmd/shfmt/testdata/`.
- Cross-compile sanity: CI builds `GOOS=plan9` and `GOOS=js GOARCH=wasm`. Don't introduce hard syscall dependencies outside `*_unix.go` / platform-tagged files.
- Builtins listed in `IsBuiltin` but not actually implemented (job control, completion programming, `ulimit`, etc.) print `<name>: not supported in this shell — <hint>` via the `unsupportedHints` map in `interp/builtin.go`. `coproc` does the same from the runner's command dispatcher. When adding a new entry to `IsBuiltin` without a dispatcher case, add a hint too — `TestUnsupportedHints` catches drift.

## Workflow

This repo is the library/engine. When you change interpreter semantics, the canonical check is `TestRunnerRunConfirm` (see Build/test) — run it before committing. Bash 5.3 drop-in behaviour as a whole (the full CLI + bash's own test suite) is validated in the [`bashy`](https://github.com/qiangli/bashy) repo via `make test-bash`; if your change targets a specific bash fixture, flip it there.

Commit style follows upstream: scoped prefixes (`interp:`, `syntax:`, `expand+interp:`) with an imperative summary; keep commits focused so they cherry-pick cleanly onto upstream topic branches.

## Plans

Save implementation plans for non-trivial work as Markdown alongside the change or in the repo root; use a descriptive filename (e.g. `plan-feature-name.md`). (The bashy compliance roadmap and per-fixture analyses live in the `bashy` repo's `docs/`.)

## Third-Party Libraries

- **Permissive licenses only**: All third-party dependencies must use MIT, BSD, Apache 2.0, or equivalent permissive licenses. No GPL/LGPL.
- **Pure Go only**: No CGo, no C dependencies.
- **Local vendoring for fixes**: If any third-party library is missing features or has bugs that block our work, make a local clone of its source code in `./libs/<pkg-name>/`. Add a `CREDITS.md` file in that directory with proper attribution (original author, license, upstream URL). Then make the required changes locally. Update `go.mod` to use a `replace` directive pointing to the local copy. This ensures we can fix upstream issues without waiting for PRs to be merged, while maintaining clear provenance.
