---
id: 04cff9c9e6f1
kind: task
title: 'Adopt Go 1.27 for Bash++'
seq: 6
status: done
priority: p0
created: 2026-09-02T23:10:00Z
sprint: 109
---

Run the Bash++ language engine, CLI, release builders, and CI on Go 1.27.x.
Preserve the Go 1.26.5 module compatibility floor until Bash++ requires a Go
1.27 language feature. Replace the Darwin signal-disposition dependency on the
private `runtime.sigaction` symbol with the public syscall boundary, and verify
the complete sh and Bashy suites plus cross-platform builds.

Completed with local Go 1.27.1 engine, CLI, vet, build-tag, and cross-build
gates green. GitHub Actions provide the final multi-platform acceptance gate.
