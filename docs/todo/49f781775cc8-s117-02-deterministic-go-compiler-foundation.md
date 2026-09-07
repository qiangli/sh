---
id: 49f781775cc8
kind: task
title: 'S117-02: deterministic Go compiler foundation'
seq: 40
status: done
priority: p0
created: 2026-09-07T17:54:06.019805Z
weave: 2
assignee: claude-opus5
sprint: 117
closed: 2026-09-07T18:25:40.309785Z
---

Build a new public lowering package consuming positioned syntax AST and static checker into deterministic ordinary Go with source mappings. Deliver actual executable vertical slice: typed declarations/function/call/result plus explicit dynamic shell boundary. No whole-program interpreter wrapper and no reparsing to select dialect. Freeze API early for consumers and report it to sprint manager. Public Go-only dependencies; preserve current profile and agentic scope contract. Parent 59bc1d4ea772.
