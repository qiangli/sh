# Source-profile diagnostics

`lower/profile_diagnostics.go` decides the statically invalid programs of the
public Go profile and reports them with the **source** diagnostic identities.

```go
list := lower.CheckProfile(file, origin)   // nil when nothing is statically decidable
list := lower.CheckProfileWithFacts(file, origin, facts)
```

It is a separate entry point from `lower.Compile`, not a change to it. A caller
runs it first; a non-nil result is a semantic reject, so no Go is emitted and
each `Diagnostic.Text` is printed followed by `\n` with status 2.

## API contract for the core caller

`.agents/handoff-profile-diagnostics.md` carries the same contract for the
manager; it is repeated here because that directory is not committed.

```go
func CheckProfile(file *syntax.File, origin string) ErrorList
func CheckProfileWithFacts(file *syntax.File, origin string, facts *ProfileFacts) ErrorList

type ProfileFacts struct {
	Types   map[string]syntax.BashPPTypeExpr // declared type -> underlying (nil = name only)
	Aliases map[string]bool                  // subset of Types declared with `=`
	Methods map[string][]ProfileMethod       // receiver type name -> declared methods
}

type ProfileMethod struct {
	Name    string
	Pointer bool // pointer receiver: not in the value method set
}
```

* The check never executes, expands, imports or compiles anything; it walks the
  positioned tree only.
* `nil` means "not statically rejectable". A non-nil `ErrorList` is non-empty and
  ordered as the interpreter emits, **not** by position.
* `origin` is the name used in the `<origin>: line N: ` prefix. An empty origin
  renders `bash`, matching the interpreter's fallback.
* Print `Diagnostic.Text` (or equivalently `Diagnostic.Error()`) followed by
  `\n` for each element, emit no Go, and exit 2. Do not re-render from `Code`
  and `Msg`: the prefix is a per-diagnostic fact and one diagnostic has no code.

## Why this is not the lowering type check

`lower.Compile` type-checks the emitted Go and surfaces go/types' own wording
under `LOWER-ETYPE`. That is a different code, a different sentence and a
different position from what the source surface prints:

| case | `LOWER-ETYPE` today | source |
|---|---|---|
| `var text string = 1` | `cannot use 1 (untyped int constant) as string value in variable declaration` | `conversions/…: line 1: BASHPP-EASSIGN-TYPE: cannot use Int constant as string in declaration` |
| `for 1 + 1 { … }` | `non-boolean condition in for statement` | `BASHPP-EFOR-COND: for condition must be boolean, got Int` |
| `func (v Missing) M()` | `LOWER-EUNDEFINED: undefined: Missing` | `invalid receiver type Missing (type is not declared in this session)` |

Translating one into the other would be a message rewrite that happens to pass
the corpus. Every rule here is instead a structural predicate over the
positioned tree; there is no fixture-name table and no go/types string anywhere
in the implementation.

## What is certified

The 15 `semantic-reject` rows of the public phase contract
(`docs/lowering/go-profile-phases.tsv`), which produce 16 diagnostics —
`cap-type-neg` records two. For each one the checker reproduces the recorded
process stderr byte for byte, and the tests re-derive those bytes by running
this checkout's interpreter rather than trusting the manifest.

| id | code | prefixed |
|---|---|---|
| `assert-impossible-neg` | `BASHPP-EASSERT-IMPOSSIBLE` | no |
| `cap-type-neg` | `BASHPP-EBUILTIN-TYPE`, then `BASHPP-ESHORT-NONEW` | no, then yes |
| `struct-literal-mixed-neg` | `BASHPP-ESTRUCT-MIXED` | no |
| `typed-overflow-neg` | `BASHPP-EEXPR-CONVERT` | yes |
| `for-cond-nonboolean-neg` | `BASHPP-EFOR-COND` | no |
| `if-cond-nonboolean-neg` | `BASHPP-EIF-COND` | no |
| `range-scalar-arity-neg` | `BASHPP-ERANGE-ARITY` | yes |
| `switch-case-type-neg` | `BASHPP-ESWITCH-TYPE` | no |
| `assign-kind-mismatch-neg` | `BASHPP-EASSIGN-TYPE` | yes |
| `const-overflow-int8-neg` | `BASHPP-EEXPR-CONVERT` | yes |
| `short-decl-no-new-neg` | `BASHPP-ESHORT-NONEW` | yes |
| `cannot-infer-neg` | `BASHPP-EGENERIC-INFER` | no |
| `constraint-violation-neg` | `BASHPP-EGENERIC-CONSTRAINT` | no |
| `missing-method-neg` | `BASHPP-EINTERFACE-MISSING` | no |
| `undefined-receiver-neg` | *(none)* | no |

