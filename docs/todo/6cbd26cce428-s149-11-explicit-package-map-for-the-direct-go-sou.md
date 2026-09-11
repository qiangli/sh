---
id: 6cbd26cce428
kind: task
title: S149.11 explicit package map for the direct Go-source interface (gosource)
seq: 90
status: todo
priority: p0
created: 2026-09-11T16:08:41.055697Z
sprint: 149
---

Per docs/bashpp-import-resolution.md section 3.2. gosource.Options gains Packages (ordered PackageSpec{Path, Sources}) and ImportBase. A map importer (types.ImporterFrom) resolves relative imports as path.Join(ImportBase, rel) only when ImportBase is set, otherwise rejects them with gc wording; looks up the explicit map first, then the caller importer; never touches the filesystem; records every resolution (from, import, path, origin, name, files) on Program.Resolutions in deterministic order. Dependency packages are type-checked in the given order and registered under their Path. lower.Options.Importer lets lowering reuse the same importer. Tests: relative import with/without base, map-before-fallback precedence, chain a<-b<-c, deterministic resolution order, diagnostics attribution.
