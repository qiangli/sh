# S562 E6 — Python handle iterators

A returned authenticated Python handle is iterable directly with Bash# range:

```bash
for item := range handle { ... }
for item, err := range handle { ... }
```

The first form yields one item per iteration. The second is the explicit domain-error opt-in: successful iterations bind a nil error, while a Python exception raised during `iter()` or `next()` produces one final iteration with a zero/nil item and the typed error. Transport, cancellation, stale-generation, and ownership failures remain failures and never enter `err`.

The adapter opens `iter(handle)` only when range starts and uses the existing foreign `Iterator` lifecycle. `StopIteration` is normal EOF and never an error or an extra iteration. Every exit closes the worker iterator; in particular, `break`, return, cancellation, and an iteration error release generators and run their Python `close()` cleanup. Handles remain authenticated by their owning module and worker generation, and nested iteration is supported because each range owns a distinct worker iterator.

Built-in Python lists normally cross the bridge by value; the handle-path list case is a returned iterator over a list. Opaque iterables such as generators and `dict_items` use this adapter directly. A handle whose object is not iterable fails with the Python `TypeError` diagnostic (or enters the optional error binding as a domain error).

Verification: the polyglot and interpreter selections passed. The requested lower selection exceeded Go's default 10-minute package timeout while an existing parity case had run for 9 seconds; the same selection passed with the repository-prescribed `-timeout=30m` in 950.350s. Focused interpreted/lowered handle-iterator tests and `go test ./lower/shellrt/...` also pass.