Two properties of that table are load-bearing and are why `Diagnostic.Text`
exists:

* **The prefix is per-diagnostic, not per-code.** `BASHPP-ESHORT-NONEW` is
  prefixed and `BASHPP-EIF-COND` is not; `cap-type-neg` prints one of each. A
  consumer cannot decide the prefix from `Code`.
* **One diagnostic has no code.** The receiver diagnostic is legacy plain text.
  Giving it a `BASHPP-E…` code to make the shape uniform would change the bytes,
  so it is reported with an empty `Code` and its exact text.

`Text` is additive: `Code`, `Msg`, `Node` and `Pos` are populated as before and
`Diagnostic.Error()` is unchanged for every diagnostic that carries no `Text`.

## Rules

Each rule is stated structurally and fires only when its premise is *decided*.
An undecided operand, an unmodelled node or an inexact constant operation makes
the rule silent — that direction costs a diagnostic, the other direction costs a
working program.

**Registries.** A pre-pass records every declared type (name, alias flag,
underlying type), every method (receiver type, name, pointer receiver) and every
function. Declarations are hoisted deliberately: a function body may legally
mention a type declared textually after it, because the body does not run until
the call.

**Scopes.** A scope is pushed for every block, function body, `if`/`for`/`switch`
header, switch arm, `range` and subshell. Function parameters, results and the
receiver are bound. Bindings record a declared type and, where it is decided, a
constant value.

**Constant facts vs. mutable bindings.** A declaration's initializer is *not* the
value a later read observes: `n := 128; n = 1` leaves `n` holding 1, and
`p := &n; *p = 1` does the same through an alias the assignment statement never
names. Deciding which write reaches which read is flow and alias analysis, which
this checker does not do. A pre-pass therefore records every identifier the file
ever assigns, updates, iterates over or takes the address of, and a name in that
set contributes no constant value at all — only its declared type, which
assignment cannot change. The scan is name-based and file-wide on purpose:
over-approximating costs diagnostics, under-approximating costs working
programs. Invalidating only direct assignment targets would not cover the alias
case.

**Method sets.** A method set is the direct methods of the named type plus the
methods promoted from every embedded field, walked recursively through named
types, pointers and embedded interfaces, with a cycle guard. Absence is never
concluded from a partial walk: if any part of the embedded graph is not modeled,
the whole set is reported undecided and no diagnostic is produced. Whether an
embedded *value* also promotes its pointer methods depends on the outer value's
addressability, which is not a property of the type, so those are included —
over-approximating the set loses a diagnostic rather than rejecting a valid
program. The receiver form written at the top level is still honoured exactly: a
pointer-receiver method stays out of a value's own method set.

**Constant evaluation.** `go/constant` over literals, `true`/`false`, bound
constants, parentheses, unary `+ - !` and the binary operators. Numeric kinds are
promoted to float to unify; mixed non-numeric kinds are *not* unified, which is
what leaves a string-versus-integer comparison undecided rather than wrongly
true. Division by zero, an inexact shift count and a mismatch between two named
types all yield "undecided".

| rule | fires when | position |
|---|---|---|
| `EBUILTIN-TYPE` | `len`/`cap` (unshadowed, one argument) whose operand is a decided scalar; `cap` rejects every scalar kind, `len` rejects all but `String` | the call |
| `ESHORT-NONEW` | no non-blank name on the left of `:=` is new **in the innermost scope**, and the binding was not left incomplete by a rejected right-hand side | the declaration |
| `ESTRUCT-MIXED` | a composite literal whose type resolves to a struct mixes keyed and positional elements (slice and map literals may legally mix them) | the literal |
| `EASSIGN-TYPE` | a declaration with a written scalar type and a decided untyped constant initializer of an unassignable kind | the initializer |
| `EEXPR-CONVERT` | the same, assignable but not representable in the written integer type; grouped constants apply iota repetition first | the initializer, or the const spec |
| `EIF-COND` / `EFOR-COND` | a header condition whose decided kind is not `Bool` | the condition |
| `ERANGE-ARITY` | a `range` over a decided integer with more than one iteration variable | the range expression |
| `ESWITCH-TYPE` | a tag and a case expression that are both decided untyped constants whose kinds differ and are not both numeric | the case expression |
| `EGENERIC-INFER` | a call with no explicit type arguments to a function with a type parameter that appears in **no** parameter type | the call |
| `EGENERIC-CONSTRAINT` | a `comparable` type parameter bound to a slice, map or function type | the call |
| `EINTERFACE-MISSING` | `var i I = v` where `v`'s type is a decided named non-interface type whose *fully walked* method set lacks a method `I` requires | the initializer |
| `EASSERT-IMPOSSIBLE` | `i.(U)` where `i`'s type is a decided interface, `U` is a concrete declared type, and `U`'s method set lacks a method the interface requires; the comma-ok form is rejected too, because it is the assertion that is impossible | the assertion |
| *(uncoded)* | a method whose receiver type name is not declared in the session or the supplied facts | the receiver type |

