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
Rust, C, C++, Go, PowerShell, C#, Bash, POSIX sh, and JavaScript are implemented adapters. Python blocks
may contain one module docstring, module-level `import` / `from ... import`
statements, literal constant assignments (`NAME = 42`, strings, numbers,
booleans, `None`, and tuples/lists/dicts of those), and ordinary synchronous,
undecorated function declarations. Classes,
async declarations, decorators, other executable top-level statements, and
non-literal defaults are rejected during preparation without executing the
module. Imports execute when the worker first loads the module, so an import
failure surfaces as a call error rather than at preparation.

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
aliases are accepted. Imports of Node built-ins (`node:fs`, `path`, ...) type-check
without `@types/node` (an ambient shorthand declaration is supplied when none is
reachable). Arbitrary executable top-level statements remain errors until
Bash++ publishes initialization semantics. Function bodies may use any TypeScript feature supported by the
selected compiler and ES target. Types representable by Bash++ receive typed
wrappers; other valid signatures receive the dynamic `(any, error)` wrapper.

JavaScript uses `~~~javascript` (or `~~~js`) and reuses the TypeScript
runner: the same compiler is run with `allowJs` and no type checking, so the
fence is parsed, imports are accepted as above, and JSDoc (`@param`,
`@returns`) supplies types. A function without complete JSDoc gets the dynamic
`(any, error)` wrapper. The `javascript`/`js` fence spellings do not collide
with shell syntax: like every fence language they are recognised only at the
`~~~` opener. Under TypeScript 7.x, JavaScript exports are always dynamic.

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

## PowerShell fences

`~~~powershell` (aliases `~~~pwsh`, `~~~ps1`) exposes its public function
definitions through the same direct or `as NAME` call model as the other
islands (`~~~powershell as ps`, then `ps.Shout("hi")`). One persistent
PowerShell 7 worker serves each module for its lifetime.

**Exported-call convention.** A function with typed parameters is a typed
call: `[string]` takes a Bash# string, `[long]` an int, `[double]` a float,
`[bool]` a bool, `[byte[]]` bytes, and collections cross as a Bash# `Object`
(JSON at the boundary). A function with no declared return type is dynamic
and binds both results (`shouted, serr := ps.Shout("hi")`). A `Verb-Noun`
export (`Get-Item`) is not a Bash# call name — the hyphen is a minus in a
qualified call and has no lowered wrapper — so it is reached by string key
(`ps.call("Get-Item", path)`) while staying loaded for the fence's other
functions. An aliased fence's exports also run as command words
(`ps.Greet world`) in lowered shell regions.

**Value, error and pipe boundary.** Scalars and bytes cross as values; lists
and maps cross as JSON through the island boundary, never as a PowerShell
object pipeline — the shell pipe stays bytes. A terminating error or an
uncaught `throw` is the call's error (bound to `err` in a typed call, exit
status `1` with the message on stderr in command form); non-terminating
errors (`Write-Error`) go to stderr without failing the call. A function
that reads pipeline input — a `process` block, a `$input` reference, or a
`ValueFromPipeline` parameter — runs as a filter over the command's stdin,
one line per item; any other function never reads stdin. Text on stdout and
stderr is normalized to `\n` on every OS; `byte[]` output passes through
untouched. Bash# adopts neither `$ErrorActionPreference`, `ExecutionPolicy`,
providers, case-insensitivity nor the Verb-Noun command system.

**Runtime.** Both fences resolve through bashy's toolchain resolver to one
pinned, digest-verified PowerShell 7.6.6 archive per platform, downloaded on
demand and cached; a fence never resolves its tool from `PATH`, and
`BASHPP_PWSH` is the one explicit override. The fence lowers: the lowered
program embeds the fence source and the environment plan resolved at compile
time and starts the same worker, so interpreted and lowered output match
byte for byte — but **the lowered binary still needs PowerShell 7 on the
target at run time**, as a lowered Python fence needs Python. Nothing from
Microsoft is compiled into, linked with or vendored in bashy. Verified
licence evidence (MIT root license in every archive, the common MIT/BSD-2
notice, and the closed native payloads the Windows zips add) is inventoried
per archive in `bashy/docs/fence-toolchain-licenses.md`.

