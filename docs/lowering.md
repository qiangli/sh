# Bash++ ordinary Go compiler foundation

`mvdan.cc/sh/v3/lower` consumes the parser's positioned `*syntax.File` and
emits inspectable, formatted Go. Functions become ordinary Go functions,
expressions become native operators, and typed calls become native calls.
The compiler never runs the source or packages the whole program inside an
interpreter. The foundation is a bounded vertical slice, not complete Bash++
coverage. Unsupported forms return positioned diagnostics and no partial output.

## Public API

```go
const DefaultRuntime = "mvdan.cc/sh/v3/lower/shellrt"

type Options struct {
    Package string // default: main
    Runtime string // default: DefaultRuntime
    Origin  string // metadata only; never emitted into Source
}

func Compile(file *syntax.File, options Options) (*Result, error)

type Result struct {
    Source   []byte
    Package  string
    Imports  []string // sorted, unique import paths
    Origin   string
    Mappings []Mapping
}

func (r *Result) LookupLine(goLine int) (Mapping, bool)

type Mapping struct {
    GoLine int
    GoCol  int
    Pos    syntax.Pos
    Node   string
}

type Diagnostic struct {
    Code string
    Msg  string
    Node string
    Pos  syntax.Pos
}

type ErrorList []Diagnostic
```

`Diagnostic` and `ErrorList` implement `error`. Failures use the constants
`CodeUnsupported`, `CodeType`, `CodeUndefined`, `CodeExpr`, `CodeResult`, and
`CodeBridge`, with values `LOWER-EUNSUPPORTED`, `LOWER-ETYPE`,
`LOWER-EUNDEFINED`, `LOWER-EEXPR`, `LOWER-ERESULT`, and `LOWER-EBRIDGE`.
Not every code is emitted by this initial slice. Returned diagnostics are
sorted by source position. `Compile` never returns a partial `Result`.

The source map anchors emitted statements and function declarations. Generated
ordinal comments keep these mappings stable through gofmt. `LookupLine` is an
exact lookup, not a nearest-line search. Generated comments contain no input
filename; neither `syntax.File.Name` nor `Options.Origin` affects emitted bytes.
Private helper names are allocated against identifiers present in the input.

## Supported foundation

- Scalar `var` and `const` declarations, short declarations, typed direct
  function calls and multiple results.
- Ordinary scalar functions, parameters, named results and returns. Parameter
  defaults, variadics, receivers, generics and marked functions are deferred.
- Positioned scalar literals, identifiers, parentheses, unary/binary operations
  and scalar conversions. Native Go type checking rejects bad arity, undefined
  names, invalid operand types and unrepresentable constants.
- Typed `if`, `for`, assignments, compound updates and branch statements.
- `print`/`println` through the standard library. Both use stdout, matching the
  Bash++ interpreter; native Go's predeclared print functions use a different
  stream and therefore are not emitted directly.
- An explicit `shellrt` output boundary for literal-format `printf` using
  `%s`, `%d`, `%%` and a small escape set, and ordinary argument-only `echo`.
  Unsupported format conversions, flags and dynamic formats are diagnosed.
- Simple `$name`/`${name}` interpolation and arithmetic expansion, and simple
  shell assignment writing through an existing typed scalar binding. Bare
  scalar identifiers in command arguments resolve as values inside a typed
  function; at script level they retain ordinary shell literal semantics.

Declarations with initializers remain in the entry function in their original
execution order. An output statement before an initializer still executes
first. Package-level function emission does not move executable initializers
into Go package initialization. Function access to script-local bindings is
currently an explicit unsupported/undefined case, requiring the later shared
state/capture representation. An empty script still emits an empty `main`.
Source declarations named `main` need the later program-entry lowering and
are explicitly rejected by this foundation.

The public Go signatures of supported functions remain scalar Go signatures.
Unused local declarations receive `_ = name` because unused-local diagnostics
are outside this source profile. All Go-backed checks are static: input
initializers and command bodies are never executed during `Compile`.

## Runtime and artifact building

Typed-only output uses only native Go and, when printing, the standard library.
It has no dependency on `sh`, the parser, or the interpreter. Mixed output
imports the configured runtime path. The default `lower/shellrt` package itself
uses only the standard library.

The runtime exports `Printf(format string, args ...any) error`,
`Echo(args ...any) error`, `Word(any) string`, and replaceable `Stdout` and
`Stderr` writers. Generated glue uses `Fail(error)`, `Status`, and `Exit()` to
retain command failure status through subsequent commands. Error output alone
does not abort an otherwise continuing script. This is a limited scalar state
bridge; it does not implement full shell environment, cwd, descriptors, traps,
subshells, pipelines, dynamic commands, task ownership, or `set -e`.

For a mixed artifact in a fresh directory, declare the runtime dependency in
that directory's module. During local development, an explicit replacement
can bind the exact engine checkout under review:

```text
module compiled-example

go 1.26.5

require mvdan.cc/sh/v3 v3.0.0
replace mvdan.cc/sh/v3 => /absolute/path/to/reviewed/sh
```

Then run `GOWORK=off go build -o program generated.go`. The replacement is
local build configuration, never embedded in generated source. Distribution
must replace it with an available module version containing the selected
runtime revision, or an explicitly managed build workspace. Record the source
commit, toolchain, module binding and artifact hash. A plain `go build` from
an unrelated directory without a module binding cannot resolve mixed output's
runtime import. No umbrella `go.work` is required.

## Extension seams and remaining work

`compile.go` owns statement/expression dispatch, bindings, Go checking and
source maps. `words.go` owns the boundary between already-classified typed
arguments and shell words. Some typed call/return fields are still `Word`
objects; the compiler validates their committed expression spelling with
`go/parser`. That decoding never chooses whether a region is Go or shell:
only the original syntax tree determines the region.

Follow-on emitters should replace individual unsupported cases, retain a
positioned error for every unimplemented variant, and add same-source
interpreted/compiled observations. Do not widen shell word semantics by
rendering arbitrary source text as Go. Every new shell capability belongs in
an explicit runtime operation. A future dynamic-shell bridge may interpret
an explicitly identified shell region; certified typed nodes must continue
to emit ordinary Go operations.

The scalar scopes are an initial binding representation, not the final shared
capture/type system. The three-clause loop initializer is currently represented
by an enclosing block; closures are unsupported, and later closure lowering
must implement Go's per-iteration variable semantics before accepting captures.
Deep readonly identity, null checking, interfaces, rich values, agentic scope,
concurrency and cancellation, imports, defer/panic semantics and full process
boundaries require their separate lowering implementations. Runtime numeric
panics and broad diagnostics also require their corresponding semantic slices.

## Foundation verification

`go test ./lower/...` builds every positive emitted fixture in a fresh module,
removes the generated source, runs the resulting binary in another directory
with no shell or Go on PATH, and compares stdout, stderr and exit status against
the interpreter on the same source and environment. Cases cover native scalar
control flow, mixed scalar state, named/multiple results, initializer side-effect
order, shell-word scope, failure restoration and final failure status. Separate
checks cover deterministic bytes/maps, empty programs, name collisions,
unsupported syntax and invalid typing. Tests use the current toolchain version
for their temporary module so they run at the repository's compatibility floor
as well as the Go 1.27 profile toolchain.
