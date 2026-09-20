# Plan — Sprint 216, Story 536 (sh-owned portion): KISS Windows first-hour shell runtime

Goal: make the first hour of real shell use on Windows work (paths, cd/pwd,
temp/null operands, pipelines, process substitution) with the smallest
reusable surface. No closure claim — the Windows CI worker measures.

## Scope (in)

1. **`pathconv/` package** — one reusable conversion package extracted from
   `interp/path.go`. Accepts drive paths in backslash (`C:\x`) and forward
   (`C:/x`) form, the MSYS drive form (`/c`, `/c/...`), and the WSL form
   (`/mnt/c`, `/mnt/c/...`). Device/UNC-prefixed paths (`\\.\pipe\x`, `//x`)
   pass through untouched (UNC *support* stays out of scope). Windows-mode
   operand normalization at the same funnel: `/dev/null` → `NUL`,
   `/tmp[/...]` → `%TEMP%` (via an overridable `TempDir` hook for tests).
2. **interp wiring** — `interp/path.go` becomes thin wrappers over
   `pathconv`, so cd/PWD/absPath/open/stat/glob all funnel through it.
3. **`pwd -W`** — Windows-only flag (still an invalid option elsewhere, to
   preserve the Linux bash-fidelity oracle) printing the native path with
   forward slashes, Git-Bash style (`C:/Users/x`).
4. **`cd D:` per-drive cwd** — the runner records the last cwd per drive on
   every successful cd; a bare `X:` operand returns to that drive's recorded
   cwd (default `X:\`). Platform-neutral helpers, wiring gated on Windows.
5. **`BASHYENV=VAR/p:VAR2/l`** — WSLENV-style opt-in env conversion at the
   child-process boundary (`nativeExecEnv`): `/p` converts one MSYS/WSL path
   to native, `/l` converts a path list (`:`-separated shell form becomes
   `;`-separated native form). Overrides the built-in name list per variable.
6. **In-process pipe closed-file fix** — real `DuplicateHandle`-based
   `dupPipeFd` on Windows (so pipeline EOF propagates and originals close,
   mirroring Unix), plus Windows broken-pipe classification
   (`ERROR_BROKEN_PIPE`, `ERROR_NO_DATA`, `os.ErrClosed`) so an early-exiting
   reader yields status 141, not "file already closed".
7. **Windows process substitution** — smallest viable named-pipe seam:
   `procSubstPipe` with a mkfifo implementation on Unix (existing logic
   moved), a `\\.\pipe\...` `CreateNamedPipe`/`ConnectNamedPipe`
   implementation on Windows, and an "unsupported" stub elsewhere.
8. **Tests** — platform-neutral conversion tests (`pathconv`, BASHYENV,
   drive-cwd helpers) plus `//go:build windows` runtime tests (compile-gated
   here via `GOOS=windows go build/vet`; CI worker runs them).

## Scope (out — per story)

UNC support, glob nocase, ADS, exec emulation, argv conversion.

## Gate

`go build ./...`, `gofmt -s`, `go vet ./...`, `GOOS=windows go build ./... &&
GOOS=windows go vet ./interp ./pathconv`, `go test ./pathconv ./interp -run
'Story536|ShellPath|NativeExecEnv|PathConv'`, plus `go test -short ./interp`.
