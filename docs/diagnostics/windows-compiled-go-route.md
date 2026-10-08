# Sprint 379 / Story 165: Windows compiled Go source route

A Go SDK reached through a Windows directory junction reproduces the hosted
failure: `filepath.EvalSymlinks` returns bare `ERROR_PATH_NOT_FOUND` while
resolving `GOROOT\bin\go.exe`, even though Windows can execute that binary.
This happens before compilation or non-main package validation. The
`bashy_core` CLI uses this standalone bootstrap; existing sh source-route tests
inject `BASHPP_GO` and did not cover it.

Windows toolchain resolution now opens the binary and obtains its resolved
path with `GetFinalPathNameByHandleW`. All four toolchain identity paths use it,
including review validation. Checksum and binary-identity checks remain in
place. Non-Windows resolution retains `filepath.EvalSymlinks`. Resolution
errors carry the operation, input path, and original cause.

## Reproduction and verification

Windows runs used noviwin1.local, isolated trees under
`C:\Users\noviadmin\s379-source-route`, `GOWORK=off`, and bashy replacing
`mvdan.cc/sh/v3` with `../sh`. The copied bashy tree was
`8dbe212ba4a85c9e20645284589a0b7a15f57a93`.

Before changing production code:

- Existing `go test -count=1 -run TestRunCompiledGoFile -v ./interp`: exit 0.
- Exact `go test -count=1 -run TestSourceRoute -v ./cmd/bashy`: exit 0 with
  both the normal module SDK and a separate toolcache-style SDK. Short 8.3
  `TEMP`/`TMP` alone also passed.
- New `go test -count=1 -run TestRunCompiledGoFileJunctionGOROOT -v ./interp`:
  exit 1. Both main and library cases failed with the exact bare Windows
  error in 0.07/0.05 seconds. A temporary contextual error identified
  `sdk-junction\bin\go.exe` as the failing symlink-resolution path.
- New `go test -count=1 -run TestBashPPGoIdentityPathDiagnostic -v ./interp`:
  exit 1 because the error lacked the resolution operation and full binary
  path.

After the fix, each command below exited 0 on both Dragon (macOS/arm64) and
noviwin1.local (Windows/amd64):

```sh
go build ./...
go vet ./...
go test -count=1 -run 'TestRunCompiledGoFile|TestBashPPGoIdentityPathDiagnostic|TestBashPPGoBootstrap|TestBashPPInjectedGo' -v ./interp
```

The final Windows regression, including injected-toolchain coverage, also
passed separately (exit 0):

```sh
go test -count=1 -run TestRunCompiledGoFileJunctionGOROOT -v ./interp
```

The actual bashy CLI suite passed on noviwin1.local (exit 0, 49.162 seconds):

```sh
go test -count=1 -run TestSourceRoute -v ./cmd/bashy
```

For that final CLI run, `GOROOT` pointed through the SDK junction,
`GOTOOLCHAIN=local`, and both `TEMP` and `TMP` were
`C:\Users\NOVIAD~1\AppData\Local\Temp`. Every Go case passed, including
extension/flag routes, module-directory execution, non-main refusal, and the
Python-extension override. The existing opt-in foreign-language cases were
outside this Go-only run; no skip or quarantine was added.

## Hosted CI handoff

Original failing run:
https://github.com/qiangli/bashy/actions/runs/37837189439/job/113517119004

The conductor owns publishing this commit, updating bashy's sh pin, and
checking hosted Windows CI. The reproduced junction bug is fixed and locally
verified; hosted green is not yet claimed. This worker commits to its current
branch without pushing, per the weave contract.
