---
id: b6d2627fd3b6
kind: task
title: S183.4 — extend the Python worker with object handles
seq: 116
status: done
priority: p0
created: 2026-09-15T00:07:55.383977Z
assignee: codex-gpt5.6-sol
sprint: 183
closed: 2026-09-15T02:20:41.327802Z
closed_by: codex-gpt5.6-sol
---

Extend the existing sh/polyglot persistent CPython worker and only the existing interp/lower seams with lazy import, positional plus Bash++ `name: value` kwargs, call/getattr/release, by-value scalar/bytes/JSON data, opaque generation-scoped handles, direct callable-handle invocation, public attributes plus the required `__name__` introspection attribute, cancellation invalidation, constructors/methods/chained attributes, deterministic diagnostics, and interpreted/native parity. Use a dedicated protocol pipe and per-operation Python plus fd-level stdout/stderr capture so package/native output cannot corrupt frames. Refuse other underscore-prefixed attributes. No second loader, C API, async, callbacks/proxies, mutation, pickling, or package installation. Gate focused polyglot/interp/lower tests and the full sh guard. Depends on S183.3. Sprint: #183.
