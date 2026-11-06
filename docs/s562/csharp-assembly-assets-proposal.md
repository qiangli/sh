# C# explicit assembly assets: design review

Sprint 562, Story-ID `7c396c9fcfa1`. Status: bounded design approved by manager (mailbox 12137).
Implementation interface: `csharp-assembly-assets.md`. Native source context and fixture graphs
belong to `regression_prep`; parent owns Linux and native Windows acceptance.

## Observed baseline

Reviewed combined runtime `f1a41ac0f3dc21a441042bec8bd95cec4a4dbcc9`, including
C# `225d` plus fixture `7edf` and shared PowerShell `81` / typed-capture `5b`.
The isolated delivery branch has an older base; this delivery changes documentation
only and does not transplant runtime files from the combined checkout.

In `polyglot/csharp.go`, `unit` keys one generated source by OS/architecture and
toolchain content. `loadRequest` carries source, assembly path, and type.
`Compile-Fence` normalizes the source and invokes Add-Type with `/unsafe`, one
output library, and no explicit dependency references. Export cache admission
checks a nonempty DLL and export JSON, not a dependency closure. Analysis and
worker loading both need any future graph contract. `polyglot.Plan` currently
has no assembly graph; a runtime-only configuration field would leave compiled
mode incomplete.

## Source discovery receipt

The inspected upstream snapshot contains the following exact files. These hashes
identify the inspected bytes, **not** proof that the archive belongs to the required
upstream commit. The fixture owner must authenticate the upstream pin before use.
Fence hashes cover bytes after the opening fence through before the closing fence,
preserving original newlines. Paths are relative to `dotnet-docs/docs/csharp/`.

| Source | File SHA256 |
| --- | --- |
| language-reference/keywords/protected-internal.md | `277860ff7d30d16443ef40ae0a9fdbc04bbdd7154db5c71a1fb297b6257e1dff` |
| language-reference/compiler-messages/cs1705.md | `ce48124f339383340ed72f51c2cb215eab77c5ad9d1cc395ac5c18f4cc6fe15e` |

| Source / opening line | Fence SHA256 | Role |
| --- | --- | --- |
| protected-internal L67 | `9be96a3f191c5a9647c98dfba8ae280477fd19cde472880e1bde98d76d3d6af8` | Separate Assembly1 base and same-assembly derived class |
| protected-internal L88 | `7411c6979838dd05b474c1be7beeec590265d8e525a7e328496ccfd3bb450d7b` | Consumer explicitly referencing Assembly1.dll |
| cs1705 L29 | `7a67b164ab105866af0f57c468c645cb61475df0d768d4787cc0151342722e09` | CS1705 assembly major version one; other components zero |
| cs1705 L48 | `9989419a547e6dcd0a660636636f8191db8cb9988099df6acbd527d2da4267b3` | CS1705 assembly major version two; other components zero |
| cs1705 L67 | `c4a1d75136c89ad820058244041960173488f52fa395293abb9b1f014757af1c` | Intermediate assembly, extern aliases AssemblyA and AssemblyB |
| cs1705 L108 | `dcf18d1fca0cf8a1e18db7f5c41dbcdfcc52f4aefc6d3fb4e364b7b41d936945` | Consumer expected to fail with CS1705 |

L88 needs L67, not the different Assembly1 example at L17. No source concatenation
can stand in for the assembly boundary. CS1705 must fail at the consumer compile
stage after all three prerequisite builds succeed; missing-type, alias, signing,
or loading errors are not equivalent evidence.

## Proposed finite contract

Use a C#-specific `csharp-assembly-graph-v1` payload. No generic asset framework,
NuGet restore, arbitrary compiler argument strings, ambient DLL directories,
source-loading directives, or shell commands in metadata.

The exact user-facing declaration syntax is a manager decision; proposed ingress
is an explicit manifest path associated with one C# module in existing project
configuration. Do not infer it from the working directory or compiler comments.
Resolve once at prepare time into an immutable payload carried through analysis,
the plan, and lowering. A compiled executable embeds all admitted source/key bytes
and identities and can run after the original source tree is removed. Any changes
to shared configuration/plan structures require a separately reviewed narrow diff.

