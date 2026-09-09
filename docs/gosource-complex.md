# GoSource complex scalar implementation

Sprint: #118; Story: #55; Story-ID: `e7025e06b7da`.

The scoped implementation enables finite complex64/complex128 GoSource scalar
declarations, expressions, conversions, arithmetic, equality, assignment,
local function arguments/results and the complex/real/imag builtins. Classic
Bash++ keeps its existing complex rejection. Exact Go constant components are
converted into typed expression trees; runtime operations and assignments round
components to their declared width.

Imported dependency calls still execute through the persistent native bridge.
Its protocol now encodes complex values with kind `complex`, their native type,
and Go's round-trippable complex number spelling. Reflection only invokes
imported dependencies such as math/cmplx and fmt. Original program statements,
expressions, declarations and functions are interpreted; no whole program is
forwarded to native Go.

`TestGoSourceComplexThreeModes` builds/runs the unchanged original with native
Go, executes the same original through Runner, then lowers and builds the
product output. It removes both source files before running the actual compiled
artifact and compares stdout/stderr. Five cases cover the original Tour example,
arithmetic/equality, width/builtins/bridge type formatting, exact rational complex
constants, and assignment/compound update/local calls. The original Tour fixture
is copied verbatim from the pinned corpus `tour/_content/tour/basics/basic-types.go`;
SHA-256 `b214090d7339363b9fb71a610c679273a2f75be7b4ea7dad4b1a3316868db4fa`.

This does not establish all Go complex semantics. Non-finite complex results
remain explicitly unsupported by the existing constant carrier; signed-zero
fidelity and complex values nested in local aggregate carriers require separate
coverage/work. Local defined types crossing the native dependency boundary
retain the bridge's existing registry limitations. These limits must remain
visible in corpus evidence; no full sprint or corpus acceptance follows from
the five focused tests.

Validation commands (using the pinned Go SDK):

```sh
GOMAXPROCS=2 go test -p 2 ./interp ./gosource -run 'TestGoSource|TestPrintAndReset|TestBashPP' -count=1
GOMAXPROCS=2 go test -p 2 ./interp -run '^TestRunnerRunConfirm$' -count=1
```

Measured focused suite: PASS (`interp` 70.7 s, `gosource` 0.6 s). The required
external Bash comparison was attempted with both the host default shim and
explicit Homebrew Bash 5.3.15. It fails existing native-Bash fixture expectations,
including `TestRunnerRunConfirm/#1957` array assignment xtrace quoting. This
test runs the external Bash executable; it does not exercise the GoSource
complex implementation. Its failure is not reported as a passing regression.
