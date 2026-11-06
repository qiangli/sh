# Sprint 562 E4 C# fence: bounded compilation-unit design

Status: manager-approved design, implemented in the dedicated story
`759e75a52741`, an implementation dependency of parent E4 story
`df328510d106`. Approval covers the bounded Roslyn-backed partition,
deterministic `/unsafe`, and v2 cache salt described here; it does not authorize
a general C# project system.

## Outcome and scope

The bounded change belongs entirely to the existing C# fence implementation.
It should make the 56 currently classified C# rows eligible for regeneration:

| gap | rows | required behavior |
| --- | ---: | --- |
| namespaces / same-named types | 31 | retain namespace declarations at compilation-unit scope instead of flattening or nesting them in the generated export class |
| unsafe code | 24 | compile the fence assembly with Roslyn's `/unsafe` option |
| extension method | 1 | retain its declaring static class at compilation-unit scope, where C# permits extension methods |

These counts reconcile the 58 C# `missing-feature` rows in the E4 inventory:
the other two are the deliberately unsupported NuGet-package contract and are
not part of this change. `Environment.Exit` has a recorded reproducer but no
inventory row and is also out of scope because it requires worker-lifecycle
semantics, not compilation-unit support.

The evidence is the E4 triage and C# findings in bashsharp-tests weave issue
28. This design does not edit either source inventory, its diagnostics, case
counts, pins, or expected output.

## Contract to preserve

The public Go surface remains `polyglot.CSharp`; no field, interface, language
alias, syntax node, or lowering contract changes. At the fence boundary:

- leading `using` directives remain compilation-unit directives;
- public static methods written as fence members remain methods on the unique
  generated `BashPP.CSharp.M<hash>.Fence` type;
- only those methods are reflected by `Fence-Exports`, so helper types and
  public methods in user namespaces do not accidentally become Bash# exports;
- overload, generic-method, `ref`/`out`, type mapping, call conversion, error,
  stdout/stderr, cache, and worker protocols remain unchanged;
- compiler diagnostics continue to name the original fence file and line via
  the existing `#line` mappings.

This is a dual-shape compilation unit, not a new fence mode visible to users:

```csharp
using System;

namespace Example.One { public class Person { } }
namespace Example.Two { public class Person { } }
static class Ext { public static int Twice(this int value) => value * 2; }

namespace BashPP.CSharp.M<hash> {
    public static class Fence {
        public static int Run() => 3.Twice();
    }
}
```

Existing member-only fences still generate the same effective declaration
shape. In particular, an ordinary helper type in a legacy member-only fence
stays nested unless the source actually needs compilation-unit declarations;
that avoids silently changing access to sibling fence fields or methods.

## Proposed implementation

1. Keep source aggregation, package-directive refusal, generated namespace
   naming, and line tracking in `csharpGenerate`. Change its internal output to
   retain a separately delimited body long enough for the C# compiler front end
   to classify declarations before the final wrapper is assembled. This is an
   internal representation only; it is never exposed or written to the cache
   as a user artifact.

2. In the existing PowerShell compile prelude, use the already shipped
   `Microsoft.CodeAnalysis.CSharp` syntax APIs to partition only top-level
   compilation-unit declarations from fence members. Compilation-unit mode is
   selected when the body contains a namespace declaration or a static class
   containing an extension method. In that mode, namespace declarations and
   top-level type/delegate declarations stay outside the generated `Fence`;
   residual methods, fields, properties, and events remain inside it. In the
   legacy mode the body is wrapped exactly as today.

   The partition must operate on Roslyn spans/trivia, not regular expressions
   or a new Go lexer. It must preserve comments, preprocessor directives,
   strings/raw strings, declaration order, and every `#line` mapping. A parse
   that cannot be partitioned unambiguously fails with its original mapped
   Roslyn diagnostic; it must not fall back to namespace stripping.

   File-scoped namespaces are rewritten from their Roslyn token/member spans
   to an equivalent block-scoped namespace before partitioning. Namespace
   types stay inside that block, while trailing free fence members remain on
   the generated export type outside it. Interleaved free members and later
   namespace declarations fail explicitly rather than being reordered.
   Follow-up review of the pinned issue-28 upstream sources found file-scoped
   forms in 13 of the 31 `missing-feature:csharp-namespaces` rows, correcting
   the initial design audit; refusal would therefore leave an in-scope gap.