Fields and admission rules:

- `schema`, `root`, `sources`, `keys`, `nodes`. Reject unknown fields, duplicate
  JSON keys, duplicate IDs and invalid UTF-8. Proposed fixed limits: 32 nodes,
  128 sources, 128 edges, 8 MiB aggregate bytes. Limits are versioned semantics.
- Each source: stable ID, logical relative path, SHA256, exact bytes. Fixture
  binding additionally records upstream repository, immutable commit, file hash,
  fence opening/end lines and fence hash. Fixture admission checks these against
  the owner's independent pinned inventory; a self-declared digest alone does not
  authenticate provenance. Runtime accepts explicitly supplied user assets without
  pretending they are official corpus fixtures.
- Each node: ID, assembly simple name, expected four-component version, expected
  culture/public-key token, output relative path, ordered source IDs, direct
  references `{node, aliases}`, optional signing-key ID. Sources supply version
  attributes; the manifest asserts emitted identity rather than rewriting source.
  Nodes emit managed DLLs; the callable root uses existing fence normalization,
  while dependency compilation units retain their original assembly scope.
- Reference aliases are explicit, unique C# identifiers, with `global` reserved
  for normal visibility. Empty alias arrays are invalid. Aliases affect metadata
  references only; source `extern alias` directives remain source. Reject missing
  nodes, self-edges, cycles, duplicate reference entries, and unreachable assets.
- Normalize path spelling to portable slash-separated relative components.
  Reject absolute/drive/UNC paths, backslashes, empty/dot/dot-dot components,
  colons, NUL, Windows device names and trailing dots/spaces; reject case-folded
  collisions on every platform. Do not follow symlinks for asset ingestion.
  Read once, hash those bytes and compile those same bytes in a fresh private
  staging directory. Never resolve references against caller CWD or PATH.
- Signing material is explicit. These fixtures use a reproducible public test key,
  hashed as an asset and clearly marked non-secret; never acquire a developer's
  signing credentials. Both versions must share that key and emitted token.
  Full signing versus any public-sign alternative must match native proof;
  silently substituting unsigned assemblies is forbidden.

Topologically build prerequisites in stable ID order. Build closure is distinct
from compiler reference sets: a consumer sees exactly its declared direct edges
plus the pinned framework references. Never add transitive references or choose
the latest assembly version. Compiler options are a closed typed record covering
language version, unsafe, nullable, optimization, deterministic output and signing;
defaults must be explicit and hashed. Framework references are an enumerated,
content-hashed set from the pinned provider, not everything loaded in PowerShell.

## Two required graphs

| Graph node | Assembly/output | Direct references |
| --- | --- | --- |
| protected.base (L67) | Assembly1 / protected/Assembly1.dll | framework only |
| protected.root (L88) | Root / protected/Root.dll | protected.base as global |
| version.one (L29) | CS1705 v1 / v1/CS1705.dll | framework only; test key |
| version.two (L48) | CS1705 v2 / v2/CS1705.dll | framework only; same test key |
| version.bridge (L67) | CS1705c / bridge/CS1705c.dll | version.one as AssemblyA; version.two as AssemblyB |
| version.root (L108) | Root / consumer/Root.dll | version.one as global; version.bridge as global |

The last four nodes form a separate graph from the first two. Same simple name
with distinct version identities is legal; same full identity with differing
content is rejected. For runtime loading, bind only declared assets by full
assembly identity in a task-owned context and explicitly fail unsupported
same-name multi-version loading. CS1705 is compile-negative and requires no
multi-version runtime loader. Do not widen this story to implement one.

## Compiler and cache decisions for review

Keep existing no-graph Add-Type behavior and the shared PowerShell prelude intact.
First prove whether the pinned Add-Type can preserve explicit assembly names,
aliases, signing and direct reference sets. If it cannot, proposed bounded fallback
is a graph-only Roslyn `CSharpCompilation`/`MetadataReference`/Emit path using the
already pinned compiler. This is a design choice requiring approval, not permission
to replace the general compiler pipeline. Preserve structured diagnostic ID,
severity and mapped source location; never convert an arbitrary exception to CS1705.

