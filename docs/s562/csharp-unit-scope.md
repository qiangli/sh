# S562 C# compilation-unit scope follow-up

Base: `d079711c8764052b12a9dec87854e7c0a1a80fb1`. The pinned
`dotnet/docs` corpus is `20f8ab0cae298fa9447eb4b48ae0fb3665741c26`.
The copied `file/L18.bsh` and `using-directive/L133.bsh` are byte-for-byte
fixtures, SHA-256 `4cc82ec71b9246e3de433b3902c9020afb98182f491643a2dab482cb6e36f8a1`
and `b9a668a4e752715a7fb7ea89718c00fe57750be4d11f6df8dc5867b83ce5ec46`.
Both expect status 0 and empty output in interpreted and compiled modes.

The prior normalizer wrapped these declaration blocks in generated `Fence`.
That changes the scope of `file class HiddenWidget` and the authored `enum
Color` named by `using static Color`. The parent corpus reported CS9054 and
an unresolved Color name in both modes. The unit partition already keeps
authored using directives and types at compilation-unit scope, while wrapping
the free `Run` method as the export. This change selects that partition only
for a top-level type with the `file` modifier or a simple `using static` name
matching a top-level authored type. Namespaces and extension classes retain
their existing selection. Ordinary and private nested types retain the
original wrapper, where free methods and state remain visible.

`verify-csharp-unit-scope.ps1` is the independent native control: it extracts
each exact authored C# block, removes only the corpus's free `Run` harness,
compiles with native `Add-Type`, and invokes the original `Main`. The lower
test checks the pinned fixture hashes, runs that control, and checks both
execution modes. A separate control keeps a private nested class, free
state, and external `using static System.Math` in the original wrapper.

The first pinned parent gate passed the file-local and ordinary nested-state
cases, then failed `using-directive/L133` with CS0246 for `Color` and CS0103
for `Green`. The leading `using static Color` had been lifted into the
generated header before the body marker, so the initial trigger could not
see it in the body syntax root. The revised trigger inspects using directives
in both regions while matching the authored type in the body.

Focused parent gate after installing a pinned pwsh runtime:

```sh
go test -tags full ./lower -run 'TestCSharpCorpusCompilationUnitFixturePins|TestCSharpCorpusCompilationUnitScopeBothModes|TestCSharpOrdinaryNestedTypeAndStateBothModes|TestCSharpAssembliesBothModesWithoutSourceAssets' -count=1 -v
go test ./polyglot -run 'TestCSharpAnalyzeExportsByReflection|TestCSharpTypedCallsErrorsAndState|TestCSharpCompilationUnitNamespacesExtensionAndTrivia|TestCSharpFileScopedNamespace|TestCSharpTopLevelExtensionClass|TestCSharpGraphRuntime' -count=1 -v
```

The macOS worker has no `pwsh`; those runtime tests skip here. The parent owns
the pinned remote C# corpus gates. No assembly graph, native-module config,
compiler flags, or status normalization changed.
