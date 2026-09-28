---
id: 01a0e76f-a579-76c2-b770-935cd2b7c8e6
seq: 28
form: page
type: lesson
title: Prove callback interface dispatch from observed receiver provenance
description: When a native method receives an original callback through interface dispatch, inspect the authenticated receiver graph under strict bounds, require every observed concrete method receiver to belong to the selected source package, and prove exactly those method bodies. Method-name enumeration alone is unsound because external embedding can override an interface method; unknown, foreign, oversized, or source-invisible graphs must refuse. Method values retain their full receiver and calls must prove the bound method body.
status: candidate
evidence: Sprint 319 story 00446a7bd51a; focused TestS319CallbackProof* and TestS319ConstraintEval*
source:
    tool: codex-gpt5.6-sol-k
    host: dragon
    episode: weave-issue-219
created: "2026-09-28T09:54:08Z"
updated: "2026-09-28T09:54:51Z"
supersedes: prove-callback-interface-dispatch-by-source-visible-method-super
---

A source-visible interface does not close its implementation set, even when it contains an unexported marker: another package can embed the interface and override an exported method. Therefore a callback lifetime proof may not infer dynamic targets from method names.

For dependency-owned receivers, use authenticated runtime provenance: perform a read-only, depth/node-bounded walk of the concrete receiver graph, collect dynamic types that implement the selected method, reject any unnamed or cross-package type, then prove every observed source method with escaped synthetic receivers. Propagate that observed set only through fields declared as the interface type. Direct method values must retain the captured receiver, record the bound declaration, and prove that declaration when invoked. Any lost binding or unresolved target remains fail-closed.

Regression coverage must include the exact dependency bridge shape plus foreign overriding implementations both at the root and nested beneath a legitimate source type, alongside field/global retention and asynchronous invocation.
