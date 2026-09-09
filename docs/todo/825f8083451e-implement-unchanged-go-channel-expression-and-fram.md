---
id: 825f8083451e
kind: task
title: Implement unchanged Go channel expression and frame semantics
seq: 51
status: assigned
priority: p0
created: 2026-09-09T06:37:55.650583Z
weave: 30
assignee: qiangli
sprint: 118
---

Sprint118 current candidate002 measured failures: Tour buffered-channels/channels/range-and-close/select; GbE channel-buffering/channel-directions/closing-channels. Receive expressions fmt.Println(<-ch) report unary ILLEGAL; sends pass raw local variable j/sum rather than evaluated scalar; goroutine calls fibonacci(cap(c),c) retain rawexpression. Fix actual GoSource channel receive/send/function argument evaluation and goroutineframe semantics using existing Bash++runtime, preserving oldBash++ABI and guards. Own interp/bashpp_concurrency*.go, interp/bashpp_chan*.go, interp/gosource_calls.go and newfocused tests; inspectactualfilepaths and notify manager if neededsharedfileoutside ownership beforeediting. Do notmodifygosource/lower, nativebridgefiles, scalar/p1/functopglobal sincepeersown. Reproduce unchangedsource from canonical ../bashpp-tests/tour/_content/tour and examples/channel*. Candidate002 rawlogs manager evidence. ScopeGOsourceonly whereappropriate preserveClassic. No source rewrites/fullnativeGo forwarding. Need3mode diffs andrace focusedtests. Commit proper Sprint118,Story+ID. No subagents/push/closure;30minbounded. Send API/fileownership plan early. Parent6f0c4d9a31be.
