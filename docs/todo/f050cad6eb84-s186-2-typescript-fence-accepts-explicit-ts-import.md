---
id: f050cad6eb84
kind: task
title: S186.2 TypeScript fence accepts explicit .ts import specifiers
seq: 119
status: done
priority: p1
created: 2026-09-15T08:10:31.418245Z
assignee: transom
sprint: 186
closed: 2026-09-15T08:17:19.116266Z
closed_by: transom
---

The fence's checking program uses fixed compiler options and does not read the project's tsconfig.json, so a relative import naming its .ts file explicitly (the spelling Node's native type stripping requires) was refused with TS5097 even where the project enables allowImportingTsExtensions. In the modern (ESM) analysis path the artifact comes from transpileModule, so the checking program sets noEmit + allowImportingTsExtensions. Test: TestTypeScriptAnalyzeRelativeTsExtensionImport (red without the fix).
