---
id: 3ef468f4e831
kind: task
title: Execute unchanged standard-library test bodies with interpreter-owned callbacks
seq: 56
status: todo
priority: p0
created: 2026-09-09T07:00:31.40852Z
sprint: 118
---

Parent 6f0c4d9a31be. Implement product test-body loading and callback execution per /Users/qiangli/projects/poc/dhnt/bashpp-tests/docs/bridge-corpus/test-body-driver-contract.md (current reviewed copy /tmp/s118-review-harness-002/docs/bridge-corpus/). First concrete errors_test source package all original companion files and tests TestNewEqual/TestErrorMethod. Original bodies remain interpreter-owned, no native original function forwarding. Native testing scheduler descriptors may callback interpreter. Scope new interp/gosource_testing* and necessary narrow parser API, coordinate worker32 gosource files and31 native bridge before cross-edits. Meaningful original pass and separate failing harness, failnow defer, nestedcallback cancellation. Account all unexecuted standard-library obligations; firstslice not whole corpus closure. No subagents push or storyclosure; 30minute bounded implementation slice, commit all scoped workingchanges.
