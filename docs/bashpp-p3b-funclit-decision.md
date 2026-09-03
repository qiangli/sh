# Bash++ P3-B — function literals, closures and variadics

Sprint 114 · story `b2c7e6409da1` · 2026-09-03

P3-B adds Go function **literals** and **variadic** parameters to the typed
functions delivered by P3-A. Nothing in P3-A changes: named declarations,
declared results, `return`, `defer` and the shell status bridging keep the
behaviour their tests pin, and a literal reuses the same invoker rather than
introducing a parallel one.

## Claimed sites

A literal appears wherever a value does, and each site commits on a prefix
stock bash already rejects (Class R), so claiming it takes nothing away from a
working script:

| Spelling | Node |
|---|---|
| `greet := func(who string) { … }` | `BashPPShortDecl.FuncLit` |
| `n := func() int { … }()` | `BashPPShortDecl.Call` with `BashPPCall.FuncLit` |
| `func(n int) { … }(1)` | `BashPPCall.FuncLit` |
| `defer func() { … }()` | `BashPPDefer.Call` with `BashPPCall.FuncLit` |
| `return func(n int) int { … }` | `BashPPReturn.FuncLit` |

`func(…)` and its `(` must be adjacent, as gofmt writes them: a spaced
`func (…)` is the bash function-definition shape and stays shell.

### The shape that is not claimed

`func() { … }` **at a command position** is the bash definition of a function
named `func`, which stock bash accepts today. Only the `(` after the matching
`}` distinguishes it from a parameterless literal, and that is unbounded
lookahead — the property `syntax/bashpp_startsites.go` exists to forbid. So a
command-position literal must carry at least one parameter. A parameterless
invocation is spelled `_ := func() { … }()`, where the `:=` has already opened
the region, and `defer func() { … }()` is unaffected because the parenthesis
after two words is a syntax error however the list is spelled.

## Values

A closure is a Go pointer with a captured scope; a shell variable holds bytes.
The variable therefore holds a **handle** — `func@bashpp:<n>` — into a
per-runner registry, and the closure itself travels with the runner, where the
existing `bashPPCloner` copies it for a subshell alongside the scopes it
captured. A name bound to a handle is callable by that name.

Capture is per **evaluation**: two calls to the same factory close over
different cells, and a closure keeps its cells alive after the frame that
declared them returns. Mutation through a captured cell is visible to every
holder, because the snapshot shares cells rather than copying values.

The function type is spelled by the bare word `func` — `func apply(cb func, n
int)` — rather than Go's `func(int) error`, whose parentheses and commas would
have to survive shell word splitting inside a parenthesised signature.

## Variadics

`func f(head string, rest ...int)` binds `rest` as an indexed variable, so the
body reads it with `"${rest[@]}"` / `${#rest[@]}` and forwards it with
`rest...`. Zero arguments still bind the name, to an empty list. Go's rules are
enforced: `...` only on the final parameter, only one name in that group, never
on a result, and `f(xs...)` only into a variadic callee.

Argument checks are arity plus a narrow type check: the numeric and boolean
types, and `func`, whose values are recognizable handles. Every other spelling —
`string`, a dotted selector, a script-declared type — is admitted, and an
untyped parameter admits everything.

## Deliberately out of scope

Methods and receivers (P3-C), `panic`/`recover` (P3-D), full function types,
literals as arguments to another call, and a call in return position
(`return f(x)`, a P3-A gap that predates this story).
