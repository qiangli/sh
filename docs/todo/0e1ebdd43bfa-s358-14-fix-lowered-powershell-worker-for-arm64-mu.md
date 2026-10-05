---
id: 0e1ebdd43bfa
kind: bug
title: S358.14 Fix lowered PowerShell worker for arm64 musl image runtime
seq: 164
status: done
priority: p0
labels:
    - bashsharp
created: 2026-10-05T05:37:46.488629Z
weave: 2
assignee: claude-opus5.5
sprint: 358
sprint_id: 59941ed8-d6fb-50fb-8ad3-4ffb362c155e
sprint_title: 'PowerShell and C# fences: Windows users at home on every OS'
closed: 2026-10-05T05:42:55.456825Z
resolution: fixed
closed_by: claude-opus5.5-b
---

Frozen candidate blocker discovered in S358.10: bashy 536e825 / sh 8c60f783b arm64 FROM-scratch --with go,pwsh image transpiles and builds exact script/sprint-358-both-smoke.bsh in /work. Interpreter in both arm64 lean and --with pwsh images prints expected SHA-256 5e0a962c... . The lowered binary in both --with go,pwsh and --with pwsh images exits 1 with load PowerShell module: EOF, assignment mismatch, and no values. Generated EnvironmentPlan.Executable is /opt/bashy/bin/dotnet-runtime-musl/10.0.12/dotnet, while Runtime is pwsh, Manager pwsh; framework-dependent arm64 route likely needs pwsh.dll argument or other launch plan lost in lowering. Reproduce with exact fixture; fix source in sh/polyglot/lower as appropriate, add focused regression; preserve x64/macOS/Windows host parity. Deliver commit with Sprint #358 and this Story-ID, no direct edits to umbrella. S358.10 final image gate remains open.
