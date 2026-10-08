---
id: 12be9967975f
kind: bug
title: 'Windows: bashy x.go (compiled whole-file Go route) fails ''The system cannot find the path specified'''
seq: 165
status: todo
priority: p0
labels:
    - windows
created: 2026-10-08T20:04:02.797454Z
sprint: 379
sprint_id: 908c2ac2-e7bc-55fe-86bd-d25046ac4684
sprint_title: 'bashy 1.0.0 feature list: bash + Bash# + Yoke'
---

bashy CI windows-latest on b70d765 (job 113506078441): every Go-source case of cmd/bashy TestSourceRoute fails in ~0.05s with code=2 output 'bashy: The system cannot find the path specified.' (go_extension, go_flag_*, go_module_directory, non-main_refusal, flag_beats_python_extension); the .bsh Go-content fence case passes, so the toolchain resolver works and the fault is in sh interp.RunCompiledGoFile / its callers on Windows (MkdirTemp, nearestGoModuleDir walk, build.Dir, program path, 8.3 short temp paths). S1 Windows lane saw the same ('native front source-route .exe lookup'). Reproduce on noviwin1.local (go only; build bashy with GOWORK=off; coordinate host with codex-gpt6-astra), fix at root with a Windows-run unit test in sh/interp, then bashy CI windows TestSourceRoute green. Blocks S3 3-OS gate and ci-green.

Worker rules (Sprint 379 conductor, 2026-10-08): work in the submodule named above; commit and push INSIDE it to its default branch (public repos: main), then stop - the conductor bumps the umbrella pin. Every commit carries Sprint: #379 plus Story/Story-ID trailers for this story, with BASHY_AGENT set to your own binding. Reproduce first with a red unit test, fix at the root, never skip, quarantine or add to a known-failures list. This dev box (Dragon) is for builds and focused unit tests only. Windows runs go to noviwin1.local, Linux runs to the novidesign.local podman machine bashy or the repo CI, and macOS full suites to novidesign.local (all passwordless ssh; use the .local names). Gate before push: go build ./... and go vet on the module, plus the focused tests, with the exit code captured (never gate a push on a pipe). Report: the root cause, the commit SHA, the exact tests run and where, and any CI run URL.
