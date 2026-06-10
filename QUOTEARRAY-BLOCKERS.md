quotearray verification blockers
================================

This sandbox does not contain `external/bash-5.3/tests`, so the required
quotearray fixture command cannot reach `./quotearray.tests` or
`./quotearray.right`.

Attempted command:

```
GOCACHE=$PWD/.cache/go-build GOPROXY=off GOSUMDB=off make build && cd external/bash-5.3/tests && BASHY=$(realpath ../../../bin/bashy) && THIS_SH=$BASHY BUILD_DIR=$PWD/.. PATH=$PWD:/usr/bin:/bin:/usr/local/bin $BASHY ./quotearray.tests 2>&1 | diff - ./quotearray.right | wc -l
```

Result:

```
zsh:cd:1: no such file or directory: external/bash-5.3/tests
```

Because the fixture directory is absent, the final diff line count is
unavailable in this sandbox.

Implemented progress:

- Quoted arithmetic text such as `(( 'assoc[$key]++' ))` now performs
  shell-style expansion before arithmetic reparse, so benign associative
  subscripts like `key=abc` increment `assoc[abc]`.
- Expanded malformed associative subscripts like
  `assoc[x],b[$(echo uname >&2)]++` now fail before running the command
  substitution from the expanded key and report the expanded invalid-operator
  token.

Remaining risk:

- The complete bash 5.3 `quotearray` output could not be compared without the
  external fixture files.
- Full `go test ./expand/... ./interp/...` is blocked by the sandbox denying
  `/bin/ps` in `TestSetsidNewSession` and `TestNohupChildIsInNewSession`.
