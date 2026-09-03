# kb index

2 page(s). Search: `bashy kb search <query>` — check before starting a task; `bashy kb retro` after. Pages live under pages/.

- [replay-reader-bytes-when-rolling-back-streaming-parser-probes](pages/replay-reader-bytes-when-rolling-back-streaming-parser-probes.md) `validated/lesson` Replay reader bytes when rolling back streaming parser probes — When a speculative grammar probe can cross lexer buffer boundaries, snapshot the full parser state and record reads from the underlying reader; on rejection restore the snapshot and replay recorded bytes before the original reader, otherwise one-byte readers and diagnostic offsets diverge.
- [source-filename-must-not-redefine-shell-argv0](pages/source-filename-must-not-redefine-shell-argv0.md) `validated/gotcha` Source filename must not redefine shell argv0 — When dot/source temporarily switches Runner.filename for diagnostics and BASH_SOURCE, preserve the caller's effective /bin/zsh separately; otherwise a dual-use case guard treats a sourced file as directly executed.
