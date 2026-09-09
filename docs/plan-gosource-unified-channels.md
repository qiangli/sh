# Unified scalar and dependency channels

Sprint: #118; Story: #51; Story-ID: 825f8083451e

GoSource-created channels with scalar, empty-struct, or dependency-owned element
types now live in the same authenticated native session as imported channels.
The interpreter executes every original expression, function, range body,
select body and goroutine. The helper executes channel make/close/len/cap and
atomic communication; it does not compile any original program body.

The channel domain is selected from the element type when make executes, not
from the first sent value. Original maps, slices, pointers, nonempty local
structs and other reference-bearing elements retain the local channel runtime.
Their shared storage is preserved. A select combining such a local channel
with a native-domain channel remains an explicit error before communication.
Original callback-bearing values cannot be retained by native channels.

## Evaluation and lifecycle

`CapacityExpr` preserves Go's typed capacity expression through conversion,
syntax walking, printing, typed JSON and lowering. Make evaluates it once,
before allocating, and follows Go's negative-capacity panic. The GoSource native
path does not apply the legacy shell channel-capacity limit. Local reference
channel capacity expressions also execute once. An expression constructor can
return a typed native channel; `_ = make(chan ...)` still executes its operand
and may panic before the result is discarded.

Close evaluates its typed channel operand once. Nil and repeated close raise
ordinary interpreted Go panic/recover. A two-variable receive assignment keeps
both value and boolean cells. Native range evaluates its channel once, receives
until closure, and executes iteration bodies and control flow locally.

An imported `time.Sleep` inside an original goroutine must release the launch
handshake before waiting; otherwise the unchanged timeout example waits two
seconds before its parent can even create the one-second timeout. A narrow
hook identifies actual imported time.Sleep (including its authenticated bound
function metadata), after argument evaluation, and releases that handshake for
positive durations. It does not alter other imported operations.

Session shutdown wakes blocked channel operations before joining them. Reset
uses a new session. Existing atomic whole-case validation and stale-session
checks remain in force; no loser consumption or replay buffering is introduced.

## Evidence and remaining limits

Seventeen authored programs compare raw streams across native Go, the actual
Runner and a source-free compiled artifact. They cover capacity evaluation,
capacity beyond the old shell limit, negative capacity, constructor returns,
computed close, nil/repeated close, mixed-origin atomic selection, direction,
range, worker-pool result, imported value identity, empty struct and no imports. Additional controls retain a local callback-bearing element,
compute len/cap operands once and create a directional channel.
The unchanged SHA-bound Go by Example timeouts source uses that same three-mode
check. Typed-JSON execution and cancellation/Reset controls run separately.

Unchanged original tickers, worker-pools and Tour default-selection also execute
successfully in all three modes. Their raw timing/job-order streams are retained
as diagnostic evidence, without claiming raw equality or weakening corpus
comparators. Existing local reference-channel controls stay enabled. Full corpus
acceptance remains with the sprint's independently built candidate gates.

Measured gates: combined native/unified channel race PASS 129.501 s; original
cancellation API plus both session families under race PASS 24.179 s; final
reference-domain/length/direction three-mode race PASS 7.314 s; classic and
existing typed send/receive regression PASS 53.336 s. Full gosource PASS
38.627 s and typedjson PASS 0.484 s. Native host Bash confirmation remains
FAIL (9.672 s), with its unchanged oracle diagnostics retained.

The local callback control initially exposed the separately owned lower
unnamed-receiver panic. This successor uses the manager-reviewed lower
423b0ac7 dependency and retains that exact control; no channel test was removed.
Only the following channel commit is required when that dependency is already
integrated in the parent candidate.
