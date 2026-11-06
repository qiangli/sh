# S562: C++20 module assets and source coverage

Owning story: `38ff35b7dc6e` (#3081), following E4 design review under
`df328510d106` (#3050). The manager approved the finite asset graph and ordinary
consumer design. C++20 and pinned Zig 0.16.0 remain the contract; exports and the
worker protocol are unchanged. Corpus generation and integration pins are
separately owned.

## Configuration and execution

Select the C++ environment using the existing overlay format:

```json
{"language":"cpp","runtime":"clang++","native_modules":"modules.json"}
```

`native_modules` is a scalar path in both JSON and YAML. The referenced strict
JSON manifest is versioned:

```json
{"version":1,"units":[
  {"name":"Example","kind":"interface","source":"Example.cppm","requires":[]}
]}
```

Kinds are `interface`, `internal-partition`, and `implementation`. Names with
colons identify partitions; each importable name has one BMI provider.
Implementation units explicitly require their interface. Paths are relative to
the selected overlay directory and confined to the project boundary after
symlink resolution. Missing files/providers, duplicate sources/providers,
unknown fields, unsupported versions and dependency cycles are errors. The
manifest has at most 256 units. No directory scan, build shell, CMake execution
or ambient module discovery occurs.

The manifest and exact source files participate in environment fingerprints.
The immutable plan clones dependency slices independently. Compiler outputs
remain private to one preparation: no BMI is reused across preparations.
Headers are read by that compilation and represented in the linked artifact;
they are not guessed from textual include scanning.

Module units remain intact assets. A normal fence imports them and exposes
only existing supported top-level bridge functions, for example:

```cpp
import Example;
int example_value() { return Example_NS::f(); }
```

Native namespace/class/template implementation details are not new Bash# value
exports. Direct named-module declarations in concatenated fence bodies are not
this feature. The linked worker is self-contained and runs without module
source or BMI files, in interpreted and lowered modes.

## Pinned compiler adapter

Extension-based `.cppm` compilation in Zig's bare Clang path produced different
target, library and stack-protector settings from normal Zig consumers. The
successful adapter uses the normal `zig c++ -std=c++20 -O0 -c -x c++` driver's
complete cc1 settings. It parses the quoted trace as data, validates exactly
one matching compiler executable/action/source/output, and changes only action
and output for BMI emission. No shell evaluates the trace. The provider must
be the actual Zig 0.16.0 executable with leading `c++`; arbitrary wrappers and
unverified compiler upgrades are not accepted by this internal-API adapter.

Each unit receives explicit mappings for its declared dependency closure.
The compiler checks the declared module name; BMI introspection verifies its
kind and identity. Introspection receives the same dependency mappings.
Ordinary object compilation and consumer analysis/linking use compatible
settings. Configuration validation is never disabled. The native launch
policy, including private Zig cache environment variables, is unchanged.
See [Clang standard modules](https://clang.llvm.org/docs/StandardCPlusPlusModules.html)
for the distinction between BMI and object compilation.

## Exact source dispositions

All eleven original rows come from MicrosoftDocs/cpp-docs commit
`281a0a0eae55fe97f5decd244db18ed5096d4687` (code MIT; prose CC-BY-4.0).
Final reviewed accounting is **seven build cases, one Example execution,
one Rectangle native compiler diagnostic, and two C++23 version dispositions**.
Every original row retains its provenance. Build and diagnostic assertions
must not be reported as successful upstream executions.

| Pinned source | Required treatment |
| --- | --- |
| [import-export-module.md:L24](https://github.com/MicrosoftDocs/cpp-docs/blob/281a0a0eae55fe97f5decd244db18ed5096d4687/docs/cpp/import-export-module.md#L24) | Empty primary interface; build/import check. |
| [import-export-module.md:L30](https://github.com/MicrosoftDocs/cpp-docs/blob/281a0a0eae55fe97f5decd244db18ed5096d4687/docs/cpp/import-export-module.md#L30) | ModuleA namespace function declarations, no definitions; interface build check, no invented function bodies. |
| [modules-cpp.md:L123](https://github.com/MicrosoftDocs/cpp-docs/blob/281a0a0eae55fe97f5decd244db18ed5096d4687/docs/cpp/modules-cpp.md#L123) | Empty internal partition; build with a primary interface companion. |
| [modules-cpp.md:L129](https://github.com/MicrosoftDocs/cpp-docs/blob/281a0a0eae55fe97f5decd244db18ed5096d4687/docs/cpp/modules-cpp.md#L129) | Empty interface partition; build and re-export from primary companion. |
| [modules-cpp.md:L27](https://github.com/MicrosoftDocs/cpp-docs/blob/281a0a0eae55fe97f5decd244db18ed5096d4687/docs/cpp/modules-cpp.md#L27) | Complete Example interface; consumer wrapper calls Example_NS::f() and expects 42. |
| [tutorial-import-stl-named-module.md:L167](https://github.com/MicrosoftDocs/cpp-docs/blob/281a0a0eae55fe97f5decd244db18ed5096d4687/docs/cpp/tutorial-import-stl-named-module.md#L167) | Reviewed C++23 language-version disposition; no PASS credit. |
| [tutorial-import-stl-named-module.md:L89](https://github.com/MicrosoftDocs/cpp-docs/blob/281a0a0eae55fe97f5decd244db18ed5096d4687/docs/cpp/tutorial-import-stl-named-module.md#L89) | Reviewed C++23 language-version disposition; no PASS credit. |
| [tutorial-named-modules-cpp.md:L113](https://github.com/MicrosoftDocs/cpp-docs/blob/281a0a0eae55fe97f5decd244db18ed5096d4687/docs/cpp/tutorial-named-modules-cpp.md#L113) | Point partition definition; build/import check with article primary interface. |
| [tutorial-named-modules-cpp.md:L222](https://github.com/MicrosoftDocs/cpp-docs/blob/281a0a0eae55fe97f5decd244db18ed5096d4687/docs/cpp/tutorial-named-modules-cpp.md#L222) | Reviewed native compiler diagnostic using exact Point/Rectangle/primary/implementation assets; no successful-run credit. |
| [tutorial-named-modules-cpp.md:L73](https://github.com/MicrosoftDocs/cpp-docs/blob/281a0a0eae55fe97f5decd244db18ed5096d4687/docs/cpp/tutorial-named-modules-cpp.md#L73) | BasicPlane function declaration without definition; interface build check. |
| [tutorial-named-modules-cpp.md:L85](https://github.com/MicrosoftDocs/cpp-docs/blob/281a0a0eae55fe97f5decd244db18ed5096d4687/docs/cpp/tutorial-named-modules-cpp.md#L85) | Empty BasicPlane.Figures primary interface; build/import check. |

The seven declaration snippets receive actual module build/import checks,
without invented function bodies. The Example bridge must return 42.

The standard-library tutorial explicitly introduces C++23 `std`/`std.compat`.
The sprint plan permits recorded dispositions for newer language versions and
excludes raising defaults. The approved reason is
`language-version:c++23; declared-contract:c++20`; the corpus owner coordinates
the narrow validator category. libc++'s [module documentation](https://libcxx.llvm.org/Modules.html)
also distinguishes standard C++23 support from an optional C++20 extension.
Neither version disposition earns PASS credit.

## Rectangle source defect and retained native proof

The exact native oracle built the Point, Rectangle and primary Figures BMI
and object pairs successfully. The original L194 implementation then failed
with `unknown type name 'Rectangle'` on its three definitions (lines 7–9).
Its declaration names `BasicPlane.Figures:Rectangle`, duplicating the interface
partition and omitting the primary-interface import supplied to an ordinary
implementation. C++20 [N4861 module.unit paragraphs 3 and 8](https://timsong-cpp.github.io/cppwp/n4861/module.unit)
require distinct partition identities and restrict that implicit import to
non-partition implementations. This is an upstream C++20 source defect.

The manager approved retaining the exact assembled Rectangle case as a native
compiler diagnostic, with no successful-run credit. A separately labeled
synthetic control may change only its declaration to
`module BasicPlane.Figures;`; that control is never an upstream replacement.

The native oracle uses the pinned Zig driver with `-std=c++20 -O0`, explicit
`-fmodule-file=NAME=BMI` mappings and normal `-c -x c++` object compilation.
Its final command compiles the unchanged `implementation.cpp` with all three
existing mappings. It is independent of the fence adapter. Retained receipts
contain every argv, stdout, stderr, exit status and input digest. Exact pinned
block digests are:

| Asset | SHA-256 |
| --- | --- |
| Figures.cppm | `4cf008f79b423caf25db3a9cc0f9864298680c607c5644f843f2b5eebcfaa62f` |
| Point.cppm | `767fcd1566543582c41138c1069cb14140652ca2c643df8ff4e66dcd3898c489` |
| Rectangle.cppm | `e39e1733fe46213fa224f792118b813be8c0281215229f858c082ba610c5c070` |
| consumer.cpp | `1e839e69ab46bf58adeb4df3fe80e7635463edf3cba0344944aaae9ae0542006` |
| implementation.cpp | `9ca9ac021ad36a78f4eb2ca9b6e36780a84da22751ac069436b2af2c7e29cc0c` |

## Validation and delivery milestones

1. Manifest validation: four focused top-level tests and 17 subtests passed,
   including strict schema, graph order, symlink escape and deep clone checks.
2. Native preparation: primary/interface/internal partitions and separate
   implementations passed, including calls after deleting every source asset.
   Driver trace refusal, compiler identity, missing object, private symbol and
   source fingerprint checks passed. Earlier raw compiler probes retain the
   incompatible target/settings failures that motivated the uniform adapter.
3. Interpreted namespace bridge parity passed. Lowered separate-implementation
   parity passed after correcting a test resolver that incorrectly answered
   Go SDK requests with Zig. The correction dispatches by requested tool name.
4. Corpus owner regenerates exact assets/provenance and the reviewed eleven-row
   accounting, then the manager checks both required execution modes.

All tests ran on the manager-held host, with raw receipts retained. No full
suite ran on the development host. Prior C variable and raw-fence fixes remain
separate ancestor commits. Integration and pin changes are not part of this
feature branch.
