---
id: 01a0e74e-c421-7971-837a-fd6950d856a6
seq: 27
form: page
type: lesson
title: Prove callback interface dispatch by source-visible method superset
description: When dependency callback lifetime proof reaches a tainted interface method with no concrete receiver binding, prove every source-visible method with that name using an escaped synthetic receiver; this admits recursive synchronous interface dispatch while preserving fail-closed refusals for field/global retention and async callback use. The name-based candidate set is only sound behind two gates - a selector based on an imported package name is a package-level native call, and the name must be declared by some source-visible interface type - otherwise a native target sharing a method name with clean source code is admitted unsoundly. Treat method values over callback-bearing receivers as retaining the whole receiver, record the bound declaration, and prove that body with the captured receiver when the value is called; a tainted callee with no resolvable body refuses.
status: candidate
source:
    tool: codex-gpt-5.5-k
    host: dragon
    episode: weave-issue-219
created: "2026-09-28T09:18:14Z"
---
