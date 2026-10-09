---
id: 12be9967975f
kind: bug
title: 'Windows: bashy x.go (compiled whole-file Go route) fails ''The system cannot find the path specified'''
seq: 165
status: done
priority: p0
labels:
    - windows
created: 2026-10-08T20:04:02.797454Z
assignee: codex-gpt6-astra
sprint: 379
sprint_id: 908c2ac2-e7bc-55fe-86bd-d25046ac4684
sprint_title: 'bashy 1.0.0 feature list: bash + Bash# + Yoke'
closed: 2026-10-08T21:18:30.205063Z
closed_by: claude-opus5.5
---

bashy CI windows-latest on b70d765 (job 113506078441): every Go-source case of cmd/bashy TestSourceRoute fails in ~0.05s with code=2 output 'bashy: The system cannot find the path specified.' (go_extension, go_flag_*, go_module_directory, non-main_refusal, flag_beats_python_extension); the .bsh Go-content fence case passes, so the toolchain resolver works and the fault is in sh interp.RunCompiledGoFile / its callers on Windows (MkdirTemp, nearestGoModuleDir walk, build.Dir, program path, 8.3 short temp paths). S1 Windows lane saw the same ('native front source-route .exe lookup'). Reproduce on noviwin1.local (go only; build bashy with GOWORK=off; coordinate host with codex-gpt6-astra), fix at root with a Windows-run unit test in sh/interp, then bashy CI windows TestSourceRoute green. Blocks S3 3-OS gate and ci-green.

Worker rules (Sprint 379 conductor, 2026-10-08): work in the submodule named above; commit and push INSIDE it to its default branch (public repos: main), then stop - the conductor bumps the umbrella pin. Every commit carries Sprint: #379 plus Story/Story-ID trailers for this story, with BASHY_AGENT set to your own binding. Reproduce first with a red unit test, fix at the root, never skip, quarantine or add to a known-failures list. This dev box (Dragon) is for builds and focused unit tests only. Windows runs go to noviwin1.local, Linux runs to the novidesign.local podman machine bashy or the repo CI, and macOS full suites to novidesign.local (all passwordless ssh; use the .local names). Gate before push: go build ./... and go vet on the module, plus the focused tests, with the exit code captured (never gate a push on a pipe). Report: the root cause, the commit SHA, the exact tests run and where, and any CI run URL.

Conductor 2026-10-08 13:40 PDT: sh c37be42 (bootstrap ignores a missing build GOROOT) fixed noviwin1 ('bashy x.go a b' prints compiled) but bashy CI windows-latest on bashy 3e9866b (pins sh c37be42) STILL fails TestSourceRoute (cmd/bashy/source_route_test.go) for every Go case, including the non-main refusal: code=2 'bashy: The system cannot find the path specified.' (run 37837189439 job 113517119004). So there is a second root cause that is present on the hosted runner and absent on noviwin1. Differences to check: the test builds with -tags bashy_core; the cwd is a t.TempDir() under the runner's short-path TEMP (C:\Users\RUNNER~1); HOME/USERPROFILE/LOCALAPPDATA/GOCACHE/GOPATH setup from setup-go; GOROOT points at the toolcache. Reproduce on noviwin1 by running exactly that test (go test -run TestSourceRoute ./cmd/bashy with GOWORK=off from a copied bashy tree, or with the runner's env shape), get the failing syscall/path (the error string is ERROR_PATH_NOT_FOUND - add the path to the error), red unit test in sh, fix at the root, then let bashy CI windows prove it.


Worker rules (Sprint 379 conductor, 2026-10-08): work in the submodule named above; commit and push INSIDE it to its default branch (public repos: main), then stop - the conductor bumps the umbrella pin. Every commit carries Sprint: #379 plus Story/Story-ID trailers for this story, with BASHY_AGENT set to your own binding. Reproduce first with a red unit test, fix at the root, never skip, quarantine or add to a known-failures list. This dev box (Dragon) is for builds and focused unit tests only. Windows runs go to noviwin1.local, Linux runs to the novidesign.local podman machine bashy or the repo CI, and macOS full suites to novidesign.local (all passwordless ssh; use the .local names). Gate before push: go build ./... and go vet on the module, plus the focused tests, with the exit code captured (never gate a push on a pipe). Report: the root cause, the commit SHA, the exact tests run and where, and any CI run URL.
