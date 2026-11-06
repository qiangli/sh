# Compiled foreign scalar arguments

Sprint 562, story a37248198e5f.

The interpreted non-direct typed fence boundary parses textual `int`,
`float64`, and `bool` arguments with `strconv.ParseInt` (base 0, 64 bits),
`ParseFloat` (64 bits), and `ParseBool`. Consequently the unchanged Python
boundary example `f := py.as_float("2.5")` passes a Python float, whereas the
old compiled Go wrapper rejects the string with `LOWER-ETYPE`.

The bounded fix routes direct scalar calls through private coercing adapters
and converts their captured parameters before dispatch. Public wrappers retain
their declared parameter types and forward to those adapters, preserving
function-value assignment and passing. Matching native values
keep their value and type. A closed scalar type switch refuses arbitrary
objects without invoking formatting, marshaling, or other host methods.
Dynamic calls, direct Python imports, handles, callbacks, collections,
variadic signatures, and ordinary Go function calls retain their paths.
No interpreter or polyglot runtime implementation changes are required.

Both ordinary and explicit-error wrappers use the same conversion generator.
Invalid input never reaches the worker. Explicit-error calls return zero and
the conversion error with status zero. Ordinary calls report the conversion
failure and a nonzero status. The existing program context, worker lifecycle,
and capability policy remain in force.

Go captures argument expressions exactly once before adapter conversion.
A generated-artifact test supplies an effectful string expression to both
success and error adapters and asserts its evaluation count. Focused source
cases also compare interpreted and compiled numeric strings, a returned
float value, and invalid strings in foundation and execution modes. The
native Python oracle executes the original fence definitions with the same
explicit scalar conversion at the caller boundary; Python annotations alone
do not perform that conversion.

The regression copy of `python-by-example/00-boundary/scalars.bsh` and its
golden is byte-identical to the corpus. It must pass in full; a result-rendering
mismatch is not grounds to rewrite its source or expected output.

Run the bounded gate with a fresh output directory and the corpus root:

```sh
bash docs/s562/verify-python-scalars.sh RESULTS CORPUS_ROOT
```

The gate requires the baseline to fail specifically with `LOWER-ETYPE`,
verifies fixture identity, and rejects missing tests, skips, and failures.
Remote deployment uses a reviewed immutable bundle and parameterized driver;
only the manager executes the staged gate.

## Foreign bytes results

The authorized follow-up preserves the interpreter's bytes shell projection:
`string(value)` for a foreign `[]byte` result. A dedicated projection kind
marks this provenance in execution-mode cells; foundation mode uses the same
bounded byte conversion. Typed storage and subsequent bytes arguments remain
`[]byte`. Nil and empty byte slices render empty, and non-UTF-8 bytes retain
their exact contents. Ordinary slice and object projection is unchanged.

The focused gate includes both modes of the exact original corpus, byte result
controls and typed reuse, plus a generated-wrapper regression asserting that
fixed leading scalar parameters of variadic exports retain their prior Go
types. Variadic ingress is not widened by this repair.

Typed public function assignments and higher-order passing are checked against
both the lifecycle baseline and the repaired compiler in both modes. The
execution wrapper retains its existing explicit program parameter. The gate
requires these baseline controls to pass, in addition to the original scalar
string call failing on that baseline and passing after the repair.

## Shell variables at foreign call sites

The C division example supplies `example_args="$*"` and `example_argc="$#"`
through shell assignments, then calls
`c.example_main(example_args, example_argc)`. Those bindings live in the
execution session rather than the generated Go declaration table. Quoting
unknown names during lowering lost both the argument string and its count.

For legacy bare identifiers at fenced foreign call sites in mixed shell
programs, resolve names through the existing session lookup at invocation
time. An unset name retains the literal fallback; a present empty value stays
empty. Declared typed values, authoritative typed expressions, quoted strings,
callback names, and public function signatures retain their existing paths.
The lookup is an argument expression evaluated once before scalar conversion;
it adds no worker dispatch or host-object access.

`verify-foreign-shell-arguments.sh RESULTS C_CORPUS_ROOT` checks an unchanged
copy of the C division source, arguments and golden, demonstrates the exact
`example_argc` conversion failure on the verified integration baseline, and
requires native C, interpreted and compiled agreement after the repair.
Additional controls cover scalar and string shell variables, empty versus
unset, reassignment, lexical shadowing, quoted literals, typed float and string
values, and both successful and invalid explicit-error calls. Run the original scalar gate too
to retain its function-value, bytes, cancellation and evaluation-count checks.
