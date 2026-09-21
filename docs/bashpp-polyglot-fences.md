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
Rust, C, C++, Go, Bash, and POSIX sh are implemented adapters. Python blocks
may contain one module docstring followed by ordinary synchronous,
undecorated function declarations. Imports, classes,
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

## Rust fences

`~~~rust` (or `~~~rs`) exposes top-level `pub fn` declarations through the
same direct or `as NAME` call model. The fence body is the crate root of a
generated cargo package that depends on the standard `serde` (with `derive`),
`serde_json`, and `base64` crates, so a fence uses serde exactly as any Rust
program does — there is no Bash#-specific carrier type:

```bash
~~~rust as rs
use serde::{Deserialize, Serialize};

#[derive(Serialize, Deserialize)]
pub struct Item { pub name: String, pub qty: u32 }

#[derive(Serialize, Deserialize)]
pub struct Order { pub items: Vec<Item> }

pub fn total(order: Order) -> u32 { order.items.iter().map(|i| i.qty).sum() }
pub fn make(qty: u32) -> Order { Order { items: vec![Item { name: "a".into(), qty }] } }
~~~

order := rs.make(2)
sum := rs.total(order)
```

Booleans, integer and floating-point kinds, `String`/`&str`, `Vec<u8>`
(bytes), and `()` keep typed wrappers. Every other parameter or result type —
a user struct, `Vec<Struct>`, `Option<T>`, a map, a tuple — is an Object that
crosses the boundary as JSON through `serde_json::from_value` and `to_value`;
a type without `Serialize`/`Deserialize` fails at preparation with rustc's own
diagnostic. Borrowed parameters (`&T`, `&[T]`, `&str`) deserialize to owned
values that are lent to the call; borrowed results, `&mut`, lifetimes, and
`impl`/`dyn` types are refused. A `Result<T, E>` result unwraps `T` and turns
`Err(e)` into a call error via `Display`; a panic is a call error too. A
mismatched argument is serde's message (`invalid type: string "hello",
expected struct Order`, ``missing field `items` ``).

One persistent worker per fence serves the module for its lifetime, so
`static` state persists across calls; `Vec<u8>` crosses as an explicit bytes
value, never a JSON array. Requests and responses are the same newline JSON
protocol the Python worker speaks (fd 3 on Unix; the marker-framed stdout on
Windows), and island stdout/stderr are captured per call and replayed on the
shell's streams — `dup2` on Unix, `SetStdHandle` on Windows. Cancellation or
`Close` terminates the worker deterministically; the next call restarts it
from the immutable plan, which carries the built binary so a lowered program
needs neither cargo nor rustc.

Preparation runs `cargo fetch --offline` (then online only if the registry
cache lacks a crate) and `cargo build --frozen` in a private temporary
package, with a target directory shared under the user cache so the serde
crates compile once per toolchain. `BASHPP_RUSTC` or a `bashpp.yaml` runtime
selects rustc, and cargo is the one beside it, else the embedder's tool
resolver (`cargo`), else PATH. Indented `pub fn` items (impl methods) and
non-`pub` functions are private helpers.

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

## Shell dialect islands

`~~~bash` and `~~~sh` contain only top-level shell function declarations.
Their public functions use the same direct or `as NAME` call model, with a
variadic string boundary: call arguments become `$1`, `$2`, and so on; the
function's exact stdout is its string result; stderr remains stderr; and a
non-zero status is a Bash++ call error.

These adapters never select or start a host shell. `bash` parses and runs with
Bashy's embedded Bash-5.3 dialect, while `sh` uses its embedded POSIX dialect.
Every invocation creates a fresh child Runner from an environment snapshot and
the invoking directory, so variables, functions, options, and `cd` changes
cannot leak to another call or the parent Bash++ runner. Lowered programs use
the same embedded adapter. Code inside an island retains ordinary shell
authority—an explicit external command may still start that command—but the
fence runtime itself has no worker, toolchain, cache artifact, or host-shell
dependency.

## Text fences and the runner override (planned — B30)

The same fence may carry a declarative **text** artifact instead of source.
The opener then takes an optional runner override after the alias:

```
~~~<type> [as <alias>] [!<runner>]
```

`~~~<type> as !<runner>` is shorthand for `as <runner> !<runner>`. For the
built-in text types (`dockerfile`, `tf`, `k8s`, `helm`, `dag`, `skill`;
`compose` is reserved) the alias exposes the processor's verbs from a fixed
table. With `!<runner>` the runner is a Bash++ function in the same unit or a
registered command of the host shell — never a PATH lookup — invoked as
`runner <verb> <file> [args…]` with stdout as the result and a non-zero status
as the call error, and the alias exposes exactly the methods the runner
declares when invoked as `runner methods <file>` (one export JSON object per
line, optionally carrying an `effect` atom that `@guard` enforces). An
undeclared method is a prepare-time error; an empty or malformed declaration
is a refusal. The body is materialized under the cache directory, keyed by the
plan id, never into the checkout, and is embedded verbatim in lowered output.
The format is never inferred: an opener whose type is not in the table and
carries no runner is rejected at prepare. The decision record and the row
table are `bashsharp/docs/fenced-text-blocks-plan.md`.
