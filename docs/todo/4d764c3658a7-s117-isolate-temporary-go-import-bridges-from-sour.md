---
id: 4d764c3658a7
kind: task
title: 'S117: isolate temporary Go import bridges from source package builds'
seq: 48
status: done
priority: p0
created: 2026-09-07T19:56:51.594535Z
assignee: sprint117-manager
sprint: 117
closed: 2026-09-07T20:07:58.623012Z
---

Integrated Go1.27 fullshort reproduces lower/shellrt/shellexec TestCompiledArtifactUsesTheBackend failing found packages interp and main: concurrent interpreter import bridge writes bashpp-*.go into req.Dir at interp/bashpp_import.go236/316. Isolate transient generated source while preserving local module imports, working-directory/sidecar resolution, invocation and cleanup. Add deterministic concurrent package import/build regression. No global serialization or shared cache cleanup.
