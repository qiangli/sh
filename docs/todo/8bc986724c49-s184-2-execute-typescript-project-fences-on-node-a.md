---
id: 8bc986724c49
kind: task
title: 'S184.2: execute TypeScript project fences on Node and Bun'
seq: 118
status: todo
priority: p0
created: 2026-09-15T05:25:35.698697Z
sprint: 184
---

## Setup

Work only in the isolated `sh` workspace. Read `AGENTS.md` and `CLAUDE.md`.
Base is current master after Sprint 183. Do not modify
`ycode/priorart/opencode` or invoke `npm install`, `pnpm install`, or
`bun install`.

## Goal

Deliver the Sprint 184 MVP vertical engine slice:

- Extend the existing `polyglot.EnvironmentPlan` discovery for language
  `typescript`/`ts`. Source-relative read-only discovery finds `package.json`,
  `tsconfig.json`, and `package-lock.json`, `pnpm-lock.yaml`, `bun.lock`, or
  `bun.lockb`. Manager selection recognizes npm, pnpm, and Bun but never
  selects the runtime.
- `BASHPP_TYPESCRIPT_RUNTIME` selects Node (default) or Bun;
  `BASHPP_NODE`/`BASHPP_BUN` select the executable.
  `BASHPP_TYPESCRIPT_MODULE` is the explicit compiler override; otherwise
  prefer project/workspace-local official TypeScript.
- Treat a TypeScript fence as ESM: admit imports and ordinary module
  initialization needed by imported functions; discover exported functions;
  load lazily under the selected Node/Bun worker; automatically await promises;
  retain the current scalar/bytes/JSON by-value codec and explicit rejection of
  unsupported identity objects.
- Use protocol framing that user/module stdout cannot corrupt.
- Pass the identical environment/runtime selection through `interp` and
  `lower`, proving interpreted/generated parity.
- Preserve existing synchronous TypeScript and Python behavior.

## MVP non-scope

Do not add direct `import typescript ...` syntax, named source environments,
JavaScript object/class handles, get/construct/release, source maps, cache/pool
frameworks, preloads, project-reference matrices, installation/env-sync,
callbacks, async iterators, hot reload, or mixed runtimes in one execution.

## Known traps

`EnvironmentPlan` has a generic shape but discovery currently rejects
non-Python languages. TypeScript currently emits CommonJS/Node10, evaluates in
Node `vm`, rejects imports/module initialization, and rejects promises.
OpenCode is Bun-only evidence: Node resolves its export but fails an unchanged
extensionless internal import. Installed Bun is 1.3.9 while `packageManager`
declares 1.3.14; never conflate them. Avoid project-tree writes;
materialization, if needed, must be outside the imported checkout and cleaned
safely.

## Tests and gate

Add hermetic unit/integration tests with pre-materialized tiny npm, pnpm, and
Bun workspace layouts. Prove runtime/manager independence, Node default,
explicit Bun, async await, stdout framing, missing runtime/module errors, and
interpreted/native parity.

```sh
go test ./polyglot ./interp ./lower -run 'TypeScript|Environment|PythonFence'
```

Stage named files only. Commit with:

```text
Sprint: #184
Story: #118
Story-ID: 8bc986724c49
```

If blocked after three honest attempts, commit the useful partial and add a
concise `S184-BLOCKERS.md`.
