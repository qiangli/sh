# Third-Party Licenses and Provenance

This repository is licensed under the top-level `LICENSE` file unless a file
states otherwise. Do not assume external reference material is covered by the
project license.

## Runtime and Test Dependencies

The Go module dependencies currently used by `go.mod` are permissively licensed
based on their local module license files:

- MIT: `github.com/creack/pty`, `github.com/ergochat/readline`,
  `github.com/go-quicktest/qt`, `github.com/kr/pretty`, `github.com/kr/text`
- BSD-style: `github.com/google/go-cmp`, `github.com/rogpeppe/go-internal`,
  `golang.org/x/*`, `mvdan.cc/editorconfig`
- Apache-2.0: `github.com/google/renameio/v2`

Before adding a dependency, verify its license is MIT, BSD, Apache-2.0, ISC, or
similarly permissive. Do not add GPL, LGPL, AGPL, or copied GNU Bash source code
to the repository.

## Bash Reference Material

The GNU Bash manual is external reference material available at
`https://www.gnu.org/software/bash/manual/bash.html`. It is published under its
own GNU Free Documentation License terms and is not covered by the repository
`LICENSE`. Do not vendor a local copy into this repository.

`external/bash-5.3` is intentionally ignored by git and should remain local-only.
It may be used as a behavior reference and for running GNU Bash compatibility
tests, but GNU Bash source and test files must not be vendored into this
repository or release archives.

## Clean-Room Rule

Implement Bash-compatible behavior from observed behavior, specifications, and
tests. Do not copy or translate GNU Bash implementation code. Keep any copied
diagnostic text short and limited to compatibility-critical command output.
