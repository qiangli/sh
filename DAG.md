---
name: sh
description: Build/test/lint targets for the sh fork as a bashy dag pipeline (agent-first equivalent of the Makefile)
---

# sh — DAG task file

The `mvdan.cc/sh/v3` fork (Bash 5.3 interpreter patches). This DAG file is the
agent-first equivalent of the `Makefile`, runnable with `bashy dag`:

```bash
bashy dag --list            # available targets
bashy dag test              # the suite (skips the Docker-only bash oracle)
bashy dag --json test       # machine-readable envelope for an agent
```

This module is standalone — no sibling/replace deps.

## Tasks

### build
Build the library + the two cmd binaries (shfmt, gosh).
Sources: cmd/, interp/, expand/, syntax/, pattern/, shell/, go.mod, go.sum
Effects: read

```bash
go build ./...
```

### test
Run the full suite. Skips `TestRunnerRunConfirm`/`TestParseConfirm` — the
behaves-like-real-Bash oracle, which needs Docker + bash 5.3 (see `confirm`).
Effects: read

```bash
go test ./... -skip 'TestRunnerRunConfirm|TestParseConfirm'
```

### test-moreinterp
`moreinterp/` is a separate Go module (u-root-backed coreutils ExecHandler);
test it independently.
Effects: read

```bash
cd moreinterp && go test ./...
```

### test-race
Race detector over the suite (CI runs this on Linux).
Effects: read

```bash
go test -race ./... -skip 'TestRunnerRunConfirm|TestParseConfirm'
```

### bashpp-race-gate
Bash++ race/lifecycle gate. Records toolchain, package and test discovery,
the broad unsafe-oracle audit, focused schedule runs, and the complete race
suite in `artifacts/bashpp-race-gate.txt`. The real Bash compatibility corpus
remains the separate `confirm` task because it needs the external Bash oracle.
The CI job gives this bounded gate enough time to emit its own diagnostics.
Effects: read, write

```bash
make bashpp-race-gate
```

### confirm
The canonical "behaves like real Bash 5.3" oracle: the same script table is run
by `gosh` and by a dockerized Bash, and outputs/exit codes are diffed. Needs
Docker + `mvdan.cc/dockexec`.
Effects: read, net

```bash
CGO_ENABLED=0 go test -run TestRunnerRunConfirm -exec 'dockexec bash:5.3' ./interp
```

### vet
Static checks: gofmt + go vet.
Effects: read

```bash
set -e
gofmt -s -d .
go vet ./...
```

### tidy
go mod tidy + gofmt -s -w . + go vet.
Effects: write

```bash
set -e
go mod tidy
gofmt -s -w .
go vet ./...
```

### clean
go clean.
Effects: destroy

```bash
go clean ./...
```