3. Pass `-CompilerOptions '/unsafe'` to the existing `Add-Type` invocation for
   every C# fence. `/unsafe` only permits unsafe constructs; it does not make a
   safe construct unsafe, and a fence already executes arbitrary foreign code.
   Applying the option deterministically avoids a substring heuristic and
   gives safe and unsafe sources one cache/compile path on Linux and Windows.

4. Keep analysis and worker recompilation on the same `Compile-Fence` helper,
   including the compilation-unit normalization and compiler options. Add the
   generated export type name to that helper's input if needed; do not duplicate
   transformation logic between `csharpAnalyze` and `csharpWorker`.

5. Bump the C# cache format salt from `bashpp-csharp-v1` to `v2`. The same raw
   fence and toolchain can now produce a different assembly and uses different
   compiler options, so old DLL/JSON pairs must not be reused. Atomic cache
   publication and missing-assembly recompilation remain unchanged.

No changes are proposed in `syntax`, `interp` evaluation, `lower`, the common
polyglot protocol, PowerShell worker framing, or environment discovery.

## Files

Implementation after approval is expected to touch only:

- `polyglot/csharp.go`: internal source layout, Roslyn-backed partition,
  `/unsafe`, shared compile path, and cache-version bump;
- `polyglot/csharp_test.go`: generation/diagnostic/cache and direct runtime
  regressions;
- `interp/bashpp_csharp_test.go`: one shell-surface regression proving that the
  exported `Run` contract survives namespaces, an extension class, and unsafe
  code.

After the sh commit is manager-reviewed, the separate bashsharp-tests corpus
lane owns regeneration of `tools/fence-corpus/csharp/generate.py`, inventory,
cases, findings, and count ratchets. This workspace must not edit those files,
canonical bashy, workspace pins, or the S561 frozen candidate.

## Focused tests

The implementation gate should add and run these focused proofs:

1. A legacy member-only fixture produces the same export names/signatures and
   keeps an ordinary helper type usable from an exported method.
2. Two block-scoped namespaces may each define `Person`; `Run` can use both,
   and methods on either `Person` are not exported.
3. A top-level static extension class compiles and `Run` observes the extension
   result; only `Run` appears in the export list.
4. An `unsafe` method using a local pointer compiles and runs under `/unsafe`.
5. A mapped compile error inside a namespace and one inside `Run` report the
   original `.bsh` file and line, and a failed compile leaves no DLL/temp entry.
6. The second prepare is a cache hit, while a pre-v2 cache entry is ignored;
   worker-side recompilation after DLL removal uses the identical source shape
   and `/unsafe` option.
7. A combined Bash# script calls the exported method in interpreted mode and
   preserves result/error binding.

Suggested developer commands are deliberately narrow:

```sh
go test ./polyglot -run 'TestCSharp(Analyze|Typed|Compile|CompilationUnit|Extension|Unsafe|Second|Worker)'
go test ./interp -run 'TestBashPPCSharp(CompilationUnit|Typed)'
```

The manager, not the dev worker, runs the regenerated affected C# corpus and
the required full Linux and Windows count-ratcheted gates on claimed hosts.
All 56 rows must move through native-oracle regeneration to PASS; no disposition
rewrite, xfail, skip, timeout increase, or denominator reduction is evidence.

## Risks and review decisions

- **Declaration partition (medium-high):** directives and trivia around moved
  namespace/type nodes are the main correctness risk. Roslyn-backed spans plus
  mapped-error tests are mandatory. A hand-written brace scanner is rejected.
- **Legacy compatibility (medium):** hoisting every apparent type would change
  member-only fences. The explicit legacy/compilation-unit branch and regression
  fixture bound that risk.
- **Add-Type portability (medium):** `/unsafe` and the syntax APIs must be
  exercised with the pinned PowerShell/Roslyn on both Linux and Windows. A
  local ambient `dotnet` SDK is not an acceptable substitute.
- **Cache identity (low after v2):** without the salt bump, stale assemblies can
  mask failures. The v2 key makes the behavior fail closed.
- **Diagnostics (medium):** moved declarations must carry their structured
  trivia and `#line` directives. Losing locations blocks approval even if the
  examples compile.

Manager review is required specifically for the compiler-backed partition and
the deterministic always-on `/unsafe` option. If either is rejected, stop and
redesign; do not broaden into a new C# project system, multi-file protocol,
syntax annotation, or general Roslyn host.

## Estimate

Implementation and focused tests: one engineering day (about 5 points):
roughly half a day for normalization/compile/cache work and half a day for
runtime, diagnostic, and compatibility tests. Corpus regeneration plus claimed
Linux/Windows gates is a separate manager/corpus-lane activity and is expected
to take another half host-day, excluding host queue time.
