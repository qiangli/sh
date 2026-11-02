---
id: 50c64b10fca7
kind: bug
title: S557 repair full-shell toolchain fixture and channel timeout evidence
seq: 177
status: assigned
priority: p1
created: 2026-11-01T23:52:56.068293Z
assignee: s557-test-readiness
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

Fullshell41356393662 at74697208: TestGoSourcePragmaPosition supported.go compilerstdoutpolluted by go1.27.1 SDKdownload bothOS; macOS TestS243OriginalChannelDomainRegressions chanlinear.go stillruntime60s. Diagnose independently preserve exact assertions/runtime60s and sourcebehavior; no fullsuite/productbuild local. Evidence /tmp/s557-shell-full-41356393662-failed.log. Separate let/flags failures owned176. Workisolated currentSh8d8ce12b, focusedauthored/regressionchecks only GOMAXPROCS2 GOFLAGS-p2; commitnopush.


## Delivery and remaining diagnosis

The pragma compiler fixture now invokes the SDK running the test through `runtime.GOROOT()/bin/go`, with `GOTOOLCHAIN=local` and `GOWORK=off`. It no longer provisions Go 1.27.1 inside the exact compiler-output oracle. Both the supported pragma's empty-output assertion and the rejected pragma's exact singleton diagnostic, source position, and failure status remain unchanged. `GOWORK=off GOMAXPROCS=2 GOFLAGS=-p=2 go test ./lower -run '^TestGoSourcePragmaPosition$' -count=1 -timeout=60s` passed in 0.432s.

Hosted phase evidence establishes that the macOS chanlinear failure is in interpreter execution: oracle compilation 0.152s, oracle execution 0.013s, interpreter 60.408s with empty output. It is not explained by compiler time sharing the runtime budget. A focused local CPU-profile run passed in 1.633s interpreter time; three further repetitions passed in 1.371s, 1.322s, and 1.446s. The module-cache SDK lacks its test tree locally, so these bounded checks used a temporary path-only test overlay pointing at the previously retained unchanged Go 1.27.2 chanlinear source. No original fixture bytes or assertions changed. Local success does not resolve the hosted full-package failure.

Only the separately timed original-channel helper now logs interpreter start/end goroutine counts and GOMAXPROCS, and samples goroutine stacks once at 55s. The snapshot is emitted only on interpreter failure. Its timer is canceled and its goroutine joined on normal completion and cleanup before accessing the captured string. The interpreter deadline remains 60s; native deadlines, original source, output comparisons, and compiled-artifact assertions are untouched. No speculative channel runtime changes were made.

The timer/capture helper's authored cancellation and join tests passed three repetitions under `-race` in 1.237s using only its two test files. The original focused channel regression with instrumentation passed again: interpreter 1.403s, package 2.842s, start/end goroutines 3/4. Hosted focused execution and potentially the full-package contention context remain required to obtain the failing stack and identify the runtime cause. This delivery fixes the pragma fixture and improves channel evidence; it does not claim the macOS timeout fixed.

## Cold-cache pinned compiler follow-up

Frozen run41370656834 Linux discovered/executed3940 top-level cases, with one
failure: TestGoSourceTypedWideUint combined output included the Go1.27.1 download
notice before `true`. Its compiler pin, ten-second deadline and exact combined
output assertion remain unchanged. The canonical full runner now prepares that
oracle before package enumeration or timed fixture execution. Preparation logs
resolver and compiler identities, requires both to be exactly Go1.27.1, and fails
closed on provisioning or identity errors. Acquisition diagnostics are retained
in the setup log, never filtered out of program output.

Five mocked preparation cases pass: expected identity with download diagnostics,
wrong resolver version, wrong compiler version, resolver failure and compiler
failure. Actual cached Go1.27.1 resolver/compiler identity checks pass. Unchanged
TestGoSourceTypedWideUint and TestClassicWideIntegerParity pass (4.217s). No full
local suite was run; cold-cache full acceptance remains hosted.
