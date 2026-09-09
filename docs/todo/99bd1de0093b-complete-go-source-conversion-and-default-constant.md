---
id: 99bd1de0093b
kind: task
title: Complete Go source conversion and default constant lowering
seq: 53
status: doing
priority: p0
created: 2026-09-09T06:37:55.781661Z
assignee: s118-go-frontend
sprint: 118
---

Sprint118 candidate002 realfailures inbothmodes: examples/base64-encoding anddirectories []byte/string conversions unsupported *ast.ArrayType. Lowered examples/constants prints600000000000 whereasnative6e+11 due untyped constant defaulttype. Generics andtypedeclarations maystillbeunsupported; fix directfrontend/lowerblocking subsets basedactualunchangedsource plus full97Tourlower failures. Own ONLY gosource/* lower/gosource.go andotherlowerfileswhenneeded, syntaxmetadata onlynotify beforeedit. No interp/nativebridge/concurrencyfiles (otherworkers). Reproduce exactupstreamsource unchanged; add meaningfulthree-mode tests possibleusingcurrentinterp; preserve117120+33focusedsuite. No fullsource forwarding/nativego runinterpreter. CommitproperSprint118 Story+ID no subagents/push/closure.30minbounded shipArrayType conversion+constantlower first, thenreportgenericsplan. Parent6f0c4d9a31be.
