# Resident channels (Sprint 374)

Reuse the existing interpreter-owned Go `chan any`, whose Go-source payloads
are assignment-copied interpreter cells. Extend the existing allocation
certificate from select participants to all channel types. Preserve it when
checked types are reparsed for inferred declarations and assignment temporaries;
previously a field assignment could silently allocate in the worker despite a
certificate on the original source type. No live-channel migration is attempted.

## Escape rule

The package planner groups named and directional views by bidirectional element
type. Assignments, nested channel payloads and select arms connect those groups.
An imported object/signature, native argument/result/receiver or unknown call
target marks its reachable channel types native. Only statically bound local
functions/methods, function literals, builtins and conversions are known local
calls. Indirect calls are conservatively treated as potential native calls.

The prototype refuses **every** package certificate if an expression/type
contains interface storage (including nested aggregate storage), if unsafe is
used, or if a callable/local method-bearing value is passed to an unknown/native
callee. This deliberately covers erased channel identities and callbacks that
can expose captured package storage. It also declines harmless programs, such
as ones using recover or an unrelated interface variable. Unknown provenance
keeps the original native allocation policy; existing reference-bearing channel
restrictions are unchanged. There is no original-program native fallback.

## Operations

- Select uses the established local arbitration and task cancellation machinery.
  Groups connected to a native-origin arm stay native. Existing mixed-domain
  arbitration remains available for reference-bearing local channels beside
  native channels; only one operation commits.
- Close wakes blocked senders, preserves buffered values, and retains nil/closed
  panic behavior. Range drains until closed, evaluates the operand once, and
  preserves channel identity through fields, indices and directional views.
- Len/cap read the resident queue. An aggregate channel's empty text carrier is
  not a nil test: the channel metadata holds its identity.
- Nil sends/receives/ranges block, nil select arms are disabled, and nil len/cap
  are zero. Direction restrictions remain enforced by types and operand checks.
- Blocking operations release the task launch handshake, observe cancellation,
  and retain deadlock detection when no interpreted task can make progress.

## Evidence and limit

Tests compare small programs with native Go and compiled output, inspect positive
and negative allocation certificates, check cancellation, and trace a resident
program to assert zero native channel operations. Each benchmark runs the same
interpreted AST in both domains, clearing only allocation certificates for the
native baseline. Timings come from the warmed program loop, excluding parsing,
worker startup and compilation; two communications count as two operations.
The loop clock endpoints still include their small bridge latency.

See `s374-resident-BLOCKERS.md` for the unchanged ken/chan root's remaining
60-second failure. The channel throughput improvement does not establish that
this root meets its bound.

Remote throughput (`-benchtime=2000x`, 4,000 communications per row), Go 1.27.1,
Darwin arm64 / Apple M4 Max:

| Workload | Native ns/op | Resident ns/op | Native ops/s | Resident ops/s |
| --- | ---: | ---: | ---: | ---: |
| Buffered | 44,027 | 3,486 | 22,713 | 286,897 |
| Struct field | 48,235 | 5,385 | 20,732 | 185,703 |
| Rendezvous | 53,609 | 5,813 | 18,653 | 172,039 |

These are single short samples, not confidence intervals. Baseline domain
selection is reproduced by clearing the certificate; the source loop and
interpreter are otherwise identical. Gains are 12.63x, 8.96x and 9.22x.
