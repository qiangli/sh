---
id: e7025e06b7da
kind: task
title: Execute unchanged Go complex scalars through the interpreter
seq: 55
status: done
priority: p0
created: 2026-09-09T06:41:24.782184Z
weave: 35
assignee: sprint118-manager
sprint: 118
closed: 2026-09-09T10:16:38.607489Z
---

Implement bounded GoSource complex scalar support for unchanged Tour basics/basic-types.go (complex128 z=cmplx.Sqrt(-5+12i), fmt %T %v) and complex64 arithmetic/conversions/real/imag/complex builtins. Bash++ oldsource policy deliberately rejectscomplex and MUST remainunchanged; enableonlyRunner.bashPPGoSource. ScopeNEW interp/gosource_complex.go/tests and interp/bashpp_scalar.go; tiny guards inbashpp_p1.go neednotify manager viaweavecomment beforeediting (worker33 ownsrestp1). Nativewire bridge worker31 ownsinterp/bashpp_native*; do noteditthose: provide precise helperpatchrequest/protocolcontract withtest to manager ifneeded. Channelworker30 ownsconcurrency/gosource_calls, frontend32 owns gosource/lower. Do notmodify upstream fixture orpipewholeprogramtonativeGo; native dependency cmplx.Sqrt mayexecuteinsideexistingpersistentbridge, testedbody remainsinterpreter. Currentbasefrozen /private/tmp/s118-published-002/sh; exactoriginalcorpus /Users/qiangli/projects/poc/dhnt/bashpp-tests/tour/_content/tour/basics/basic-types.go. Meaningful3mode differentialtest andclassiccomplexrejection regression. Report API/protocol plan early, commitfocused workingpiece with properSprint118 Story+ID trailers. No subagents/push/closure,30minbounded. This is codeimplementation task notread-only. Parent6f0c4d9a31be.


Manager acceptance (2026-09-09): bounded complex scalar scope is complete. Reviewed implementation 2a3ce96f is included in runtime integration 005 and published integration 006a. Exact unchanged Tour basic-types plus complex64 arithmetic, conversions and builtins passed native/interpreted/compiled comparisons, with classic Bash++ rejection preserved. Independent GoSource and race checks passed; publication-006a make test and make build passed. Durable receipts: sprint118-evidence/runtime-integration-006 and sprint118-evidence/publication-006a. General complex aggregate/nonfinite behavior and full corpus completion are outside this bounded story; their failures remain on the open parents.
