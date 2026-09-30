# fmt type identity projection

Story #1289, Sprint #319.

1. Reuse the native helper's registered source type identities for `%T`.
2. Preserve value operands and normalize format argument indices; select added
   type strings only for projected type verbs. Keep malformed and extra-argument
   formats on the existing fmt path to retain their diagnostics.
3. Run the linked-package regression plus focused fmt and type identity tests.

The regression from commit 4250dc11d is included unchanged, with a second test
for mixed verbs, indices, dynamic width/precision, spread arguments, writers,
Appendf, and Errorf wrapping.

Validation attempted:

```sh
go test -tags full ./interp -run '^(TestS319LinkedPackageTypeVerb.*|TestS243MappedReflectIdentity|TestS243OriginalIssue49547GenericTypeFormatting|TestGoSourceImportedTypeIdentity|TestS275FmtValueCallbacksPreserveEffects|TestGoSourceS319VariadicFmtWriterFormatsBeforeLocalWrite)$' -count=1 -v
```

Blocked before compilation: the sandbox denies initialization of the
weave-provided GOCACHE. No alternative cache was used. The worker template
parses with gofmt and git diff --check passes. Runtime acceptance is unverified;
this is a candidate for manager validation, not a green delivery. Review callback
mutation of spread arguments when a type projection requires a copied slice.
No Linux run or full syntax-root acceptance is claimed. TestVerify and TestDump
remain outside this step.
