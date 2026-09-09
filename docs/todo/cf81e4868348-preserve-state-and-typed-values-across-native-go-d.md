---
id: cf81e4868348
kind: task
title: Preserve state and typed values across native Go dependency calls
seq: 50
status: done
priority: p0
created: 2026-09-09T03:46:48.115225Z
weave: 29
assignee: qiangli
sprint: 118
closed: 2026-09-09T04:53:59.53916Z
---

Sprint118 W7 critical runtime bridge vertical slice. Read /Users/qiangli/projects/poc/dhnt/docs/sprint-118-master-execution-plan.md. Original Go bytes must execute under Bash++ interpreter and lowerer; whole tested program must NOT be passed to go run or a second general Go interpreter. Existing bridge in sh/interp/bashpp_import.go/eval.go spawns native scalar/stateless calls; this breaks package state, structured arguments/results and callbacks. Design and implement smallest sound shared native dependency bridge vertical slice preserving package state and typed identity; demonstrate original Go fmt structured values, os.Setenv/os.Getenv state, math/rand state and an imported callback if feasible. Report architecture and exact gaps quickly before broad implementation. Own ONLY interp/bashpp_import.go, interp/bashpp_eval.go and NEW interp bridge files/tests; frontend worker owns gosource, interp/gosource.go, bashpp_p1.go, bashpp_func.go and lower.NewModuleImporter. Existing source can be read at /Users/qiangli/.bashy/weave/sh-7e2e7b65/workspaces/issue-28; committed feature197aabcd and ee4476c9. No shared source changes outside your isolated workspace, no subagents, no pushes/closure. Commit named files with Sprint: #118 and Story plus Story-ID trailers. Use GOMAXPROCS=2, Go -p 2 and focused tests only. Check bashy inbox --as s118-nativebridge at each work boundary and heed manager updates. Parent6f0c4d9a31be. Need actual dependency operations through runtime; native go test alone is not product proof.

Review and acceptance,2026-09-08:
Merged6fc00264. Manager native bridge race gate passed: persistent package state, typed values/handles, module init/argv/runtime cwd, cancellation and public child session isolation. Canonical120+33 regressions and CLI72probes passed. This is the scoped dependency bridge slice; imported callback reentry, general generics/mutable aggregates and full panic semantics remain explicit parent work (interp/bashpp_native_bridge.md).
