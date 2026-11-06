# Explicit C# assembly assets

Sprint 562, Story-ID `7c396c9fcfa1`. The manager approved the bounded design
recorded in `csharp-assembly-assets-proposal.md`; this document describes the
implemented interface. Native fixture acceptance remains a separate gate.

Select a graph explicitly in the existing project overlay:

```json
{"language":"csharp","runtime":"pwsh","csharp_assemblies":"assets/graph.json"}
```

YAML and named environments accept the same `csharp_assemblies` field. The path
is relative to the overlay directory. Asset paths are relative to the manifest.
Other languages reject this setting. Nothing discovers adjacent DLL files.

A minimal manifest is:

```json
{
  "schema": "csharp-assembly-graph-v1",
  "root": "consumer",
  "sources": [
    {"id":"base","path":"Base.cs","sha256":"<SHA256 of exact file bytes>"},
    {"id":"consumer","path":"Consumer.cs","sha256":"<SHA256 of exact file bytes>"}
  ],
  "keys": [],
  "nodes": [
    {"id":"base","name":"Assembly1","version":"0.0.0.0",
     "culture":"neutral","public_key_token":"null","output":"base/Assembly1.dll",
     "sources":["base"],"references":[]},
    {"id":"consumer","name":"Assembly2","version":"0.0.0.0",
     "culture":"neutral","public_key_token":"null","output":"consumer/Assembly2.dll",
     "sources":["consumer"],"references":[{"node":"base","aliases":["global"]}]}
  ]
}
```

The root's source files are complete, unmodified C# compilation units. The C#
fence supplies a separate observer/callable unit, normalized by the existing
fence compiler. This keeps official sources independent of the observer. Root
source assets must not also be copied into the fence. Each asset and the observer
participate separately in the build identity. Observer `#line` filenames become
`observer.bsh` for relocation-stable identity; asset diagnostics retain their
manifest-relative filenames and original line numbers.

Nodes compile in deterministic dependency order against precisely their direct
references and the pinned provider's `ref/*.dll` framework set. `global` gives
normal visibility; other aliases require `extern alias` in source. Aliases do
not imply transitive references. Two versions of a simple name are permitted
for compilation; successful execution with multiple versions of one simple name
is explicitly unsupported. Duplicate full identities are rejected.

The graph-only Roslyn path fixes C# 12, library output, unsafe enabled, nullable
annotations disabled, release optimization and deterministic emission. Source
assembly attributes remain unchanged. Each emitted name/version/culture/token
must equal the manifest. Only neutral culture is supported in v1. Signed nodes
name a key asset by `key`, assert a public-key token, and use full signing with
both delay signing and public signing disabled. Keys for public fixtures must be
explicitly non-secret test material; no signing credentials are discovered.
Compiler errors retain stage, ID, severity, logical file, line, column and message
through `CSharpGraphError`. A missing prerequisite is not a root CS1705 result.

Admission rejects unknown/duplicate JSON fields, invalid UTF-8 source, duplicate
IDs, missing/unreachable assets, cycles, invalid aliases, portable-path violations
and case-folded collisions. Limits are 32 nodes, 128 sources, 128 edges, 32 keys,
and 8 MiB aggregate source/key bytes. Files are bounded regular-file reads under
`os.Root`; symlink path components are rejected. Manifest hashes authenticate
content consistency only. Official corpus provenance requires the fixture owner's
independent pinned source inventory.

Discovery embeds the admitted bytes in immutable JSON strings in EnvironmentPlan
and Plan; lowering emits these strings into the artifact. Execution does not read
the original manifest, sources or keys. The existing no-graph Add-Type path and
shared PowerShell prelude are unchanged.

The separate cache key hashes the embedded graph, normalized observer, compiler
adapter, closed options, OS/architecture, provider executable/runtime files and
all framework reference contents. It does not trust size/mtime memoization or
absolute source paths. A private staging build checks every PE identity before
publishing a receipt with all output hashes and exports. Every reuse verifies
all output hashes and asserted identities. Producers serialize via a per-key
lock; a stale lock fails after two minutes instead of admitting partial output.
Corrupt entries are replaced as whole directories. The cache is a consistency
mechanism, not an independent security authority. Graphs load into a worker-owned
AssemblyLoadContext, with declared dependency paths supplied explicitly.

## Targeted gates

Portable validation: `go test ./polyglot -run 'TestCSharpGraph(Validation|Snapshot|ContentIdentity|Overlay)'`.

With pinned `pwsh` on PATH, run `go test -tags full ./polyglot -run TestCSharpGraph`
and `go test -tags full ./lower -run TestCSharpAssembliesBothModesWithoutSourceAssets`.
The latter checks interpreted output, removes the asset directory and project
configuration, and then builds/runs the lowered artifact in a fresh environment.

`gate-csharp-assemblies.ps1` stages the native Windows product gate with explicit
source/provider/SDK/scratch arguments. It checks named test PASS events and rejects
skips, failed events and empty selections. It uses fresh private caches. Native
protected-internal and signed CS1705 fixture proof is owned by the fixture helper;
these synthetic regressions do not replace that official-source proof.

For official fixtures, set `BASHPP_CSHARP_OFFICIAL_GRAPHS` to a fixture-owner
verified root containing `protected/` and `cs1705/`. Each directory supplies its
own `bashpp.json`, manifest/assets and `observer.bsh`. The negative consumer node
must be named `root`; `cs1705/expected-diagnostic.json` records native
`stage` and a `diagnostics` array from the native receipt. The array must retain
both identical CS1705 errors, each with `location: "none"`, null/empty path,
line/column 1 and the complete native identity message; no source path is invented. Run `go test -tags full ./lower -run '^TestCSharpOfficialGraphsBothModes$'`.
This checks output `2` in both modes and requires structured root-stage CS1705,
then checks both product modes fail during compilation with that diagnostic.
The fixture owner must validate source-pin binding and native receipts before
this product gate; a manifest alone does not establish upstream authenticity.

The load rebuild now receives the operation context through an optional runtime
load hook. Cancellation while waiting for a publisher lock terminates loading
and closes the newly started worker. Portable lock/deadline and actual-provider
load regressions cover this path.

Both gate wrappers require an expected commit marker, verify the staged source
SHA256 inventory, and compare Go/PowerShell executable digests to caller-supplied
pins before running tests. Stage markers and inventories must be generated from
the manager-approved git archive. `CacheSeed` / `--cache-seed` optionally copies
`gomod` and `go-build` into fresh private scratch; tests never write to the seed.
The Linux wrapper is `gate-csharp-assemblies.py`; Windows uses the PS1 wrapper.
Official fixture arguments remain required for official-source acceptance.

The independently produced native fixture package placed `graph.json` under
`assets/` while its declared paths already began with `assets/`. The retained
d466 product gate therefore failed admission with `assets/assets`; no compiler
result was reached. Do not add a runtime path fallback. For that exact package,
run `prepare-csharp-official-graphs.py --source ORIGINAL --output FRESH` and pass
FRESH to the product gates. The adapter moves the unchanged manifest to each
fixture root and updates only `bashpp.json`. Its packaging receipt records all
input/output hashes; sources, keys, observers and native diagnostics stay exact.
The original fixture tree and failed gate evidence remain untouched. This
packaging step supplies no new native or upstream-authentication claim.
