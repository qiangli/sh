# Bash++ polyglot source fences

Bash++ accepts a naked source fence only at column one and only when the
Bash++ dialect is selected:

```bash
~~~python
def add(a: int, b: int) -> int:
    return a + b
~~~

answer := add(20, 22)
```

The opening and closing delimiters contain the same run of three or more
tildes. The opener is immediately followed by a language identifier and may
end with ` as NAME`. The body is raw foreign source; like the rest of the shell
parser, CRLF input is normalized to LF. Backtick and quote runs retain their
Classic shell meanings.

A fence is a declaration unit, not an implicit command. Python, TypeScript,
Rust, C, C++, and Go are implemented adapters. Python blocks may contain one module docstring followed by
ordinary synchronous, undecorated function declarations. Imports, classes,
async declarations, decorators, executable top-level statements, and
non-literal defaults are rejected during preparation without executing the
module.

Public functions in an unaliased block are promoted into the Bash++ callable
namespace. A named block instead exposes qualified calls and promotes nothing:

```bash
~~~python as py
def add(a: int, b: int) -> int:
    return a + b
~~~

answer := py.add(20, 22)
```

All Python blocks in one parsed source unit form one private module and must
make the same alias choice. The module is held by the runtime, never by a shell
variable or environment entry. Collisions with Bash++ functions,
declarations, imports, aliases, or another promoted export are hard source
errors.

Complete scalar annotations (`int`, `float`, `str`, `bool`, `bytes`, `None`,
and `Any`) produce a typed Bash++ wrapper. An incomplete or unsupported
signature produces a dynamic variadic wrapper returning `(any, error)`.
Arguments and results cross a Go-owned NDJSON protocol to one persistent
`python3` worker per module; stdout and stderr remain streams. Cancellation
terminates the worker, and a later call recreates it from the immutable module
plan. No cgo or CPython ABI binding is used.

TypeScript uses `~~~typescript` (or the `~~~ts` shorthand) and follows the
same promotion and alias rules:

```bash
~~~typescript as ts
interface Pair { left: number; right: number }
export function sum(pair: Pair): number {
    return pair.left + pair.right
}
~~~

answer, err := ts.sum('{"left":20,"right":22}')
```

The adapter loads the official Apache-2.0 `typescript` npm compiler module in
an isolated Node process. TypeScript 5.9/6.x uses its established `Program`
compiler API; TypeScript 7.x uses Microsoft's new Go-backed synchronous
snapshot/checker API and official emitter. Both paths use the official AST,
checker, diagnostics, and CommonJS output; Bash++ does not implement a second
TypeScript parser. Analysis never evaluates the source. The emitted artifact
is stored in the immutable private-module plan and executed on first call by
one persistent Node worker.

Function declarations and declaration-safe forms such as interfaces and type
aliases are accepted. Imports and arbitrary executable top-level statements
remain errors until Bash++ publishes module resolution and initialization
semantics. Function bodies may use any TypeScript feature supported by the
selected compiler and ES target. Types representable by Bash++ receive typed
wrappers; other valid signatures receive the dynamic `(any, error)` wrapper.

Node and the compiler are discovered lazily. `BASHPP_NODE` overrides the Node
executable and `BASHPP_TYPESCRIPT_MODULE` overrides the module name or absolute
entrypoint; defaults are `node` and normal `require("typescript")` resolution.
Scripts with no TypeScript blocks perform no discovery. The process boundary
provides lifecycle and protocol isolation, not a security sandbox: called code
runs with the shell account's authority.

The fence start site is Class E because a language fence is a valid Classic shell
command. It is therefore deliberately narrow: indentation, an argument
position, `command ~~~python`, or a quoted opener remains ordinary shell. The
feature is inert in Classic and POSIX dialects. Merely reading or rendering a
Markdown file never invokes the Bash++ parser or worker.

## Native C and C++ fences

`~~~c`, `~~~cpp`, and the `~~~cxx` alias expose non-`static`, top-level
function definitions through the same direct/qualified call model. Clang's
JSON AST is the signature authority; Bash++ does not parse C or C++ itself.
The combined C17 or C++20 translation unit is compiled once, and its native
worker artifact is stored in the immutable module plan and embedded in lowered
Go output.

Supported signatures contain `void`, booleans, ordinary or fixed-width
integers, `float`, `double`, C strings, and C++ `std::string` values or const
references. C++ namespace members are not exported; overloads, methods, templates, variadics,
aggregates, and other pointer types fail during preparation. Private helpers
use `static` linkage. C++ exceptions and worker failures become Bash++ call
errors; ordinary stdout and stderr remain visible.

Compiler selection is source-relative and deterministic: a matching
`bashpp.yaml`/`bashpp.json` runtime, then `BASHPP_CC` or `BASHPP_CXX`, then
`clang`/`cc` or `clang++`/`c++` on the recorded `PATH`. The source directory
and discovered project root are include roots for quoted includes
(`#include "libavutil/version.h"` resolves against the checkout; passed as
`-iquote`, so a project file named like a standard header — `VERSION` beside
C++20's `<version>` on a case-insensitive filesystem — never shadows the
compiler's own `<…>` search), but Bash++ does not infer build flags or link
third-party libraries. Each call launches a fresh worker, so
native globals do not persist between calls. The boundary is process
isolation, not a sandbox; fenced native code has the shell account's authority.

## Go fences

`~~~go` exposes exported top-level functions through the same direct or
`as NAME` call model. Imports and function declarations are accepted; package
clauses, methods, top-level variables/types/constants, variadics, and more than
one value result are refused. Supported parameters/results are booleans,
integer and floating-point kinds, strings, and `[]byte`. A final `error` result
is the call error, so both `func F() error` and `func F() (T, error)` follow the
ordinary Go convention.

The nearest `go.mod` defines the project. `BASHPP_GO` selects an explicit Go
executable; otherwise the adapter uses `go` on PATH, then the `bashy go`
provisioner. Module/workspace files, `GOFLAGS`, the launch environment, and the
executable identity participate in the environment fingerprint.

Preparation writes generated sources only to a private temporary directory.
`go build -overlay` makes them appear inside the checkout module for the build,
so a fence may import checkout packages while the checkout remains
byte-identical. The worker is stored in the immutable plan and embedded in
lowered output. Each call launches a fresh worker, exchanges one JSON
request/result frame, exposes ordinary stdout/stderr, and turns panics,
build/worker failures, malformed frames, and non-nil trailing errors into
Bash++ call errors.