**Ordering and stopping.** The interpreter exits 2 at its first source
rejection, so the checker stops there too. The one exception is a short
declaration: its right-hand side is checked first and the declaration-form
diagnostic follows, which is exactly the two diagnostics `cap-type-neg` records —
in emission order, which for that case is *not* position order.

## Deliberately not decided

This is a frozen partial slice, not a compiler front end. It is silent on:

* **The five runtime rows** the phase contract assigns to the artifact —
  `assert-fail-neg`, `nil-deref-neg`, `readonly-clear-neg`,
  `readonly-bypass-neg`, `panic-unrecovered`. They are rejected, but only after
  running, and a test asserts this checker stays quiet while the interpreter
  still reports them.
* **Source diagnostics outside the 15**: tagless-switch case kinds,
  `BASHPP-EGENERIC-ARITY`, `BASHPP-ESTRUCT-POSITIONAL`, `BASHPP-ECONST-EXPR`,
  `BASHPP-EINTERFACE-SIGNATURE`, alias receivers, and every other rejection the
  interpreter can make. Unsupported is never counted as certified.
* **Method signatures.** Interface satisfaction is decided on method names only,
  so a name-matching method with the wrong signature is not rejected here.
* **Promotion depth and ambiguity.** Promoted methods are collected as a union,
  not by Go's shallowest-depth rule, so a method that is ambiguous at equal depth
  (and therefore not promoted) is still counted as present. That direction only
  suppresses diagnostics.
* **Any read of a name the file writes to.** Once a name is assigned, updated,
  iterated over or address-taken anywhere in the file, no rule that needs its
  value fires — including at reads that provably precede every write.
* **Constraints other than `comparable`.** Unions, approximations and interface
  constraints are left to the interpreter.
* **Typed-operand assignability.** A declaration whose initializer already has a
  named type is an assignability question between two named types and is out of
  slice.
* **Diagnostic order across functions.** Function bodies are walked at their
  declaration site rather than their call site. Every certified case has its
  diagnostics inside one function, so this is not exercised; a program whose
  diagnostics span several functions is not claimed.

## Facts

`ProfileFacts` supplies declarations from earlier in the same session, which a
single positioned file cannot carry. Facts are merged *under* the file, so a
caller cannot make the checker reject a program by supplying stale facts — they
can only make it quieter (a receiver type it would otherwise call undefined) or
more precise (a method set the file alone does not complete).

## Tests

`lower/profile_diagnostics_test.go` embeds the 15 semantic-reject sources, the 5
runtime negatives and 40 artifact-run sources. It asserts:

* exact stderr bytes for the 15, cross-checked against this checkout's
  interpreter rather than against the manifest alone;
* nil for the 5 runtime negatives, while the interpreter still rejects them;
* nil for the 40 valid programs;
* the same rules on 15 fully renamed rewrites — different variable, constant,
  type, method and function names, different line numbers, different origins —
  each expectation being the interpreter's own stderr for the rewritten program;
* 19 near-miss controls one token away from a rejected program, each required to
  be accepted by both the checker and the interpreter;
* both origin renderings, the uncoded diagnostic, the secondary ordering, and
  that the check leaves no filesystem or environment effects;
* three promoted-method programs and three mutable-value programs from the
  independent adversarial review, each required to run cleanly under the
  interpreter and to be accepted here, plus a control confirming an unwritten
  name is still a usable constant fact;
* that operands an operator does not support leave the expression undecided
  rather than panicking inside `go/constant`.

`TestCheckProfileCorpusSweep` runs the full 120-row corpus when
`BASHPP_PROFILE_CORPUS` points at the public checkout: 15 exact, 105 clean.
