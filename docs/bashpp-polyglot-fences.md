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

A fence is a declaration unit, not an implicit command. Python is the first
implemented adapter. Its blocks may contain one module docstring followed by
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

The fence start site is Class E because `~~~python` is a valid Classic shell
command. It is therefore deliberately narrow: indentation, an argument
position, `command ~~~python`, or a quoted opener remains ordinary shell. The
feature is inert in Classic and POSIX dialects. Merely reading or rendering a
Markdown file never invokes the Bash++ parser or worker.
