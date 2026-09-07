# Native callables and source-ordered globals

The callable emitter extends `lower` with native closures and captured cells,
function/method values, multiple and named results, inferred concrete function
parameters and returned function literals, native defer, scalar control flow,
constant groups, and ordinary import calls. It consumes the positioned parser
nodes; shell and typed regions are never reclassified from raw source.

## Globals and captures

Compilation has two static passes. The first uses Go's checker to infer global
storage types from declarations and initializers; it executes nothing. The
second emits zero-initialized package storage and runs every initializer in the
entry function at its original source position. Package initialization cannot
move a source initializer ahead of an earlier command. Both passes produce the
same deterministic public source-map contract.

Named functions capture only global bindings declared before the function's
source declaration. The interpreter captures that lexical environment; a
later declaration does not become visible just because a native Go package
would make it visible. Unsupported forward captures are diagnosed. Bound
closures retain native Go cells, so later mutations are visible and escaping
factories retain distinct cells for distinct invocations.

The source's `func main` receives a private generated name. Calls to it remain
ordinary native calls; the generated process entry function still executes
source top-level statements in order. Three-clause loops retain their actual
Go initializer in the loop header, preserving per-iteration cells for closures.

Bare `func` has no directly equivalent ordinary Go type. A function returning
a literal derives its concrete result signature from that literal. A bare
function parameter derives its concrete signature from callable arguments in
the source unit. Calls with unresolved or incompatible signatures remain
explicit diagnostics; the compiler does not fall back to reflective execution.

## Defer and panic

Native Go performs call argument evaluation, defer ordering, stack unwinding
and direct-only recovery. Generated bookkeeping retains the source panic chain
so an unrecovered nested panic reports the original and replacement values,
without emitting an unrelated Go stack trace. Unrecovered source panics exit 2.
A successful recover removes the most recent source panic; unsuccessful
recovery yields the interpreter's empty payload and status 1 when observed
through the shell boundary. Named results remain ordinary native Go result
bindings and can be changed by deferred closures.

Type-helper hooks provide signature types, receivers, selectors, type
assertions, generic parameter/argument text and composite expressions. Type
and interface behavior still requires its own complete acceptance inventory;
using a native Go operator alone does not establish interpreter parity.

## Supported import boundary and remaining work

Ordinary calls into reviewed standard-library packages emit native imports and
calls. Module-aware external import resolution remains separate work. Imported
value bindings currently return an explicit diagnostic: the interpreter stores
these results as object cells whose shell rendering differs from native scalar
rendering. For example, an imported string result interpolates as a JSON string
including quotes. The complete runtime bridge must preserve that projection
and associated object identity before these bindings can be accepted.

Other outstanding callables/runtime combinations include dynamically varying
bare-function signatures, shell-function callbacks and deferred shell-command
forms, full shell scalar coercion/parameter expansions, and runtime error
classification for native operators. The emitted core is a delivery slice,
not a claim that all certified language rows or process boundaries are complete.

## Verification

The combined `go test ./lower/...` suite compiles actual executable artifacts
and compares their stdout, stderr and exit status with interpretation of the
same sources. Callable tests cover captures and mutation, escaping factories,
source main, per-iteration deferred closures, function/method values, named
results, switch, constant groups/integer range, inferred function parameters,
reviewed import calls, direct-only recover, nested panic restoration and exit
status. Unsupported cases remain diagnostics; a test count is not the full
language denominator.

## Scalar shell and variadic follow-up

The next native slice adds simple shell-local scalar bindings, result-less
function return statuses, typed variadic signatures and spread, and the quoted
slice iteration used by variadic helpers. Typed variadic length/index parameter
expansions are explicit operations; unsupported shell expansions still fail.

Shell arguments now follow the existing structured selector/index convention:
a path rooted in a live binding projects that field or element. This applies
to command arguments, not arbitrary quoted text or typed return words. Return
values receive the declared native result conversion at the callable boundary,
including named scalar underlying types. Reviewed dot imports are resolved
against the actual package export scope.

These repairs were measured against additional public profile fixtures. Rich
root-object JSON rendering and exact-rational scalar rendering remain separate
representation obligations. Inferred integer constant expressions exceeding
int64 use native `math/big.Int` storage; arithmetic on those stored values still
requires a separate numeric emitter.

## Binding and type-switch follow-up

Tuple short declarations preserve existing bindings while adding new storage;
source initializers still run in entry order. Constant groups retain omitted
initializer expressions and the interpreter's lexical `iota` shadowing. Native
type switches bind the selected concrete value only inside its selected case.
The `make` emitter consumes the parser's committed type argument once, even
when its compatibility word is also retained in the AST. Scoped `${name-unset}`
expansions preserve the source's static binding lifetime.
