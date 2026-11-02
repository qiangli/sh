---
id: 7a887d4056ea
kind: bug
title: S557 repair shell native fixture preparation and race audit coverage
seq: 174
status: assigned
priority: p1
created: 2026-11-01T19:31:36.125261Z
assignee: s557-manager
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

Sprint557 migration repair. Work only in assigned isolated workspace. Read local CLAUDE/AGENTS. Preserve all test assertions, coverage, thresholds and deadlines; diagnose before modifying. No release/push/parent pin changes. No heavy/full local builds/suites: bounded targeted tests only GOMAXPROCS=2 GOFLAGS=-p=2, full validation hosted by manager. Commit named files with Sprint: #557 and correct Story/Story-ID trailers and injected BASHY_AGENT. Report exact evidence and limitations. Fullshell41334013294 Linux onlyfunctional failure TestGoSourcePointerInitializer hardcodesGOTOOLCHAINgo1.27.1 downloadstderr CombinedOutput contaminating7 9 oracle; nativeShquick artifact test GOPROXYoff lacksx/toolsv0.38.0 dependency preparation. Separatebuild/toolchainpreparefromruntimeoracle preservingexactstdout/offlineexecution and assertions. Race oracleaudit blocks6locations5files fifo benchmark const200,fifo_marker_test map,fifo_marker_windows bytes.Buffer,signal_windows bytes.Buffer,signal_wintable2maps: inspect intendedpolicy and document/repair exact inventory without weakening gate. Logs/tmp/s557-fullshell-41334013294-linux and /tmp/s557-native-ubuntu-workspace-41332670393. Do not touch syntax/parser let work assignedseparately.

Delivery in isolated s557-nativeprep-review checkout: pointer initializer
compiles with the installed supported SDK in a separate 120-second phase,
then retains its exact combined-output oracle and original 10-second execution
deadline. Standalone shellexec artifact preparation downloads its declared
module graph from disposable go.mod/go.sum copies; the existing artifact
phase still uses GOPROXY=off and its unchanged 120-second budget, removes the
source, and executes with PATH=/no-tools. Repository manifests are not edited.

Audit review: the benchmark constant is immutable; FIFO uniqueness and Windows
signal-table maps belong to one serial test invocation. Those exact declarations
carry documented safe-private annotations. Shared Windows FIFO/signal output
uses existing mutex-protected buffers, as does the newly added strict async
EXIT fixture (ownership coordinated with its author). No audit selectors,
required inventory, thresholds, skips, or assertions were relaxed.

Bounded verification (GOMAXPROCS=2, GOFLAGS=-p=2, GOWORK=off): pointer fixture
PASS 0.712s; offline compiled-backend fixture PASS 1.921s; FIFO leaf/signal table
race tests PASS 1.464s; strict async EXIT race test PASS 1.513s. Windows amd64
interp test package cross-compiled successfully; Windows execution remains
hosted. Audit self-test PASS; production audit PASS (scanned=51 skipped=1136
total=1187). Full race and fresh-cache hosted acceptance remain pending. No
full local suite, push, parent pin change, or parser/let edit occurred.

## New lifecycle regression inventory follow-up

Frozen run41370656834 correctly refused race execution because the exact manifest
omitted TestCommandSubstitutionExitTrapCancellation. Auditing retained hosted
interp discovery found another new prior-selector member:
TestPhaseDiagnosticCanceledBeforeSnapshot. Both now have explicit cancellation
rows. The companion TestCommandSubstitutionExitTrapPanic is explicitly included
in the panic category. All169 hosted-discovered interp legacy-selector members
are covered; no selector, category floor, test assertion or deadline is relaxed.
Focused race execution of these three authored regressions passes count3. The
full race gate remains a required hosted acceptance check.
