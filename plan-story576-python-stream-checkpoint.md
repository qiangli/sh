# S221.5 — streaming adapter checkpoint

Sprint: #221 · Story: #576 · Story-ID: 050586a14a25

## Implemented and measured

Python source fences with declared Iterator[T] return a lazy, single-consumer
iterator. The first range starts a real worker child, uses B14's bounded
line-channel primitive, decodes NDJSON and closes/reaps on completion, error,
return or early break. Merely assigning an iterator starts no child.

Declared TextIO -> Iterator[str] exports are real byte-pipeline filters.
An ordinary iterator export invoked as a command emits NDJSON. Both use
the existing pipeline process-group hooks and native process cleanup.
No special inherited file descriptor is required: stream requests are argv
data and stdin remains the ordinary byte pipe, including on Windows.

Generated wrappers and range lowering use the same polyglot StreamCommand
and the process story's public interp.StartLineCommand bridge, not another
line-process implementation. Interpreted/lowered smoke fixtures pass for
iterator values and a printf | py.upper | cat filter. A focused race gate
covers those paths and rejected marker declarations.

Original-source validation: CPython v3.14.0,
Lib/test/test_generators.py GeneratorTest.test_issue103488, PSF-2.0:
the original `yield; raise ValueError()` body runs as a typed island and
must preserve its partial value before reporting the error. Additional
local fixtures cover empty/many streams, infinite-generator early break,
NDJSON command output and TextIO pipeline transformation.

## Explicit residuals — story is NOT complete

- Rust Iterator returns are not yet implemented.
- The declared foreign Iterator/TextIO adapters exist; the previous candidate's
  invented ordinary-function @go.stream/@go.filter markers are now refused,
  never silently treated as successful no-ops. A general decorator adapter
  contract is not delivered.
- Imports of Python files have no analyzed Iterator signature; this chunk
  targets declared source-fence exports.
- Iterator values are rangeable objects, not yet general Bash# channel values;
  assigned-variable range is tested, direct range-over-call needs parser work.
- A streaming invocation owns a separate child. It does not share mutable
  globals with the persistent non-streaming module worker.
- Python finalizers are not promised on forced child cancellation.
- Native Windows runtime and signal/job-control integration gates remain open.
- The old candidate's lower/shellrt lineprocess/pipelinefilter copies are
  not used by these generated adapters and should not be delivered as a second
  substrate. Manager should select only the needed pipeline helper or replace
  it with the shared process API.
- The emitter records iterator binding names source-wide; lexical shadowing
  needs a dedicated diagnostic/ownership test before claiming general support.

## Focused gate

`go test -race -tags full ./interp ./lower -run 'TestPython(Iterator|TextIO)|Test.*GoStreamGoFilterDecorator' -count=1 -timeout=90s`

This checkpoint intentionally does not claim complete Sprint 221 acceptance.
