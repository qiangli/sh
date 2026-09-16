---
id: cd4d88919000
kind: task
title: S196 match Bash declaration quoting for escape bytes
seq: 134
status: done
priority: p0
created: 2026-09-16T11:22:29.851199Z
assignee: codex-gpt5.6-sol
sprint: 196
closed: 2026-09-16T12:26:45.216472Z
closed_by: codex-gpt5.6-sol
---

Installed Bash declaration output formats ESC as octal while GNU Bash 5.3 emits the canonical ANSI-C E escape. A pinned downstream shell-environment decoder depends on Bash declaration formatting. Preserve shell round-trip bytes and match native declaration quoting without changing generic syntax.Quote behavior or downstream fixtures. Add native-differential regressions for export/declare including literal backslash-octal text and mixed controls.

Implementation: declaration/export/readonly formatting now uses GNU Bash's
ANSI-C `\E` spelling for ESC. Escape-aware conversion preserves literal
backslash-octal text, neighboring control bytes, and numeric suffixes. The
generic `syntax.Quote` API remains unchanged. Both old and new output can be
reimported by a shell; this repair addresses observable output compatibility,
not loss of the original variable bytes.

Focused verification: `go test ./interp -run '^TestDeclarationEscapeByte$'
-count=1 -timeout=2m` PASS (0.523s), including the installed GNU Bash 5.3.15
oracle, export/declare/readonly output, and reimported byte equality. Final
installed integration gates remain parent-owned and pending.