Cache identity uses a domain-separated SHA256 over a canonical length-delimited
encoding of schema/compiler adapter version, OS/architecture, provider executable,
runtime/compiler/framework-reference content hashes, compile options, normalized
source and output logical paths, exact bytes, signing-key identity, node identities,
and ordered direct edges/aliases. Sort set-like collections; preserve source order.
Host absolute paths and mtimes are not semantic identity. Hash input content even
if size and mtime are unchanged. Each node key includes referenced node keys.
The root/export key includes the whole graph. Bump the graph cache namespace.

Compile in private staging; verify PE identity and record output SHA256 for every
node before publishing a manifest and export receipt atomically. Cache reuse checks
the complete manifest, all output hashes and identities. Partial, mutated or missing
dependencies rebuild or fail closed; a root DLL alone is never a valid graph hit.
Do not cache compile failures as successful exports. Concurrent producers must
verify any winner before reuse. Hash validation protects consistency; a writable
local cache is not an independent trust authority.

## Proof plan and ownership

`regression_prep` supplies the authenticated source pin, exact graph fixtures,
public signing-key material and native commands. This worker implements only an
approved narrow runtime contract and stages the targeted gate. The parent executes
acceptance on pinned Linux and actual native Windows, not Windows cross-compilation
or WSL presented as Windows. SSH destinations and deployment paths stay in private
coordination; public scripts accept explicit paths.

Required matrix on **each** OS:

1. Fresh pinned-native prerequisite builds; inspect name/version/token/reference
   metadata. Record exact compiler/provider versions and binary hashes, source and
   graph hashes, argv, stage exit status, stdout/stderr, and output hashes.
2. Native protected L88 compilation succeeds against separate Assembly1. A reviewed
   harness may observe the result `2` without changing official source bytes;
   harness bytes and binding are separately hashed. Parent/fixture owner approves
   the observer and exact golden output before candidate comparison.
3. Native CS1705 prerequisites succeed; root compilation fails with mapped CS1705
   at the higher-version use. Preserve raw diagnostics and independently verify
   code/location/stage, rather than accepting any nonzero exit or text substring.
4. Identical authenticated fixtures in product interpreted and compiled modes.
   Positive row compares native output/status; negative row must fail at C# analysis
   or compilation with CS1705, not execute a fabricated runtime error.
5. Targeted integrity checks: source/key/reference/alias/version mutations change
   identity; same-size/mtime mutations are detected; moved source root is stable;
   traversal/cycle/case collision/unknown fields are rejected; corrupt dependency
   cache cannot pass; compiled artifact runs without the original assets directory.
6. Existing focused C#/PowerShell and both-mode tests on the reviewed combined
   candidate. Inspect named test events and require nonzero expected counts, zero
   skips/failures; exit zero alone is insufficient. Preserve PS81/typed5b behavior.

Parent receipts reconcile at least two official target rows × two product modes
× two OSes (eight product observations), plus native stage receipts per OS. A
compile-negative observation is successful evidence only when its declared
diagnostic contract passes. No full-corpus or story-completion claim from this gate.

## Review questions and present limits

Manager review must settle manifest ingress/plan transport, limits and alias
semantics, pinned Add-Type feasibility versus graph-only Roslyn, signing behavior,
and protected L88 observer. Await upstream pin and native fixture receipt from
`regression_prep`. Discovery hashes can be checked with the companion script;
that gate is source discovery only. No native Linux/Windows or product assembly
graph acceptance has been run or claimed by this proposal.

Discovery gate (archive and checkout supplied explicitly):

```sh
python3 docs/s562/verify-csharp-assembly-discovery.py \
  --upstream-archive "$UPSTREAM_ARCHIVE" --combined-runtime "$COMBINED_RUNTIME"
```

Delivery validation: two file hashes, six fence hashes and exact clean baseline
passed. A temporary archive changing L88's return value was rejected at source
hash admission. `git diff --check` passed. These results validate this discovery
receipt only; they do not satisfy the native or product proof matrix above.
