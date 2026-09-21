# Foreign streaming adapters

Sprint: #221 · Story: #576 · Story-ID: 050586a14a25

The original fresh-process Python checkpoint is superseded by persistent
worker iterator operations. Python `Iterator[T]`/`Generator[T,...]` exports and
Rust `impl Iterator<Item = T>` exports generate wrappers through the existing
foreign wrapper emitter. `Iterator<Item = Result<T,E>>` separates a Rust error
from earlier values. Python `TextIO -> Iterator[str]` exports adapt byte-pipeline
input through the existing callback frames. No separate worker, process
supervisor, value codec, or decorator registry is introduced.

The public iterator is single-consumer and rangeable. Its delivery uses the
B14 bounded Go channel: one producer closes, the queue is fixed, wait is
exact-once, errors remain separate, and range exit closes the iterator. This
is the internal Go-channel contract; public channel receive/select syntax is
not introduced. A variable merely holding an iterator does not start work.
Lexical binding ownership in lowering is scope-local, not a source-wide name
classification. Element conversion uses the existing foreign result adapter.

The worker owns iterator state and normal module state together. Each next
request produces one value; close executes Python finally/Rust drop without
resetting unrelated state. Early close first closes delivery and allows an
in-flight next to settle; an unresponsive next is cancelled after 100 ms and
its worker is killed/reaped through the existing Module lifecycle. A cancelled
worker invalidates its iterators. This is cancellation, not successful partial
completion. Filters read bounded byte chunks, decode UTF-8 incrementally, and
never share pipeline bytes with protocol stdin (including Windows).

Source-level decorator interaction uses the existing `*Call`/`Next` stack and
`@go.error` wrapper around a body that invokes the generated adapter. The
fixture proves before/body/result order; no no-op `@go.stream` or `@go.filter`
markers are added. The runnable sample is `examples/foreign-streams.bpp`.

Pipeline process-group reservation is implemented separately in
`interp/bashpp_stream_group*.go`: workers change their own group through an
acknowledged operation on the same protocol, avoiding the POSIX prohibition
on a parent changing a child group after exec. Worker module leases serialize
pipeline ownership. Classic pipelines are unaffected. Pipeline stream commands
must be statically resolvable before launch (literal module commands or named
shell wrappers); an unreserved dynamic command is refused. One persistent
module cannot occupy two concurrent pipeline stages because its one protocol
may be waiting on an input callback; different modules can occupy distinct
stages. Sequential calls within one stage are supported.

## Source provenance and fixtures

- CPython `23116f998f6789d8c2fbe5ed5b8146854c8c2a4f`, PSF-2.0,
  `Lib/test/test_generators.py`: empty/exhausted generators, partial output then
  exception, `close`/finally, early abandonment. Independent bridge cases:
  `polyglot/iterator_test.go`, `interp/bashpp_foreign_iterator_test.go`.
- Rust `59807616e1fa2540724bfbac14d7976d7e4a3860`, MIT OR Apache-2.0,
  `library/core/src/iter/traits/iterator.rs` next/termination and
  `library/core/src/result.rs` error propagation. Independent bridge cases:
  `TestRustIteratorLanguage`, `TestRustIteratorLowered`.
- xonsh `e2b76f7fa54d2272c0b5a84b77a816fe882bee48`, BSD-2-Clause,
  `xonsh/procs/pipelines.py`/`tests/xintegration/test_integrations.py`:
  streaming aliases, partial output/failure, downstream early exit, job signals.
  Independent byte-filter and group cases live in `interp` and `lower`.
- Go `862c888e612ac346c7c4d99c9392bdfd265f33b0`, BSD-3-Clause,
  `src/os/exec/exec_test.go` lifecycle. Reuse the actual B14 substrate fixture
  `TestBashPPProcessBoundedBackpressure` and cancellation/oversize/reap tests.

Focused acceptance command:

```
go test -race -tags full ./polyglot ./interp ./lower -run 'TestIterator|Test(Python(Iterator|TextIO)|RustIterator|ForeignStreaming)|TestBashPPProcess' -count=1 -timeout=120s
```

Three-platform and complete release gates are recorded by the sprint manager
against the final integrated candidate; this plan makes no unmeasured claim.
