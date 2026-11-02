---
id: 6988fd06e87a
kind: bug
title: S557 join replacement signal forwarders after process completion
seq: 179
status: assigned
priority: p1
created: 2026-11-01T23:54:32.875763Z
assignee: s557-manager
sprint: 557
sprint_id: f5ccf563-f5d1-5b94-8be4-9388e51ef86d
sprint_title: 'Bashy v1.0.0 preparation: split private/public development and retire qiangli/dhnt'
---

Native41359794189 Linuxworkspace TestConcurrencyScheduleMatrix detects2goroutines forwardSignalsWithReport.func2 exec_replacement_signal_unix.go82 age1min. Candidate74697208. Diagnose cleanupownership without addingallowlist or reducing schedule matrix. /tmp/s557-native-125007549282.log. Boundedsignal/concurrency tests; nofullsuite local. Preserve sync/async signalcontracts.


## Delivery evidence

Implemented by s557-public-tooling as the manager-authorized internal helper;
story ownership remains s557-manager. The forwarder already stopped and joined
correctly when its callback ran. The native dependency bridge only ran that
callback from explicit session.close, while its process waiter published done
without stopping forwarding. Process completion now owns that cleanup and joins
before publishing done; sync.OnceFunc shares the stop operation safely with the
existing explicit close path. Signal delivery/reporting and signal dispositions
remain handled by the existing forwarder unchanged.

Extracted the existing process waiter into waitNativeProcess, preserving drain,
listener and socket cleanup. Bounded reexec-test-child cases use the actual
signal forwarder, normal exit and killed exit, a blocked stop callback proving
done cannot publish before joining, concurrent owner close, and repeated close.
No native dependency worker or full product was built for these tests.

Bounded GOWORK=off / GOMAXPROCS=2 / GOFLAGS=-p=2 tests passed:
- TestNativeProcessWaitJoinsSignalForwarder (normal, killed).
- Existing TestGoSourceNativeCancellationClassification,
  TestGoSourceNativeCancellationKeepsProcessFailures,
  TestGoSourceNativeForwardedSignalBecomesProgramStatus,
  TestGoSourceScalarInterruptionCannotBecomeFatal,
  TestGoSourceForwardedSignalRestoresWiredInterruption.
- Existing TestAsyncSelfSignalFailedPublishedReplacement.
- Unmodified TestConcurrencyScheduleMatrix under -short (same scheduler matrix
  and two repetitions per scheduler setting, no allowlist/threshold changes).
Combined focused check completed in 1.164s. Full hosted native validation remains
required to confirm the original leak report is resolved in suite ordering.