**Windows PowerShell 5.1 is not the guest runtime.** The fence always runs
on the provisioned PowerShell 7.

Full contract: `bashsharp/docs/powershell-csharp-fences.md`.

## C# fences

`~~~csharp` (alias `~~~cs`) exposes `public static` methods through the same
direct or `as NAME` call model (`~~~csharp as cs`, then `cs.Square(6)`). It
compiles through `Add-Type` in the same provisioned PowerShell 7 worker as
the PowerShell fence, so one pinned archive serves both languages.

**Exported-call convention.** A `~~~csharp` fence is a declaration unit, not
a program: leading `using` lines carry into the generated unit, then each
`public static` method is one Bash# callable with one signature. Instance
methods, constructors, non-public members, overloads, generic methods and
`ref`/`out` parameters are refused at preparation; there are no top-level
statements and no `Main`. A fence-declared class or struct result crosses as
a dict of its public properties and fields, an enum as its name, and every
other parameter/result type maps by the shared value table (`string`,
`long`/`int`, `double`, `bool`, `byte[]`, objects as JSON).

**Value and error boundary.** Arguments convert to the declared parameter
types (a missing argument takes the parameter's default); `Console` output
during a call is that call's stdout and stderr. An uncaught exception is the
call's error carrying the exception type and message; a typed call binds it
with one extra `err` result. Compilation failures report with
source-location diagnostics naming the script file and the fence's own lines
before the workflow runs, and a failed compile leaves no cache entry. The
compiled assembly and export list are cached under the user cache, keyed by
the generated source and a content hash of the `pwsh` launcher and the
compiler assemblies beside it. A `#:package` / `#:` directive or
`#r "nuget: …"` line is refused at prepare: NuGet restore is a separate
follow-up, not part of this contract.

**Runtime.** Same provisioned PowerShell 7.6.6 as the PowerShell fence (same
pins, same `BASHPP_PWSH` override, same per-archive licence inventory in
`bashy/docs/fence-toolchain-licenses.md`). The fence lowers with the same
parity and the same requirement: **the lowered binary still needs the
provisioned PowerShell 7 where it runs**; it embeds neither PowerShell, the
CLR nor a compiled C# assembly. Native AOT is not attempted.

**Windows PowerShell 5.1 is not the guest runtime.** C# compiles through the
provisioned PowerShell 7 on every OS; no .NET SDK is installed or required.

Full contract: `bashsharp/docs/powershell-csharp-fences.md`.

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

## Text fences and the runner override (B30)

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
`runner <verb> <file> [args…]`. A command runner's stdout, with its trailing
newlines removed as a command substitution reads them, is the result; a
Bash++ function runner (`func r(verb string, file string, args ...string)
string`) returns the result, or prints it — a function that returns nothing
has its stdout as the result the same way; a non-zero status is the call
error. The alias
exposes exactly the methods the runner declares when invoked as
`runner methods <file>` (one export JSON object per line — `name`, optional
`signature`, optional `effects` array (or comma-separated `effect`), and an
optional boolean `agentic`); an empty
or malformed declaration is a refusal, an undeclared method is an unknown
callable. The runner's own top-level declaration is evaluated when the unit
is prepared, so the fence may name a function declared anywhere in the unit.
A method declaring `"agentic": true` requires an explicit `agentic { }`
caller or an agentic function, independently of its effect cap. The interpreter
checks this before dispatching the host method; it never calls a model itself.
Agentic foreign methods currently refuse lowering by name with the interpreted
route. A host method may declare `params: ["string"]` and
`results: ["string", "error"]`: both partial output and the typed error are
retained, and the error supports `.Error()`.

Every text fence, row or runner, needs an alias: its methods are known only
after preparation, too late for the parser's bare-call look-ahead. The body
is materialized under the cache directory, keyed by the plan id, never into
the checkout; built-in rows embed it verbatim in lowered output. A runner
fence whose runner is a Bash++ `func` of the unit with the runner signature
**lowers with the program**: `methods` is answered at compile time by that
declaration alone, evaluated through the interpreter (a fence is prepared
before the unit's first statement runs, so the runner sees no program state
either way), the plan carries the exports the interpreter would prepare, and
the lowered program calls the lowered function directly with its stdout
captured as the interpreter captures it — interpreted and lowered runs agree
on stdout and status. A transpiled binary never depends on a shell on the
target, so every other runner shape is refused at compile time by name with
the route: a shell-function runner (it lives in the interpreter session), a
builtin or registered-command runner (it lives in the shell that runs the
program), and the rows whose processor is the running shell (`dag`, `skill`)
— wrap the command in a Bash++ `func` runner to lower it. A `func` runner
that itself calls a verb of the host shell (`"$BASH" zig …`) lowers, but the
binary then needs that shell on the target: keep such a runner interpreted.
The format is never inferred: an opener whose type
is not in the table and carries no runner is rejected at prepare. The
decision record and the row table are
`bashsharp/docs/fenced-text-blocks-plan.md`.

The manifest rows the host shell registers (`cargo`, `pyproject`, `gomod`,
`cmake`, `makefile`, `package`) run their processor in the caller's
directory with the manifest under the fence root: a row may **shadow**
entries of the caller's directory beside the manifest (cargo's `src`), or
present the manifest through a go-style **overlay** (`{overlay}`), and a
manifest row may be the **module** of a code fence in the same unit
(`gomod` → `~~~go`: the caller's directory is the Go module with no `go.mod`
in it). A materialized manifest is left alone once written — the fence
directory is keyed by content, and a processor's own edits to it (`go mod
tidy`) belong to that fence.

### Embed: the fence body from a file

A body too large to carry inline, or a file other tools also read
(`package.json`, `pyproject.toml`, a Dockerfile), is embedded instead:

```
embed <type> "./<path>" [as <alias>] [!<runner>]
```

It is exactly the fence `~~~<type> [as <alias>] [!<runner>]` with the file's
bytes as its body: same alias, verbs, runner, effects and lowering. The path
is a double-quoted literal starting with `./` or `../`, resolved against the
directory of the script that holds it; the file is read when the script is
parsed (lowered: when it is compiled). Like a fence it is claimed only at
column 1 and only in Bash++; any other shape — or `command embed …` — is an
ordinary command.

### Write your own fence

A language or a toolchain the engine has never heard of needs a runner and
nothing else. Provide one of two ways — registered once with the host shell
and named from any script, or inline in the script as a Bash++ `func` or a
shell function:

```
func builder(verb string, file string, args ...string) string {
  case "$verb" in
    methods) printf '%s\n' '{"name":"build","effects":["write","exec"]}' '{"name":"run","effects":["exec"]}' ;;
    build)   mycc "$file" -o "${file%/*}/app" && echo "${file%/*}/app" ;;
    run)     "${file%/*}/app" "${args[@]}" ;;
  esac
}
~~~mylang as m !builder
...
~~~
bin := m.build()
m.run("arg")
```

The contract, in full: the runner is invoked as `runner <verb> <file>
[args…]` in the caller's directory with the caller's environment, plus
`BASHPP_FENCE_TYPE` and `BASHPP_FENCE_FILE`; `methods` is asked once, when the
unit is prepared, and its answer — one JSON object per line, `name`, optional
`signature` (the bridge honors typed parameters and results; the default is
`(args ...string) string`), optional `effects` — is the alias's whole method
set; the materialized body is `$2` and its directory is the fence's own
scratch, stable across calls for the same body; stdout (trailing newlines
removed) or a returned string is the value; a non-zero status is the call
error; declared effects are what the contract layer checks, exactly as for a
built-in row. A runner name resolves to a function in the unit, a builtin or
a command the host registered — never a PATH program.
