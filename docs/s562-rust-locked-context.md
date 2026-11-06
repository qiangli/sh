# S562 Rust locked dependency context

Story f9a83f049144. Proposal staged before implementation; manager owns remote
acceptance and corpus46 owns native fixtures and corpus adapter changes.

Opt in with BASHPP_RUST_CONTEXT naming a JSON file (relative to the requesting
source directory). No context preserves the existing bridge-only build path.
The context declares schema 1, SHA256 of the exact aggregated Rust fence source,
compiler and Cargo binary SHA256 and exact version outputs, exact direct versions
and features (including serde/serde_json/base64), and SHA256 inventory of
Cargo.lock, vendor/** and explicit packages/<crate>/**. The initial crates.io-only proposal was extended after corpus46 supplied the
exact trpl source package: explicit inventoried packages/<crate> dependencies
are supported. No git, ambient Cargo.toml imports, extra Cargo configuration,
or unlocked fallback is permitted.
The generated root package remains bashpp_module 0.0.0, edition 2024; prepare the
lock against that package and the declared bridge+user dependencies.

Discovery binds the JSON bytes into the environment fingerprint and snapshots
the verified closure. Preparation checks source and provider identities, copies only inventoried
regular files into a bounded private directory, and builds with an empty private
CARGO_HOME and target. All Cargo invocations are frozen/offline. Dependency-only
preflight precedes user compilation. Admission/provider/dependency errors carry
RUST-ECONTEXT/RUST-EPROVIDER/RUST-EDEPENDENCY, never source diagnostic permission.
Only root compiler errors other than dependency-resolution error codes may be
classified RUST-ESOURCE; unclassified build failures remain RUST-EBUILD.

The executable bytes remain Plan.Artifact. Execution consumes neither context,
vendor tree, lock, compiler nor Cargo. Context bytes are not embedded as assets.
Initial proposed limits were superseded by the measured bounds below.
Hash inventories authenticate supplied contents, not publisher trust; the corpus
runner must authenticate its native source/provider/context tuple independently.

Narrow gate: admission rejection controls, immutable discovery, real offline
vendored user-crate execution after source assets are removed, genuine source
error vs missing closure, unchanged no-context controls, cancellation. Manager
then runs the exact rand/trpl native tuple and interpreted/compiled corpus cases
on the pinned Linux provider, reconciles counts, and accepts remotely.

## Reviewed implementation contract

Manager messages mb:12292 and mb:12301 approved the bounded design and requested
a durable overlay and native tool identities. Both are implemented. Prefer:

```json
{"language":"rust","runtime":"/absolute/rustc","rust_context":"closure/context.json"}
```

The context path is relative to the overlay. BASHPP_RUST_CONTEXT overrides it;
a relative override is relative to the requesting source. Standalone Go callers
may set Rust.ContextFile and optionally ContextSHA256. No process-global context
is read by a standalone Rust{} value.

JSON field names: `schema` (1), `source_sha256`, `rustc_sha256`, `cargo_sha256`,
`rustc_version` (trimmed `rustc -vV`), `cargo_version` (trimmed `cargo -V`),
`dependencies`, `files`, `build_tools`, optional `provider_files`.
Each dependency is `{ "version": "1.2.3", "default_features": true,
"features": ["feature"] }`; versions exclude a leading equals sign. Each
`build_tools` entry (`CC`, `CXX`, `AR`, `LINKER`, all required) is
`{ "path": "/absolute/tool", "sha256": "<full digest>" }`. `provider_files`
is an absolute path to SHA256 map binding SDK binaries invoked by wrappers.
`files` maps `Cargo.lock`, `vendor/**` and declared `packages/<crate>/**` paths to full SHA256 values. The selected
native SDK receipt must cover wrapper dependencies; a wrapper hash alone does
not authenticate a compiler it invokes. Arbitrary ambient compiler flags are
removed; only the pinned linker is supplied through CARGO_ENCODED_RUSTFLAGS.

Discovery snapshots all authenticated closure bytes into a private immutable
preparation field; environment Clone shares this read-only snapshot. Later
source-asset edits cannot alter it. A fresh discovery rejects changed hashes.
The generated worker manifest is deterministic from exact dependencies and has
no caller-provided TOML. Lowered launch plans deliberately omit the snapshot.
The environment identity includes the context digest, hence source, lock,
closure and declared provider hashes. Provider files and versions are checked
at each preparation. The full fence source is preserved byte-for-byte before
the generated bridge is appended.

Corpus46 mb:12314 measured the complete native vendor188 at **243 MiB** unpacked.
Revised bounds allow headroom: JSON **8 MiB**, **40,000 files**, **32 MiB/file**,
**512 MiB total**, **64 direct crates** and **40,000 SDK identity files**. Duplicate
JSON keys, case-insensitive and file/directory path collisions, symlinks,
traversal, non-regular files, extra Cargo configuration and undeclared
packages are refused. Only inventoried files enter preparation.

Root compiler diagnostics require an error code and a primary span within the
original source bytes to receive RUST-ESOURCE. Missing-crate/import/module codes
remain RUST-EDEPENDENCY. Generated-bridge and uncoded linker/build failures are
RUST-EBUILD. RUST-ESOURCE is a stage classification, **not** an oracle license:
the diagnostic runner still needs its authenticated exact native receipt.

Run `scripts/verify-rust-context.sh /absolute/new-log-directory`. The gate needs
rustc/Cargo, native CC/CXX/AR, Python3 and cached test-only versions serde1.0.229,
serde_json1.0.151, base640.22.1, rand0.9.3 plus their closure. It fails on missing
inputs and reconciles named tests with zero skipped/failed cases. Provider
versions and hashes are measured for these local mechanism tests; they are not
represented as the manager's official pinned Linux native proof.

Manager acceptance remains separate: corpus46 supplies the complete worker-root
lock/vendor tuple (including bridge dependencies) and exact CC/CXX/AR/linker
wrappers with SDK receipt; the diagnostic owner binds source/provider/error
receipts. With no cache declaration, the private target recompiles dependencies per
preparation. For the trpl case deadline, declare an absolute `cache_dir` in the
context and first call `Rust.PrewarmDependencies(ctx, exactSource)` using the
separate prerequisite budget. `scripts/rust-context-prewarm.go` is a buildable
helper with a fixed 120-second prewarm deadline; normal case callers retain
their 20-second deadlines. Build the helper against this exact candidate before
running it; the helper never attempts source compilation.

The cache key covers exact direct dependencies/features, lock/vendor/path bytes
and all declared provider hashes/versions. Only source hash and cache directory
are excluded so cases with the same closure can share dependency work. Cache
source files are rehashed and unexpected files rejected on every use. Cached
compiler outputs are private build outputs under that key, never imported from
ambient Cargo targets. Stable vendor paths permit reuse; unique root binary
names and cleanup avoid cross-case output collisions. Source mutation still
requires a different authenticated context/source digest. Provider and closure
changes select a different cache. The prewarm test proves dependency artifact
mtime stability through source compilation, provider-key invalidation, and
rejection of modified cached lock bytes. Corpus acceptance must independently
verify the full trpl prewarm and subsequent20s cases on the native tuple.


Corpus46 mb:12318 supplied exact worker-root locks at
`/tmp/s562-worker-closures-v1/{rand,trpl}/Cargo.lock`: bridge dependencies
serde1.0.210 + derive, serde_json1.0.128, base640.22.1; rand0.9.5. The trpl
root contains `exclude=["packages/trpl"]` to retain the upstream trpl manifest's
own workspace. A direct dependency may therefore also declare
`"path":"packages/trpl"` with its exact package version. Only that package's
inventoried bytes are copied; Cargo metadata must report the declared version
and exact package manifest location. The deterministic root manifest excludes
all such explicit packages from its workspace. Nested undeclared path dependencies
remain rejected. Measured resolved vendor: trpl175 packages221,404,932 bytes8,793
files; rand23 packages15,754,393 bytes1,631 files. No native source is trimmed.
A focused synthetic path crate with its own workspace now exercises this branch
alongside rand in the artifact gate.


Corpus46 mb:12339 supplies native tool identities at
`/tmp/s562-native-proof-9913a2c9/native-tools.json` and the complete **19,547-file**
Zig SDK inventory in `linker-inputs.json`. Provider inventory capacity is40,000
files, separate from the40,000 closure-file bound. This supports the exact
native wrappers and their already-proven LLVM-to-Zig target translation; no
arbitrary flags or new target translation are introduced here.

## Root module steering (2026-11-05)

The conductor corrected the old missing-module golden for ch07-05L21: the
exact declared src/front_of_house.rs makes native compilation succeed. The
existing `files` inventory now also admits explicit `src/**/*.rs` modules,
except case-insensitive reserved `src/main.rs` and `src/lib.rs`. Existing
hash, count, byte, collision and symlink checks apply. Modules are copied into
each isolated build root even when dependency caching is enabled, and their
hashes bind discovery and cache identity. No ambient module discovery occurs.
The original aggregate remains the exact generated worker prefix. Build uses
`--quiet --message-format=json` to retain Cargo records without progress text.
Manager-approved 512 MiB replaces the earlier implementation's 384 MiB limit.

Follow-up local gate: `/tmp/s562-rust-root-module-gate-final` reconciles 39
polyglot, 2 lower and 29 race test/subtest passes, zero skips/failures. This
includes native module execution with/without the cache, concurrent native
source builds sharing a warmed target, unchanged dependency artifact mtimes,
concurrent closure publication, cancellation and module snapshot controls.
These local providers remain mechanism evidence, not official acceptance.

The private parent materialization receipt binds five official rand/trpl cases. Read-only
inspection found `source.rs` absent alongside each `case.bsh`/`bashpp.json`;
corpus46 was notified to restore exact authenticated source extraction before
using the prewarm helper and to supply the corrected ch07 module fixture.
No remote acceptance builds were run by this worker. Parent must build the
helper from this committed candidate, run separate 120-second prewarms, then
retain 20-second native and interpreted/compiled case gates with authenticated
source/dependency diagnostic receipts. The old missing-module golden is invalid.
