# Contextual (reverse) inference for function values — Sprint 117 / L141

## Problem

Arguments reach a typed call already expanded to strings. A closure argument is
still a value in that model — the string is the registry handle
`func@bashpp:N` — but a plain function NAME is not: the callee saw the four
letters `f2` and nothing behind them, so a parameter declared `func() int`
rejected it:

```
BASHPP-EARG-FUNCTYPE: consume requires func()(int) for parameter fn
```

Real Go accepts the same three programs, and so does the compiled artifact this
repo's `lower` produces for them. Only the interpreter disagreed.

## Repair

Two steps, in `interp/bashpp_funcvalue.go` (new file) plus a four-line hook in
`bashPPInvoke` and a four-line guard in `bashPPInstantiateFunc`:

1. **Named function value at a concrete func parameter.** Before the call's type
   check runs, an argument that names a declared function, landing on a
   parameter whose declared type is a func type, is converted into a function
   value — the same handle a closure argument carries. Everything downstream
   (the type check, the frame, `fn()` in the body) is unchanged.
2. **Contextual (reverse) type argument inference.** When the named function is
   generic, its type arguments come from the signature the PARAMETER declares,
   not from any argument value — a function used as a value is not called here.
   `consume(f2)` at `func() int` infers `P = int` from the result slot.
   Inference is a proposal only: the instantiated signature must then equal the
   parameter's, and the inferred arguments must satisfy their constraints.

A value instantiated this way is called at the types it carries;
`bashPPInstantiateFunc` no longer re-infers type arguments for a function value
whose type parameters are all already bound (re-inference would ask a
`func() int` value to determine `P` from zero arguments).

Reference: `go/types` reverse inference, worked examples in the Go 1.27 tree at
`src/internal/types/testdata/examples/inference2.go`.

## Supported subset — stated exactly

Supported:

- A named non-generic function passed to a parameter of func type.
- A named generic function passed to a parameter of **concrete** func type, with
  its type arguments inferred from that parameter's parameter and result slots
  (Go's `v4 func() int = f2`, `g1(f1)`, `g2(f4)` shapes, in argument position).
- A closure value, exactly as before — that path is untouched.

Not supported (unchanged by this work, and NOT silently accepted):

- A generic CONSUMER whose own func-typed parameter is still open — Go's
  `g4(f6)` / `g5(f6, f7)`. Inference across two open signatures is not
  attempted; the argument is reported as an ordinary signature mismatch.
- `f[T]` written as a value expression (`fn := echo[int]`), which this dialect's
  grammar does not parse as an instantiation — it reports
  `BASHPP-ESELECTOR-ROOT`. Unchanged; no grammar work was done here.
- Reverse inference in assignment (`var fn func() int = f2`) and in `return`
  position. Only argument position is repaired. The assignment form is out of
  reach from `interp` alone in any case: `var fn func() int = f2` does not parse
  today — `3:13: a command can only contain words and redirects; encountered "("`
  — and grammar work was out of scope here.
- A method value (`x.M` passed as a function value).

## Evidence

Code SHA: see the commit carrying this file. Toolchain: Go 1.27.0
(`GOTOOLCHAIN=auto` resolving `go1.27.0` from the module cache),
`PATH=/bin:/usr/bin:/opt/homebrew/bin`,
`GOCACHE=/tmp/s117-contextual-inference-worker-cache`.

Fixtures: `/tmp/s117-l141-proof/{closure-context-control,named-context-control,contextual-generic-function-value}`,
used as found — sources unchanged.

### Three-control proof (native Go / compiled artifact / interpreter)

| control | `native` (Go 1.27) | `artifact` (lower→Go) | interpreter (this repo) |
| --- | --- | --- | --- |
| closure-context-control | exit 0, `accepted` | exit 0, `accepted` | exit 0, `accepted` |
| named-context-control | exit 0, `accepted` | exit 0, `accepted` | exit 0, `accepted` |
| contextual-generic-function-value | exit 0, `accepted` | exit 0, `accepted` | exit 0, `accepted` |

Before the repair the interpreter column read, for the last two rows:
`exit 2, BASHPP-EARG-FUNCTYPE: consume requires func()(int) for parameter fn`.
Each `generated.go` was also rebuilt from source with Go 1.27 and rerun, giving
`accepted` in all three cases; `lower` was not modified.

Inference is observable, not just permissive: with `consume` calling its
parameter, `func f2[P any]() P` at `func() int` prints `0` — int's zero value,
which is what `P = int` means — and `func echo[P any](v P) P` at
`func(int) int` returns its argument.

### Negative controls

Each is rejected by real Go 1.27 as well (message quoted from `go build`):

| control | Go 1.27 | interpreter |
| --- | --- | --- |
| result mismatch (`func() string` at `func() int`) | `cannot use fixed (value of type func() string) as func() int value` | `BASHPP-EARG-FUNCTYPE` |
| arity mismatch (`func(int, int) int` at `func() int`) | `cannot use two (value of type func(a int, b int) int) as func() int value` | `BASHPP-EARG-FUNCTYPE` |
| generic arity mismatch (`echo` at `func() int`) | `type func[P any](v P) P of echo does not match func() int (cannot infer P)` | `BASHPP-EARG-FUNCTYPE` |
| generic signature mismatch (`func(P, P) P` at `func(int, string) int`) | `type func[P any](a P, b P) P of pairwise does not match func(int, string) int` | `BASHPP-EARG-FUNCTYPE` |
| uninferable type parameter (`func hidden[P any]() int` at `func() int`) | `cannot infer P` | `BASHPP-EGENERIC-INFER` |
| unsatisfied constraint (`func zero[P Number]() P` at `func() string`) | `string does not satisfy Number (string missing in ~int \| ~float64)` | `BASHPP-EGENERIC-CONSTRAINT` |
| undeclared name | `undefined: missing` | `BASHPP-EARG-FUNCTYPE` |

The one deliberate difference: for a generic function whose SHAPE cannot line up
at all (`echo` at `func() int`), we report the signature mismatch rather than
Go's "cannot infer P" — there is no slot pairing to draw a binding from, so the
mismatch is the more direct statement. `BASHPP-EGENERIC-INFER` is reserved for
the case Go reserves it for: the shapes line up but the context never mentions
the type parameter.

Tests: `interp/bashpp_funcvalue_test.go` —
`TestBashPPContextualFuncValueControls`, `...Calls`, `...Rejections`.
