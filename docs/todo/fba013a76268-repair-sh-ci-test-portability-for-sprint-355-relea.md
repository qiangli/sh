---
id: fba013a76268
kind: bug
title: Repair sh CI test portability for Sprint 355 release sibling
seq: 157
status: todo
priority: p0
labels:
    - windows
    - ci
    - linux
created: 2026-10-02T04:33:51.282151Z
sprint: 355
sprint_id: 3a83ff48-7f8b-5be4-b0e6-e146762b2573
sprint_title: Profile D residual blocker triage and targeted closure
---

Sh 029c69d fails Windows go vet because imported-instances cache test copies Runner with atomic noCopy and declare-f escaped-newline test depends on Unix-only runScript helper and sed. Ubuntu full-tag vet also fails a second Runner copy in gosource S281 metadata test. Replace test-only Runner copies with the supported Subshell clone path, scope sed test to Unix, verify focused tests, full-tag vet, and Windows vet/cross-build, and push a separate sh test-only commit. Acceptance: Windows and Ubuntu CI no longer report these diagnostics; fd1 provenance production commit remains separate.
