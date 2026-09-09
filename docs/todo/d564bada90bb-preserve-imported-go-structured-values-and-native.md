---
id: d564bada90bb
kind: task
title: Preserve imported Go structured values and native process semantics
seq: 52
status: doing
priority: p0
created: 2026-09-09T06:37:55.717046Z
weave: 31
assignee: s118-native-types
sprint: 118
---

Sprint118 candidate002 failures: undefined imported types atomic.Uint64/sync.Mutex/embed.FS; native []string result cannot range(os.Environ); os.Args indexing/slicing treatedaspackage structuredselector; nativehandles f non-scalar(defer file); flag.IntVar address argument unsupported; os.Exit(3) mappedtointerpreterexit1 and syscall.Exec/EOF opaque. Implement coherent native structuredvalue/type bridge verticalslice using existingpersistentdependencyprocess; originalbody remainsinterpreted. Own interp/bashpp_native* files, interp/bashpp_import.go, interp/bashpp_eval.go, interp/gosource.go, newtests. No gosourcefrontend/lower/channel files; sharedp1/scalar/functionneeds coordinate manager first. Do notrewriteexamples/wholeprogramnativeforward. Differential unchangedrows and lifecycle/race tests, nofakePASS orblanketexceptions. Originalsources canonical ../bashpp-tests/examples plusTour. Frozenbase /tmp/s118-published-002 forreproonly. Commit proper Sprint118 Story+ID, no push/closure/subagents;30min bounded send firstexactAPI/fileplan. Parent6f0c4d9a31be.
