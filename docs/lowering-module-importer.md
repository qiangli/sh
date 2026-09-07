# Lowering Module Importer

## Overview
The `moduleImporter` in package `lower` implements `types.Importer` and `types.ImporterFrom`. It resolves Go packages by invoking `go list -export -deps -json` using the build-time toolchain binary `runtime.GOROOT()/bin/go` with `GOTOOLCHAIN=local`.

## Features & Verification
- **Package Identity**: Preserves a single `go/types` package identity across standard library and module dependencies without executing package `init` functions.
- **Contextual Vendor Resolution**: Respects contextual vendored packages (including GOPATH vendor mode and module vendor directories) by maintaining `ImportMap` mappings.
- **Internal Visibility Enforcement**: Enforces Go `internal` package access rules relative to the caller's package import path.
- **Module Context**: Preserves module, workspace (`go.work`), replace directives, and GOPATH context.
