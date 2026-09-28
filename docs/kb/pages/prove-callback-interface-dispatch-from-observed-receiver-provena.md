---
id: 01a0e76f-a579-76c2-b770-935cd2b7c8e6
seq: 28
form: page
type: lesson
title: Prove callback interface dispatch from observed receiver provenance
description: 'When a native method receives an original callback through interface dispatch, inspect the authenticated receiver graph under strict bounds and prove only observed same-package concrete method bodies. Treat that type set as entry-snapshot provenance: invalidate it after opaque interface-field replacement or whenever an alias crosses code that may mutate it. Unknown, foreign, escaped, oversized, source-invisible, or lost method-value provenance must refuse.'
status: candidate
evidence: Sprint 319 story 00446a7bd51a; commits c49e3b31 and dfe69871; focused TestS319CallbackProof*, TestS319Manager*, TestS319ConstraintEval*, and TestS281OnceValueValueSliceResult
source:
    tool: codex-gpt5.6-sol-k
    host: dragon
    episode: weave-issue-219
created: "2026-09-28T09:54:08Z"
updated: "2026-09-28T10:06:14Z"
supersedes: prove-callback-interface-dispatch-by-source-visible-method-super
---

A source-visible interface does not close its implementation set, even when it contains an unexported marker: another package can embed the interface and override an exported method. Therefore a callback lifetime proof may not infer dynamic targets from method names.

For dependency-owned receivers, use authenticated runtime provenance: perform a read-only, depth/node-bounded walk of the concrete receiver graph, collect dynamic types that implement the selected method, reject any unnamed or cross-package type, then prove every observed source method with escaped synthetic receivers. Propagate that observed set only through fields declared as the interface type.

The observed set is an entry snapshot, not a permanent closed-world fact. A direct assignment to an interface field must replace its provenance with the tracked RHS; an opaque RHS has no admissible dynamic types. If a receiver or interface-field alias crosses a call or other escape boundary, code outside the active proof may replace the dynamic value, so snapshot-derived dispatch provenance must be invalidated before any later callback-bearing call. Exact source-visible concrete replacements may still be proved by their concrete method body.

Direct method values must retain the captured receiver, record the bound declaration, and prove that declaration when invoked. Any lost binding or unresolved target remains fail-closed.

Regression coverage must include the exact dependency bridge shape, foreign overriding implementations both at the root and nested beneath a legitimate source type, direct opaque replacement, helper-mediated replacement, pointer alias mutation, callback retention, and asynchronous invocation.
