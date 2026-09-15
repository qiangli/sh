---
id: 0fec539dae0b
kind: task
title: 'S190.2 native fences: project include roots serve quoted includes only (-iquote)'
seq: 122
status: done
priority: p0
created: 2026-09-15T10:41:53.016694Z
assignee: transom
sprint: 190
closed: 2026-09-15T11:19:05.616932Z
closed_by: transom
---

Sprint 190 measured a ~~~cxx fence against tesseract: the checkout root was passed as -I, so on macOS its VERSION file resolved <version> (pulled in by <string>) and every C++ fence in that checkout failed to compile. Fix: -iquote for the source dir + project root (quoted includes only, the documented contract); regression test with a root file literally named version (fails on any filesystem under -I); docs/bashpp-polyglot-fences.md. Gate: go test ./polyglot ./interp ./syntax ./lower; bashpp-tests polyglot-gate C/C++ rows on the installed binary.
