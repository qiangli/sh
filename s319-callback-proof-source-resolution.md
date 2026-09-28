# Sprint 319 / Story 1086 — the callback proof could not READ `cmd/compile/internal/syntax`

Sprint: #319
Story: #1086
Story-ID: 2405313cf7e2

## The residual

The previous lane on this story (`11fd72c2`, integrated as sh `7f8c3df6`)
generalized the recursion rule to method values and raised
`dependencyCallbackProofDepthBound`, and it did make the whole-package proof
of `cmd/compile/internal/syntax.Parse` certify:

```
syntax.Parse: ok=true steps=10426/20000 maxDepth=65/256     (TestS319CallbackProofSyntaxParseDepth)
TestS281DependencySourceCallbackProof: PASS                 (the end-to-end gate)
```

The manager's authenticated go1.27.1 run on that exact sh nevertheless still
failed, identically:

```
--- FAIL: TestTypeSetString (0.02s)
panic: gosource: asynchronous or retained original function callbacks are
unsupported for __gosource_import_0_64_0.Parse
```

Reproduced here on `7f8c3df6` with the manager's own script
(`.agents/sprint319/manager-types2-repro.sh`, fresh `S319_REPRO_BASE`,
`S319_REPRO_SH` = this worktree): same failure, same 0.02s.

## The proof never ran

Under `BASHPP_CALLBACK_PROOF_DIAG` there was nothing. Not a rule, not a
budget — no line at all, because the refusal was upstream of every diagnostic
the proof records. Bounded instrumentation at the loader showed why:

```
ENTRY op="call" sel="__gosource_import_0_64_0.Parse"
  arg2 kind="callback"                         -> path=cmd/compile/internal/syntax name=Parse args=[2]
LOAD  path="cmd/compile/internal/syntax" err=<nil> dir="" gofiles=[]
```

`dependencyFunctionCallbackLifetimeProof` was entered with exactly the key the
green measurement proves. `loadDependencyFunctionCallbackLifetimeProof` then
listed the package and got nothing back:

```
"Incomplete": true,
"Error": { "Err": "ambiguous import: found package cmd/compile/internal/syntax in multiple directories:
    /…/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.1.darwin-arm64/src/cmd/compile/internal/syntax
    /…/manager-types2-repro/goroot/src/cmd/compile/internal/syntax" }
```

The proof resolves its sources by listing the import path **in the interpreted
program's own working directory**. `go test cmd/compile/internal/types2` runs
that program with its cwd inside the harness's `goroot` symlink farm, whose
`src/cmd` is a main module that declares the path, while `GOROOT` names the
toolchain module's copy. Two trees, one path, no answer — so
`facts.Dir == ""`, and the loader returned `false` **silently**. The transport
then reported the general callback rule for what was an unreadable package.

`slices.SortFunc` in the same session resolved fine (`ok=true steps=155`): a
`std` path resolves from any directory. Only the second `cmd` module collides.

This is why the library gates stayed green. `TestS319CallbackProofSyntaxParseDepth`
reads the package straight off `runtime.GOROOT()`, and
`TestS281DependencySourceCallbackProof` runs its program from an ordinary
directory. Neither has a second goroot underfoot.

## The rule this lane adds

Whether a directory can name an import path is a property of **that
directory**, not of the dependency. The dependency worker is unaffected by the
program's cwd — it is compiled from its own scratch directory
(`bashPPBuildWorkerImportcfg`), where only the configured toolchain resolves.
So the proof was refusing a callback whose code it could have read, and whose
code the worker had already linked.

`dependencyCallbackProofSources` replaces the single listing:

1. List in the module context every other dependency resolution in the bridge
   uses (`bashPPModuleRequest`). Whenever the program's module can name the
   path this is the answer, and nothing changes.
2. If that listing names no readable package, retry **once** in a neutral empty
   directory — the same context the worker's own build resolves in.
3. Admit the retry only if it landed inside the GOROOT of the toolchain that
   BUILDS the worker (`go env GOROOT` asked of that toolchain, containment by
   cleaned relative path). Anything else refuses, unread.

The authentication is what makes the retry sound rather than a second guess.
The toolchain's copy is the one copy the worker can have linked: a worker build
from the program's directory would have failed on the very same ambiguity, so a
session that is running at all linked that copy. A readable package the module
context already named is never re-listed, and a package that is unreadable in
both contexts stays refused.

"Readable" is unchanged from before this lane — a directory, at least one Go
file, and no cgo file — now spelled once in
`dependencyCallbackProofSourcesUsable` instead of inline.

## The silence this lane removes

Every route out of the loader now records why, through
`dependencyCallbackProofSourceDiagnostic`: one bounded line per refused load,
under `BASHPP_CALLBACK_PROOF_DIAG`, naming the listing error, the unreadable
file, or the parse failure. A proof that cannot read the dependency is a
refusal like any other and must say so.

This is the second time the same silence sent this story to the wrong rule.
The previous lane found `recursiveBodyStoresCallback` answering "this body
stores a callback" on an exhausted depth bound without recording anything; the
outer message then named a rule where the cause was a budget. Here the loader
answered "refused" on an unreadable package, and the outer message named a rule
where the cause was a directory.

## Measured

Manager reproducer, `.agents/sprint319/manager-types2-repro.sh`, fresh base,
authenticated go1.27.1 patched `go-bashpp`, `cmd/compile/internal/types2`:

| sh | `TestTypeSetString` |
| --- | --- |
| `7f8c3df6` (integrated base) | FAIL 0.02s — asynchronous or retained … for `__gosource_import_0_64_0.Parse` |
| this lane | **PASS 16.65s** (package ok, 48.5s) |

## Negatives preserved

Nothing in the proof's rules, budgets or store analysis moved; this lane is
entirely about which sources the proof reads and what it says when it cannot
read any. Unchanged and green:

- `TestS319CallbackProofRecursiveMethodValue` (with its `places_grow` and
  `method_value_retained` refusals), `TestS319CallbackProofSyntaxParseDepth`
- `interp/bashpp_s319_callback_proof_method_value_test.go`,
  `interp/bashpp_s319_manager_{future_dispatch,alias_mutation,global_alias}_test.go`
- `TestS281CallbackProofUnreachableCalleeNegatives`,
  `TestS281CallbackProofClosureParameterNestedRetention`,
  `TestDependencyCallbackProofRecursiveCloneLineage`,
  `TestS281DependencySourceCallbackProof`

New in this lane
(`interp/bashpp_s319_callback_proof_sources_internal_test.go`):

- `TestS319CallbackProofSourcesResolveAroundAmbiguousProgramDir` — builds the
  second-goroot shape hermetically in a temp dir, asserts the program directory
  genuinely cannot name the path (else it skips, so the reduction cannot
  silently stop reducing), and then that the sources come from the build
  toolchain, not from the program's own tree, and that
  `cmd/compile/internal/syntax.Parse` proves over them.
- `TestS319CallbackProofSourcesRefuseUnreadable` — a path that names nothing,
  and a relative path; both refuse with an error that names the path, and the
  whole proof refuses with them.
- `TestS319CallbackProofSourcesRefuseCgo` — listable and still unreadable.
- `TestS319CallbackProofWithin` — the containment boundaries the GOROOT
  authentication rests on: a sibling sharing a prefix, a parent, trailing
  separators, empty operands.
