---
id: a6be199aa493
kind: feature
title: 'bash++ L1: typed records + := tuple-return with auto-bound err'
status: triaged
stage: code
reporter: qiangli
created: 2026-07-13T11:16:34.736832Z
weave: 7
---

Build on the landed L0 foundation (LangBashPP dialect variant in syntax; Object ValueKind + dialect seam in expand/object.go; interp gating + auto-JSON in interp/bashpp.go). Implement L1 per bashy/docs/bash-plus-plus-design.md: (1) TYPED RECORDS via a two-word / declare form — a structured record value carried in the existing Object ValueKind; (2) the := OPERATOR with tuple-return binding an auto 'err' variable, mapping Go's (val, err) — e.g. 'content, err := readFile config.json'. Both MUST be gated on the LangBashPP dialect (zero effect under default bash, like L0). HARD RULES: (a) supersetness is MEASURED — macOS 'make test-bash-parallel' stays 86/86 (the moat), non-negotiable; every bash-5.3 fixture is unchanged under the default dialect; (b) add focused Go tests in interp/expand/syntax (mirroring the L0 bashpp_test.go / object_test.go pattern) proving the new forms work under LangBashPP and are inert otherwise; (c) brand-neutral, self-contained. Scope is L1 ONLY (typed records + :=); L2 (go routine/channels) and L3 (reflect bridge) are separate later issues. See the design doc's 'Open questions' on the error model before finalizing := semantics.
